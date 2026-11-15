package api

import "net/url"

// RuntimeDetail is what the platform says one offered runtime IS (GET /deploy-options: runtime_details), so a client
// chooses among the runtimes by these fields and never by reading a name.
type RuntimeDetail struct {
	// Language is the language key ("python", "node", "bun"): the keys DefaultRuntimes uses and a project is detected as.
	Language string `json:"language"`
	// Version is the language version the image carries ("3.14", "24").
	Version string `json:"version"`
	// TypeScript is Node with tsx: a .ts entry point runs with no build step.
	TypeScript bool `json:"typescript"`
	// Entrypoint is the file run when aetherfy.yaml names none.
	Entrypoint string `json:"entrypoint"`
}

// DeployOptions is GET /deploy-options: the regions and memory sizes an agent
// can be given, each with whether the caller's plan allows it. The same source
// the dashboard's configurator offers them from, so `afy init` never carries a
// list of its own.
type DeployOptions struct {
	// Runtimes are the runtimes a deploy accepts, in the server's order.
	Runtimes []string `json:"runtimes"`
	// RuntimeDetails is each offered language runtime's fields, keyed by its name. The `dockerfile` mode is a runtime
	// with no language, so it has none.
	RuntimeDetails map[string]RuntimeDetail `json:"runtime_details"`
	// DefaultRuntimes is the runtime to pick for a language when the project names no version,
	// keyed by language ("python", "node"): the platform's choice, so no client carries one.
	DefaultRuntimes map[string]string `json:"default_runtimes"`
	Regions         []struct {
		Code    string `json:"code"`
		Allowed bool   `json:"allowed"`
	} `json:"regions"`
	// MaxRegions is how many regions the plan lets a workspace own (and an
	// agent deploy to).
	MaxRegions int `json:"max_regions"`
	MemoryMB   []struct {
		MemoryMB int  `json:"memory_mb"`
		Allowed  bool `json:"allowed"`
	} `json:"memory_mb"`
}

// AllowedRegions are the region codes allowed, in the platform's order: for a
// workspace asked about (GetDeployOptions(id)), the deploy's own verdict for an
// agent in it -- plan, workspace scope, back-fill included.
func (o *DeployOptions) AllowedRegions() []string {
	var out []string
	for _, r := range o.Regions {
		if r.Allowed {
			out = append(out, r.Code)
		}
	}
	return out
}

// AllowedMemory are the memory sizes the plan allows, ascending.
func (o *DeployOptions) AllowedMemory() []int {
	var out []int
	for _, m := range o.MemoryMB {
		if m.Allowed {
			out = append(out, m.MemoryMB)
		}
	}
	return out
}

// GetDeployOptions reads GET /deploy-options, for an agent in workspace
// workspaceID ("" for the plan alone).
func (c *Client) GetDeployOptions(workspaceID string) (*DeployOptions, error) {
	var out DeployOptions
	path := "/deploy-options"
	if workspaceID != "" {
		path += "?workspace_id=" + url.QueryEscape(workspaceID)
	}
	if err := c.Get(path, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
