package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/aetherfy/cli/internal/api"
	"github.com/aetherfy/cli/internal/archive"
	"github.com/aetherfy/cli/internal/output"
	"github.com/spf13/cobra"
)

var deployCmd = &cobra.Command{
	Use:   "deploy [path]",
	Short: "Deploy an agent",
	Long: `Deploy code to an Aetherfy agent.

The path should contain an aetherfy.yaml configuration file.
If no path is specified, the current directory is used.

The deployment process:
  1. Validates aetherfy.yaml exists
  2. Creates a ZIP archive of your code
  3. Uploads to Aetherfy
  4. Builds a Docker image
  5. Deploys to Fly.io

Files matching patterns in .afyignore will be excluded.`,
	Example: `  # Deploy current directory
  afy deploy

  # Deploy specific directory
  afy deploy ./my-agent

  # Deploy with watch mode
  afy deploy --watch

  # Deploy to specific region
  afy deploy --region fra`,
	Args: cobra.MaximumNArgs(1),
	RunE: runDeploy,
}

var (
	deployRegion string
	deployWatch  bool
	deployAgent  string
)

func init() {
	deployCmd.Flags().StringVarP(&deployRegion, "region", "r", "", "Target region (iad, fra, sin)")
	deployCmd.Flags().BoolVarP(&deployWatch, "watch", "w", false, "Watch logs after deployment")
	deployCmd.Flags().StringVarP(&deployAgent, "agent", "a", "", "Agent ID or name (reads from aetherfy.yaml if not specified)")
}

func runDeploy(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}

	// Determine path
	path := "."
	if len(args) > 0 {
		path = args[0]
	}

	// Resolve to absolute path
	absPath, err := filepath.Abs(path)
	if err != nil {
		output.PrintError("Invalid path: %v", err)
		os.Exit(1)
	}

	// Check path exists
	info, err := os.Stat(absPath)
	if err != nil {
		output.PrintError("Path not found: %s", absPath)
		os.Exit(1)
	}
	if !info.IsDir() {
		output.PrintError("Path must be a directory: %s", absPath)
		os.Exit(1)
	}

	output.PrintInfo("Deploying from: %s", absPath)
	output.Println("")

	// Validate aetherfy.yaml exists
	if err := archive.ValidateAetherfyConfig(absPath); err != nil {
		output.PrintError("%v", err)
		output.Println("")
		output.Println("Create an aetherfy.yaml file with your agent configuration.")
		output.Println("Example:")
		output.Println("")
		output.Dim.Println("  name: my-agent")
		output.Dim.Println("  runtime: python3.11")
		output.Dim.Println("  entrypoint: main.py")
		os.Exit(1)
	}

	// TODO: Parse aetherfy.yaml to get agent name if not specified
	agentID := deployAgent
	if agentID == "" {
		output.PrintError("Agent ID required. Use --agent flag or specify 'name' in aetherfy.yaml")
		os.Exit(1)
	}

	// Load ignore patterns
	ignorePatterns, err := archive.LoadIgnorePatterns(absPath)
	if err != nil {
		output.PrintWarning("Failed to load .afyignore: %v", err)
	}

	// Create tarball archive (.tar.gz)
	sp := output.NewSpinner("Creating archive...")
	sp.Start()

	tarballData, err := archive.CreateTarball(absPath, ignorePatterns)
	sp.Stop()

	if err != nil {
		output.PrintError("Failed to create archive: %v", err)
		os.Exit(1)
	}

	sizeMB := float64(len(tarballData)) / 1024 / 1024
	output.PrintSuccess("Archive created (%.2f MB)", sizeMB)

	// Upload and deploy
	sp = output.NewSpinner("Uploading and deploying...")
	sp.Start()

	client := api.NewClient()
	resp, err := client.Deploy(agentID, tarballData, deployRegion)
	sp.Stop()

	if err != nil {
		output.PrintError("Deployment failed: %v", err)
		os.Exit(1)
	}

	output.PrintSuccess("Deployment started!")
	output.Println("")
	output.KeyValue("Deployment ID", resp.DeploymentID)
	output.KeyValue("Job ID", resp.JobID)
	output.KeyValue("Status", resp.Status)

	// Watch mode - poll for status and show logs
	if deployWatch {
		output.Println("")
		output.PrintInfo("Watching deployment...")
		watchDeployment(client, agentID, resp.DeploymentID)
	} else {
		output.Println("")
		output.Println("Run 'afy logs " + agentID + "' to view logs")
	}

	return nil
}

func watchDeployment(client *api.Client, agentID, deploymentID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	lastStatus := ""

	for {
		select {
		case <-ctx.Done():
			output.PrintWarning("Deployment watch timed out")
			return
		case <-ticker.C:
			deployment, err := client.GetDeployment(deploymentID)
			if err != nil {
				output.PrintWarning("Failed to get deployment status: %v", err)
				continue
			}

			if deployment.Status != lastStatus {
				output.Printf("Status: %s\n", formatStatus(deployment.Status))
				lastStatus = deployment.Status
			}

			switch deployment.Status {
			case "completed", "running", "active":
				output.PrintSuccess("Deployment completed!")
				return
			case "failed", "error":
				output.PrintError("Deployment failed")
				// Try to get logs
				logs, err := client.GetDeploymentLogs(agentID, 20)
				if err == nil && len(logs) > 0 {
					output.Println("")
					output.Println("Recent logs:")
					for _, log := range logs {
						fmt.Printf("  %s\n", log.Message)
					}
				}
				return
			}
		}
	}
}
