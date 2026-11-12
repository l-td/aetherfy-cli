package api

import "fmt"

// The device sign-in (RFC 8628 in shape): a CLI that holds no key asks for a
// code, a person approves the code in the signed-in dashboard, and the CLI
// collects the new key once. Both calls are made WITHOUT credentials
// (NewAnonymousClient): a key can never mint a key.

// DeviceAuthorization is the answer of POST /auth/device.
type DeviceAuthorization struct {
	// DeviceCode is the opaque secret the CLI polls with. It is never shown.
	DeviceCode string `json:"device_code"`
	// UserCode is what the person reads in the dashboard and compares.
	UserCode string `json:"user_code"`
	// VerificationURI is the page to open; VerificationURIComplete is the same
	// page with the code already filled in.
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	// ExpiresIn is how long the code lives, in seconds. Interval is how often
	// to ask whether it was approved, in seconds.
	ExpiresIn int `json:"expires_in"`
	Interval  int `json:"interval"`
}

// DeviceStatus is the status of a poll that is not an error.
const (
	DeviceStatusPending  = "pending"
	DeviceStatusApproved = "approved"
)

// DeviceToken is the answer of POST /auth/device/token that is not an error:
// pending (202) until the code is approved, then approved (200) exactly once,
// with the new key's plaintext.
type DeviceToken struct {
	Status   string  `json:"status"`
	Interval int     `json:"interval"`
	APIKey   string  `json:"api_key"`
	Key      *APIKey `json:"key"`
}

type deviceRequest struct {
	DeviceName string `json:"device_name"`
	KeyName    string `json:"key_name,omitempty"`
}

// StartDevice asks for a code. keyName is optional.
func (c *Client) StartDevice(deviceName, keyName string) (*DeviceAuthorization, error) {
	var auth DeviceAuthorization
	if err := c.Post("/auth/device", deviceRequest{DeviceName: deviceName, KeyName: keyName}, &auth); err != nil {
		return nil, err
	}
	return &auth, nil
}

// PollDevice asks whether the code was approved. A 202 is the answer
// "pending", not an error; every refusal is an *APIError with the server's code.
func (c *Client) PollDevice(deviceCode string) (*DeviceToken, error) {
	var tok DeviceToken
	if err := c.Post("/auth/device/token", map[string]string{"device_code": deviceCode}, &tok); err != nil {
		return nil, err
	}
	switch tok.Status {
	case DeviceStatusPending:
	case DeviceStatusApproved:
		if tok.APIKey == "" {
			return nil, &ResponseInvalidError{Message: "The control plane approved the sign-in but sent no key."}
		}
	default:
		return nil, &ResponseInvalidError{Message: fmt.Sprintf("The control plane answered a sign-in poll with the status %q, which this version of afy does not know. Update afy: run 'afy upgrade'.", tok.Status)}
	}
	return &tok, nil
}
