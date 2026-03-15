package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aetherfy/cli/internal/detect"
	"github.com/aetherfy/cli/internal/output"
	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var initCmd = &cobra.Command{
	Use:   "init [path]",
	Short: "Initialize an aetherfy.yaml configuration file",
	Long: `Scan the current directory and generate an aetherfy.yaml configuration file.

The command detects your project type and asks a few questions to build a
configuration that is ready to deploy. Run 'afy deploy' when you are satisfied
with the generated file.

All prompts can be skipped by providing flags directly (useful for scripting/CI).`,
	Example: `  # Interactive
  afy init
  afy init ./my-agent

  # Non-interactive (all flags provided)
  afy init --name my-bot --runtime python3.11 --type service --region iad --memory 256`,
	Args: cobra.MaximumNArgs(1),
	RunE: runInit,
}

var (
	initName       string
	initRuntime    string
	initEntrypoint string
	initType       string
	initRegion     string
	initMemory     int
	initKeepAlive  bool
	initWorkspace  bool
	initForce      bool
)

func init() {
	initCmd.Flags().StringVar(&initName, "name", "", "Agent name (skips prompt)")
	initCmd.Flags().StringVar(&initRuntime, "runtime", "", "Runtime: python3.11, python3.12, python3.13, node20, node22, bun, dockerfile (skips prompt)")
	initCmd.Flags().StringVar(&initEntrypoint, "entrypoint", "", "Entrypoint file, e.g. main.py or index.js (skips prompt)")
	initCmd.Flags().StringVar(&initType, "type", "", "Agent type: service or job (skips prompt)")
	initCmd.Flags().StringVar(&initRegion, "region", "", "Region: iad, fra, sin (skips prompt)")
	initCmd.Flags().IntVar(&initMemory, "memory", 0, "Memory in MB: 256, 512, 1024 (skips prompt)")
	initCmd.Flags().BoolVar(&initKeepAlive, "keep-alive", false, "Enable always-on billing (skips billing prompt)")
	initCmd.Flags().BoolVar(&initWorkspace, "workspace", false, "Enable VectorDB workspace (skips workspace prompt)")
	initCmd.Flags().BoolVarP(&initForce, "force", "f", false, "Overwrite existing aetherfy.yaml without asking")
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

var (
	reSlugUnsafe = regexp.MustCompile(`[^a-z0-9-]`)
	reSlugDashes = regexp.MustCompile(`-+`)
)

// isInteractive returns true when stdin is a real terminal.
// When false (CI, piped input, e2e tests), prompts are skipped and
// defaults are used for any missing flags.
func isInteractive() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// slugify turns a string into a safe agent name
func slugify(s string) string {
	s = strings.ToLower(s)
	s = reSlugUnsafe.ReplaceAllString(s, "-")
	s = reSlugDashes.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

func runInit(cmd *cobra.Command, args []string) error {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("invalid path: %w", err)
	}

	configPath := filepath.Join(absDir, "aetherfy.yaml")

	// Warn if config already exists
	if fileExists(configPath) && !initForce {
		if !isInteractive() {
			output.PrintError("aetherfy.yaml already exists. Use --force to overwrite.")
			return fmt.Errorf("aetherfy.yaml already exists")
		}
		prompt := promptui.Prompt{
			Label:     "aetherfy.yaml already exists. Overwrite",
			IsConfirm: true,
		}
		if _, err := prompt.Run(); err != nil {
			output.Println("Aborted.")
			return nil
		}
	}

	output.Println("")
	output.PrintInfo("Scanning your project...")

	hints := detect.Project(absDir)

	// Print detections
	if hints.Runtime != "" {
		output.PrintSuccess("Detected runtime: %s", hints.Runtime)
	}
	if hints.Entrypoint != "" {
		output.PrintSuccess("Detected entrypoint: %s", hints.Entrypoint)
	}
	if hints.VectorDB {
		output.PrintSuccess("Found 'qdrant_memory' — will suggest VectorDB workspace")
	}
	if hints.Runtime == "" && initRuntime == "" {
		if isInteractive() {
			output.PrintWarning("Could not detect runtime — you will be asked to choose")
		} else {
			output.PrintWarning("Could not detect runtime — use --runtime to specify one")
		}
	}
	selectedRuntime := initRuntime
	if selectedRuntime == "" {
		selectedRuntime = hints.Runtime
	}
	if hints.Entrypoint == "" && initEntrypoint == "" && selectedRuntime != "dockerfile" {
		if isInteractive() {
			output.PrintWarning("Could not detect entrypoint — you will be asked to specify one")
		} else {
			output.PrintWarning("Could not detect entrypoint — add 'entrypoint:' to aetherfy.yaml before deploying")
		}
	}
	if hints.HasMastra {
		output.PrintSuccess("Detected 'mastra' in dependencies")
	}
	output.Println("")

	interactive := isInteractive()

	// --- Agent name ---
	agentName := initName
	if agentName == "" {
		defaultName := slugify(filepath.Base(absDir))
		if interactive {
			namePrompt := promptui.Prompt{
				Label:   "Agent name",
				Default: defaultName,
				Validate: func(s string) error {
					if strings.TrimSpace(s) == "" {
						return fmt.Errorf("name cannot be empty")
					}
					return nil
				},
			}
			agentName, err = namePrompt.Run()
			if err != nil {
				return fmt.Errorf("prompt cancelled")
			}
		} else {
			agentName = defaultName
		}
	}
	agentName = slugify(strings.TrimSpace(agentName))

	// --- Runtime ---
	runtime := initRuntime
	if runtime == "" {
		if interactive {
			runtimeOptions := []string{"python3.13", "python3.12", "python3.11", "node22", "node20", "bun", "dockerfile"}
			runtimeDefault := 2 // python3.11
			if hints.Runtime != "" {
				for i, r := range runtimeOptions {
					if r == hints.Runtime {
						runtimeDefault = i
						break
					}
				}
			}
			runtimeSelect := promptui.Select{
				Label:     "Runtime",
				Items:     runtimeOptions,
				CursorPos: runtimeDefault,
			}
			_, runtime, err = runtimeSelect.Run()
			if err != nil {
				return fmt.Errorf("prompt cancelled")
			}
		} else if hints.Runtime != "" {
			runtime = hints.Runtime
		} else {
			return fmt.Errorf("could not detect runtime. Use --runtime to specify one (python3.11, python3.12, python3.13, node20, node22, bun, dockerfile)")
		}
	}

	// --- Agent type ---
	agentType := initType
	if agentType == "" {
		if interactive {
			typeSelect := promptui.Select{
				Label: "Agent type",
				Items: []string{
					"service  (always-on listener, e.g. API or webhook handler)",
					"job      (ephemeral, wakes up, runs, then stops)",
				},
			}
			typeIdx, _, err := typeSelect.Run()
			if err != nil {
				return fmt.Errorf("prompt cancelled")
			}
			agentType = "service"
			if typeIdx == 1 {
				agentType = "job"
			}
		} else {
			agentType = "service"
		}
	}

	// --- Region ---
	region := initRegion
	if region == "" {
		if interactive {
			regionItems := []string{
				"iad  (US East — Virginia)",
				"fra  (EU Central — Frankfurt)",
				"sin  (AP Southeast — Singapore)",
			}
			regionKeys := []string{"iad", "fra", "sin"}
			regionSelect := promptui.Select{
				Label:     "Region",
				Items:     regionItems,
				CursorPos: 0,
			}
			regionIdx, _, err := regionSelect.Run()
			if err != nil {
				return fmt.Errorf("prompt cancelled")
			}
			region = regionKeys[regionIdx]
		} else {
			region = "iad"
		}
	}

	// --- Memory ---
	memoryMB := initMemory
	if memoryMB == 0 {
		if interactive {
			memoryItems := []string{
				"256 MB   (Default)",
				"512 MB   (Recommended for AI)",
				"1024 MB  (Heavy workloads)",
			}
			memoryValues := []int{256, 512, 1024}
			memorySelect := promptui.Select{
				Label:     "Memory",
				Items:     memoryItems,
				CursorPos: 0, // default 256
			}
			memIdx, _, err := memorySelect.Run()
			if err != nil {
				return fmt.Errorf("prompt cancelled")
			}
			memoryMB = memoryValues[memIdx]
		} else {
			memoryMB = 256
		}
	}

	// --- Billing mode ---
	keepAlive := initKeepAlive
	if !cmd.Flags().Changed("keep-alive") {
		if interactive {
			billingSelect := promptui.Select{
				Label: "Billing mode",
				Items: []string{
					"auto-sleep  ($0 when idle, ~500ms cold start)",
					"always-on   (+$10/mo, 0ms cold start)",
				},
			}
			billingIdx, _, err := billingSelect.Run()
			if err != nil {
				return fmt.Errorf("prompt cancelled")
			}
			keepAlive = billingIdx == 1
		}
		// non-interactive default: false (auto-sleep) — zero value already set
	}

	// --- VectorDB workspace ---
	enableVectorDB := initWorkspace
	if !cmd.Flags().Changed("workspace") {
		if interactive {
			vectorDBDefault := "n"
			if hints.VectorDB {
				vectorDBDefault = "y"
			}
			vectorPrompt := promptui.Prompt{
				Label:     "Enable VectorDB workspace",
				Default:   vectorDBDefault,
				IsConfirm: true,
			}
			_, vectorErr := vectorPrompt.Run()
			enableVectorDB = vectorErr == nil // confirmed = yes
		}
		// non-interactive default: false — zero value already set
	}

	// --- Entrypoint ---
	// dockerfile runtime: user owns the container, entrypoint is irrelevant
	entrypoint := ""
	if runtime != "dockerfile" {
		entrypoint = initEntrypoint
		if entrypoint == "" {
			entrypoint = hints.Entrypoint
		}
		if entrypoint == "" && interactive {
			epPrompt := promptui.Prompt{
				Label:   "Entrypoint file (e.g. main.py, index.js) — leave blank to set later",
				Default: "",
			}
			ep, epErr := epPrompt.Run()
			if epErr == nil {
				entrypoint = strings.TrimSpace(ep)
			}
		}
	}

	output.Println("")
	output.PrintInfo("Writing aetherfy.yaml...")

	content := buildAetherfyYAML(agentName, runtime, agentType, region, memoryMB, keepAlive, enableVectorDB, entrypoint)

	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write aetherfy.yaml: %w", err)
	}

	output.PrintSuccess("aetherfy.yaml created.")
	output.Println("")
	output.Println("Run 'afy deploy' when you are ready to go live.")
	return nil
}

