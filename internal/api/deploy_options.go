package api

import "net/url"

// DeployOptions is GET /deploy-options: the regions and memory sizes an agent
// can be given, each with whether the caller's plan allows it. The same source
// the dashboard's configurator offers them from, so `afy init` never carries a
// list of its own.
type DeployOptions struct {
	// Runtimes are the runtimes a deploy accepts, in the server's order.
	Runtimes []string `json:"runtimes"`
	Regions  []struct {
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
