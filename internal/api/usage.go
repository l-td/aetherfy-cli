package api

import (
	"encoding/json"
)

// Usage is the account's usage meter: the dashboard's Usage & Spend card, in
// the same words (the control plane's answer to GET /usage is the dashboard's
// translateUsagePreview, so no raw credit unit ever reaches a client).
//
// Only the fields `afy usage` shows are decoded. Raw is the body as the server
// wrote it, and `afy usage --json` prints that unchanged.
type Usage struct {
	HasActivePeriod bool   `json:"hasActivePeriod"`
	Reason          string `json:"reason"`
	Tier            string `json:"tier"`

	PeriodStart  string   `json:"periodStart"`
	PeriodEnd    string   `json:"periodEnd"`
	GaugePercent *float64 `json:"gaugePercent"`
	GaugeLabel   string   `json:"gaugeLabel"`

	InOverage      bool    `json:"inOverage"`
	OverageDollars float64 `json:"overageDollars"`
	OverageLabel   *string `json:"overageLabel"`

	TotalCompositionLabel *string `json:"totalCompositionLabel"`
	CurrentTotalLabel     *string `json:"currentTotalLabel"`

	AwakeBudgetNote string `json:"awakeBudgetNote"`

	Forecast struct {
		Message *string `json:"message"`
	} `json:"forecast"`

	Today struct {
		Label *string `json:"label"`
	} `json:"today"`

	Agents []UsageAgent `json:"agents"`

	Raw json.RawMessage `json:"-"`
}

// UsageAgent is one agent's row of the meter.
type UsageAgent struct {
	AgentID     string       `json:"agentId"`
	AgentName   string       `json:"agentName"`
	State       *string      `json:"state"`
	Display     string       `json:"display"`
	UsagePaused bool         `json:"usagePaused"`
	AwakeTime   []UsageAwake `json:"awakeTime"`
}

// UsageAwake is an agent's awake time in one region against that region's
// budget; Label is the sentence the dashboard shows.
type UsageAwake struct {
	Region  string  `json:"region"`
	Percent float64 `json:"percent"`
	Label   string  `json:"label"`
}

// GetUsage reads the account's usage meter.
func (c *Client) GetUsage() (*Usage, error) {
	raw, err := c.getDocument("/usage")
	if err != nil {
		return nil, err
	}
	u := &Usage{Raw: raw}
	if err := json.Unmarshal(raw, u); err != nil {
		return nil, requestError(err)
	}
	return u, nil
}
