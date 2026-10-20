package cmd

// afy collections update <name> --name / --description: rename a collection,
// or change its description, in its workspace (the vectors API's PATCH
// …/collections/{name}). A rename changes the name only: the collection's id,
// its stored data, its aliases (they follow the id), its schema and regions
// stay. Code that addresses it by the old name stops finding it (404).

import (
	"fmt"

	"github.com/l-td/aetherfy-cli/internal/vectors"
	"github.com/spf13/cobra"
)

var collectionsUpdateCmd = &cobra.Command{
	Use:   "update <name>",
	Short: "Rename a collection or change its description",
	Long: `Rename a collection (--name), change its description (--description), or both.

The collection stays in its workspace (moving it is 'afy collections move').
A rename changes the name only: the collection's data, its aliases, its schema
and its regions stay. Code that addresses it by the old name stops finding it.
A name already used by a collection or an alias in the workspace is refused,
and so is renaming the memory SDK's threads collection.

--description "" clears the description.`,
	Example: `  # Rename a collection
  afy collections update articles --name articles-v1 --workspace research

  # Change its description
  afy collections update articles --description "News articles, 2026"`,
	Args: refuseArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		var name, description *string
		if cmd.Flags().Changed("name") {
			name = &collectionUpdateName
		}
		if cmd.Flags().Changed("description") {
			description = &collectionUpdateDescription
		}
		return runVec(cmd, func(r *vecRun) int {
			return collectionsUpdate(r, args[0], name, description)
		})
	},
}

var (
	collectionUpdateName        string
	collectionUpdateDescription string
)

func collectionsUpdate(r *vecRun, current string, name, description *string) int {
	if name == nil && description == nil {
		return r.fail(refuse("specify --name <new> and/or --description <text>"))
	}
	if name != nil && *name == "" {
		return r.fail(refuse("--name needs the collection's new name"))
	}
	client, code := r.open()
	if code != 0 {
		return code
	}
	updated, err := client.UpdateCollection(current, vectors.UpdateCollectionRequest{Name: name, Description: description})
	if err != nil {
		return r.fail(err)
	}
	if r.json {
		return r.printJSON(struct {
			vecEnvelope
			Collection string                  `json:"collection"`
			Previous   string                  `json:"previous_name"`
			Result     *vectors.CollectionInfo `json:"result"`
		}{r.envelope(), updated.Name, current, updated})
	}
	where := workspaceLabel(client.Workspace())
	if updated.Name != current {
		fmt.Fprintf(r.stdout, "Collection '%s' renamed to '%s' in %s.\n", current, updated.Name, where)
		fmt.Fprintf(r.stdout, "Its data, aliases, schema and regions are unchanged. Code that addresses it as '%s' must use '%s' now.\n",
			current, updated.Name)
	} else {
		fmt.Fprintf(r.stdout, "Collection '%s' updated in %s.\n", updated.Name, where)
	}
	if description != nil {
		desc := "(none)"
		if updated.Description != nil && *updated.Description != "" {
			desc = *updated.Description
		}
		fmt.Fprintf(r.stdout, "Description: %s\n", desc)
	}
	return 0
}

func init() {
	collectionsUpdateCmd.Flags().StringVar(&collectionUpdateName, "name", "", "New collection name, in the same workspace")
	collectionsUpdateCmd.Flags().StringVar(&collectionUpdateDescription, "description", "", "New description (\"\" clears it)")
	collectionsCmd.AddCommand(collectionsUpdateCmd)
}