func buildAetherfyYAML(name, runtime, agentType, region string, memoryMB int, keepAlive, vectorDB bool, entrypoint string) string {
	var sb strings.Builder

	sb.WriteString("# Aetherfy Configuration File\n")
	sb.WriteString(fmt.Sprintf("name: %s\n", name))
	sb.WriteString(fmt.Sprintf("runtime: %s\n", runtime))
	sb.WriteString(fmt.Sprintf("type: %s\n", agentType))
	sb.WriteString("regions:\n")
	sb.WriteString(fmt.Sprintf("  - %s\n", region))
	sb.WriteString(fmt.Sprintf("memory_mb: %d\n", memoryMB))

	if keepAlive {
		sb.WriteString("keep_alive: true\n")
	} else {
		sb.WriteString("keep_alive: false\n")
	}

	if runtime != "dockerfile" {
		if entrypoint != "" {
			sb.WriteString(fmt.Sprintf("entrypoint: %q\n", entrypoint))
		} else {
			sb.WriteString("# entrypoint: main.py  # TODO: set your entrypoint file\n")
		}
	}

	if vectorDB {
		sb.WriteString(fmt.Sprintf("workspace: %s-workspace\n", name))
	}

	sb.WriteString("\n# --- ADVANCED SETTINGS ---\n")
	sb.WriteString("# Uncomment below to enable multi-agent spawning\n")
	sb.WriteString("# spawn:\n")
	sb.WriteString("#   enabled: true\n")
	sb.WriteString("#   workspace: shared-workspace\n")
	sb.WriteString("#   workers:\n")
	sb.WriteString("#     - sub-agent-1\n")

	return sb.String()
}
