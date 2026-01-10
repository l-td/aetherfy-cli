package test

import (
	"testing"

	"github.com/aetherfy/cli/internal/api"
)

// TestUserInfoStructure tests that UserInfo has all required fields
func TestUserInfoStructure(t *testing.T) {
	userInfo := api.UserInfo{
		UserID: "test-user-id",
		Email:  "test@example.com",
		Tier:   "free",
	}

	if userInfo.UserID != "test-user-id" {
		t.Errorf("Expected UserID to be 'test-user-id', got '%s'", userInfo.UserID)
	}

	if userInfo.Email != "test@example.com" {
		t.Errorf("Expected Email to be 'test@example.com', got '%s'", userInfo.Email)
	}

	if userInfo.Tier != "free" {
		t.Errorf("Expected Tier to be 'free', got '%s'", userInfo.Tier)
	}
}

// TestUserInfoEmpty tests that UserInfo can be created empty
func TestUserInfoEmpty(t *testing.T) {
	var userInfo api.UserInfo

	if userInfo.UserID != "" {
		t.Errorf("Expected empty UserID, got '%s'", userInfo.UserID)
	}

	if userInfo.Email != "" {
		t.Errorf("Expected empty Email, got '%s'", userInfo.Email)
	}

	if userInfo.Tier != "" {
		t.Errorf("Expected empty Tier, got '%s'", userInfo.Tier)
	}
}
