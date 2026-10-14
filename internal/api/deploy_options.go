package api

// DeployOptions is GET /deploy-options: the regions and memory sizes an agent
// can be given, each with whether the caller's plan allows it. The same source
// the dashboard's configurator offers them from, so `afy init` never carries a
// list of its own.
type DeployOptions struct {
	Regions []struct {
		Code    string `json:"code"`
		Allowed bool   `json:"allowed"`
	} `json:"regions"`
	MaxRegions *int    `json:"max_regions"`
	HomeRegion *string `json:"home_region"`
	MemoryMB   []struct {
		MemoryMB int  `json:"memory_mb"`
		Allowed  bool `json:"allowed"`
	} `json:"memory_mb"`
	MaxMemoryMB *int `json:"max_memory_mb"`
}

// AllowedRegions are the region codes the plan allows, in the platform's order.
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

// GetDeployOptions reads GET /deploy-options.
func (c *Client) GetDeployOptions() (*DeployOptions, error) {
	var out DeployOptions
	if err := c.Get("/deploy-options", &out); err != nil {
		return nil, err
	}
	return &out, nil
}
