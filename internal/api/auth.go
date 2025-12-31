package api

// ValidateAPIKey checks if the API key is valid by making a test request
func (c *Client) ValidateAPIKey() (*UserInfo, error) {
	// Use agents list endpoint to validate - it requires auth
	var agents []Agent
	err := c.Get("/agents", &agents)
	if err != nil {
		return nil, err
	}

	// API key is valid - we don't have a /me endpoint, so return minimal info
	return &UserInfo{
		UserID: "authenticated",
		Email:  "",
		Tier:   "",
	}, nil
}

// GetHealth checks if the API is reachable
func (c *Client) GetHealth() (*HealthResponse, error) {
	var health HealthResponse
	// Health endpoint is at root, not under /api/v1
	resp, err := c.http.R().
		SetResult(&health).
		Get(c.baseURL + "/../health")

	if err != nil {
		return nil, err
	}

	if resp.IsError() {
		return nil, parseAPIError(resp)
	}

	return &health, nil
}
