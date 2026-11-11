package api

import (
	"encoding/json"
	"net/url"
)

// The control plane's AI Gateway management routes (/api/v1/gateway/*).
// Money is a JSON number of US dollars both ways, the control plane's wire
// form (its api/routes/gateway.py), carried here as float64.

// GatewaySettings is GET /gateway/settings.
func (c *Client) GatewaySettings() (*GatewayAccountSettings, error) {
	var out GatewayAccountSettings
	if err := c.Get("/gateway/settings", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PutGatewaySettings sets (budget non-nil) or removes (nil) the account budget.
func (c *Client) PutGatewaySettings(budget *float64) (*GatewayAccountSettings, error) {
	var out GatewayAccountSettings
	if err := c.Put("/gateway/settings", map[string]*float64{"monthly_budget_usd": budget}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GatewayAgents is GET /gateway/agents.
func (c *Client) GatewayAgents() (*GatewayAgentList, error) {
	var out GatewayAgentList
	if err := c.Get("/gateway/agents", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PutGatewayAgent updates one agent's settings (id or name).
func (c *Client) PutGatewayAgent(agent string, req GatewayAgentUpdate) (*GatewayAgentSettings, error) {
	var out GatewayAgentSettings
	if err := c.Put("/gateway/agents/"+url.PathEscape(agent), req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GatewayUsageQuery is GET /gateway/usage's query string.
type GatewayUsageQuery struct {
	Since   string // RFC 3339; "" = the server's default (30 days)
	Until   string
	GroupBy string // model, agent or day
	Agent   string // id or name; "" = every agent
}

// GatewayUsage is GET /gateway/usage.
func (c *Client) GatewayUsage(q GatewayUsageQuery) (*GatewayUsageReport, error) {
	v := url.Values{}
	for k, val := range map[string]string{"since": q.Since, "until": q.Until, "group_by": q.GroupBy, "agent": q.Agent} {
		if val != "" {
			v.Set(k, val)
		}
	}
	path := "/gateway/usage"
	if enc := v.Encode(); enc != "" {
		path += "?" + enc
	}
	var out GatewayUsageReport
	if err := c.Get(path, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GatewayAgentUpdate is PUT /gateway/agents/{agent}'s body. A field is sent
// only when Set; sent as nil it is JSON null, which CLEARS the setting (no
// budget; every model), while a field not sent is left unchanged.
type GatewayAgentUpdate struct {
	Budget        *float64
	BudgetSet     bool
	AllowedModels []string
	ModelsSet     bool
}

// MarshalJSON sends exactly the fields that were set.
func (u GatewayAgentUpdate) MarshalJSON() ([]byte, error) {
	body := map[string]interface{}{}
	if u.BudgetSet {
		body["monthly_budget_usd"] = u.Budget
	}
	if u.ModelsSet {
		if u.AllowedModels == nil {
			body["allowed_models"] = nil
		} else {
			body["allowed_models"] = u.AllowedModels
		}
	}
	return json.Marshal(body)
}
