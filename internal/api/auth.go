package api

// ValidateAPIKey checks if the API key is valid by making a test request
func (c *Client) ValidateAPIKey() (*UserInfo, error) {
	// Use the /auth/me endpoint to validate and get user info
	var userInfo UserInfo
	err := c.Get("/auth/me", &userInfo)
	if err != nil {
		return nil, err
	}

	return &userInfo, nil
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
