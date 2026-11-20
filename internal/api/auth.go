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
