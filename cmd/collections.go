package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/l-td/aetherfy-cli/internal/output"
	"github.com/l-td/aetherfy-cli/internal/vectors"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var collectionsCmd = &cobra.Command{
	Use:     "collections",
	Aliases: []string{"collection"},
	Short:   "Inspect and manage vector collections",
	Long: `Inspect and manage vector collections in the vectors API.

Collection names are scoped to a workspace. The workspace is --workspace when
given, else AETHERFY_WORKSPACE, else none, the same default as the SDKs. The
endpoint is --vectors-url, else AETHERFY_VECTORS_URL, else the one region
discovery names for --api-region, else the global endpoint.

To read what is in one, see afy collections get <name> and afy points count
<collection>. Loading points is the SDKs' job: they chunk and retry large
batches.`,
}

// --- LIST ---

var collectionsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the collections in a workspace",
	Example: `  # List the collections outside any workspace
  afy collections list

  # List a workspace's collections, as JSON
  afy collections list --workspace research --json`,
	Args: refuseArgs(cobra.NoArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runVec(cmd, func(r *vecRun) int { return collectionsList(r) })
	},
}

type collectionJSON struct {
	Name        string   `json:"name"`
	Description *string  `json:"description"`
	Size        int      `json:"size"`
	Distance    string   `json:"distance"`
	Status      string   `json:"status"`
	PointsCount int64    `json:"points_count"`
	Regions     []string `json:"regions,omitempty"`
	CreatedAt   string   `json:"created_at,omitempty"`
	UpdatedAt   string   `json:"updated_at,omitempty"`
}

func toCollectionJSON(c vectors.CollectionInfo) collectionJSON {
	return collectionJSON{
		Name:        c.Name,
		Description: c.Description,
		Size:        c.Config.Params.Vectors.Size,
		Distance:    c.Config.Params.Vectors.Distance,
		Status:      c.Status,
		PointsCount: c.PointsCount,
		Regions:     c.Regions,
		CreatedAt:   c.CreatedAt,
		UpdatedAt:   c.UpdatedAt,
	}
}

func workspaceLabel(ws string) string {
	if ws == "" {
		return "no workspace"
	}
	return "workspace '" + ws + "'"
}

func collectionsList(r *vecRun) int {
	client, code := r.open()
	if code != 0 {
		return code
	}
	cols, err := client.ListCollections()
	if err != nil {
		return r.fail(err)
	}
	if r.json {
		out := struct {
			vecEnvelope
			Collections []collectionJSON `json:"collections"`
		}{vecEnvelope: r.envelope(), Collections: []collectionJSON{}}
		for _, c := range cols {
			out.Collections = append(out.Collections, toCollectionJSON(c))
		}
		return r.printJSON(out)
	}
	if len(cols) == 0 {
		fmt.Fprintf(r.stdout, "No collections in %s.\n", workspaceLabel(client.Workspace()))
		return 0
	}
	rows := make([][]string, 0, len(cols))
	for _, c := range cols {
		rows = append(rows, []string{
			c.Name,
			fmt.Sprintf("%d", c.Config.Params.Vectors.Size),
			c.Config.Params.Vectors.Distance,
			fmt.Sprintf("%d", c.PointsCount),
			c.Status,
		})
	}
	output.RenderTable(r.stdout, []string{"Name", "Size", "Distance", "Points", "Status"}, rows)
	fmt.Fprintf(r.stdout, "\n%d collection(s) in %s\n", len(cols), workspaceLabel(client.Workspace()))
	return 0
}

// --- GET ---

var collectionsGetCmd = &cobra.Command{
	Use:   "get <name>",
	Short: "Show one collection",
	Example: `  # Show a collection's size, distance, point count and regions
  afy collections get articles --json`,
	Args: refuseArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runVec(cmd, func(r *vecRun) int { return collectionsGet(r, args[0]) })
	},
}

func collectionsGet(r *vecRun, name string) int {
	client, code := r.open()
	if code != 0 {
		return code
	}
	c, err := client.GetCollection(name)
	if err != nil {
		return r.fail(err)
	}
	if r.json {
		return r.printJSON(struct {
			vecEnvelope
			Collection collectionJSON `json:"collection"`
		}{r.envelope(), toCollectionJSON(*c)})
	}
	kv := func(k, v string) { fmt.Fprintf(r.stdout, "%-13s %s\n", k+":", v) }
	kv("Name", c.Name)
	kv("Workspace", workspaceLabel(client.Workspace()))
	if c.Description != nil && *c.Description != "" {
		kv("Description", *c.Description)
	}
	kv("Size", fmt.Sprintf("%d", c.Config.Params.Vectors.Size))
	kv("Distance", c.Config.Params.Vectors.Distance)
	kv("Points", fmt.Sprintf("%d", c.PointsCount))
	kv("Status", c.Status)
	kv("Regions", strings.Join(c.Regions, ", "))
	kv("Created", c.CreatedAt)
	return 0
}

// --- CREATE ---

var collectionsCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a collection",
	Long: `Create a collection for vectors of one size, compared with one distance.

Without --regions the collection is placed in every region your plan or
workspace covers; --regions pins it to a subset of those. Creating a
collection that already exists with the same settings succeeds and changes
nothing.`,
	Example: `  # A collection for 1536-dimension embeddings compared by cosine
  afy collections create articles --size 1536 --distance cosine

  # Pinned to two regions, in a workspace
  afy collections create articles --size 768 --distance dot --regions us-east-1,eu-central-1 --workspace research`,
	Args: refuseArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runVec(cmd, func(r *vecRun) int {
			return collectionsCreate(r, args[0], collectionSize, collectionDistance, collectionRegions)
		})
	},
}

var (
	collectionSize     int
	collectionDistance string
	collectionRegions  []string
)

// distances maps --distance to the name vectordb stores. The server also
// normalises case, but sending its own spelling keeps the request identical
// to the SDKs'.
var distances = map[string]string{
	"cosine":    "Cosine",
	"dot":       "Dot",
	"euclid":    "Euclid",
	"manhattan": "Manhattan",
}

func collectionsCreate(r *vecRun, name string, size int, distance string, regions []string) int {
	if size <= 0 {
		return r.fail(refuse("--size must be a whole number above 0, got %d", size))
	}
	wire, ok := distances[strings.ToLower(distance)]
	if !ok {
		return r.fail(refuse("--distance must be one of cosine, dot, euclid, manhattan, got '%s'", distance))
	}
	client, code := r.open()
	if code != 0 {
		return code
	}
	placed, err := client.CreateCollection(vectors.CreateCollectionRequest{
		Name:    name,
		Vectors: vectors.VectorParams{Size: size, Distance: wire},
		Regions: regions,
	})
	if err != nil {
		return r.fail(err)
	}
	if r.json {
		// Only what the create settled: its status and point count are a
		// `collections get` away, and a zero here would read as a fact.
		type created struct {
			Name     string   `json:"name"`
			Size     int      `json:"size"`
			Distance string   `json:"distance"`
			Regions  []string `json:"regions"`
		}
		if placed == nil {
			placed = []string{}
		}
		return r.printJSON(struct {
			vecEnvelope
			Collection created `json:"collection"`
		}{r.envelope(), created{name, size, wire, placed}})
	}
	fmt.Fprintf(r.stdout, "Collection '%s' created in %s (size %d, %s), regions: %s\n",
		name, workspaceLabel(client.Workspace()), size, wire, strings.Join(placed, ", "))
	return 0
}

// --- DELETE ---

var collectionsDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a collection and every point in it",
	Long: `Delete a collection and every point in it.

Asks you to type the collection's name first. --yes skips the question; it is
required when stdin is not a terminal, so a script cannot delete by accident.`,
	Example: `  # Delete, confirming by name
  afy collections delete articles

  # Delete from a script
  afy collections delete articles --yes --json`,
	Args: refuseArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		interactive := term.IsTerminal(int(os.Stdin.Fd()))
		return runVec(cmd, func(r *vecRun) int {
			return collectionsDelete(r, args[0], collectionDeleteYes, os.Stdin, interactive)
		})
	},
}

var collectionDeleteYes bool

func collectionsDelete(r *vecRun, name string, yes bool, stdin io.Reader, interactive bool) int {
	if !yes {
		if !interactive {
			return r.fail(refuse("not deleting collection '%s': stdin is not a terminal, so nobody "+
				"can confirm. Pass --yes to delete without the question.", name))
		}
		// The question goes to stderr, so --json keeps stdout to one object.
		fmt.Fprintf(r.stderr, "This permanently deletes collection '%s' and every point in it.\n", name)
		fmt.Fprint(r.stderr, "Type the collection name to confirm: ")
		line, _ := bufio.NewReader(stdin).ReadString('\n')
		if strings.TrimSpace(line) != name {
			return r.fail(refuse("deletion cancelled: the name typed did not match"))
		}
	}
	client, code := r.open()
	if code != 0 {
		return code
	}
	if err := client.DeleteCollection(name); err != nil {
		return r.fail(err)
	}
	if r.json {
		return r.printJSON(struct {
			vecEnvelope
			Collection string `json:"collection"`
			Deleted    bool   `json:"deleted"`
		}{r.envelope(), name, true})
	}
	fmt.Fprintf(r.stdout, "Collection '%s' deleted from %s.\n", name, workspaceLabel(client.Workspace()))
	return 0
}

func init() {

	collectionsCreateCmd.Flags().IntVar(&collectionSize, "size", 0, "Vector size (dimensions), required")
	collectionsCreateCmd.Flags().StringVar(&collectionDistance, "distance", "", "Distance: cosine, dot, euclid or manhattan, required")
	collectionsCreateCmd.Flags().StringSliceVar(&collectionRegions, "regions", nil, "Regions to place the collection in, comma-separated (default: all your regions)")

	collectionsDeleteCmd.Flags().BoolVarP(&collectionDeleteYes, "yes", "y", false, "Delete without asking")

	collectionsCmd.AddCommand(collectionsListCmd)
	collectionsCmd.AddCommand(collectionsGetCmd)
	collectionsCmd.AddCommand(collectionsCreateCmd)
	collectionsCmd.AddCommand(collectionsDeleteCmd)
}
