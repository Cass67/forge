// Package copilot implements GitHub Copilot authentication via the RFC 8628
// OAuth 2.0 Device Authorization Grant flow.
package copilot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const (
	deviceURL = "https://github.com/login/device/code"
	tokenURL  = "https://github.com/login/oauth/access_token"
)

type DeviceCode struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	Error       string `json:"error"`
	Interval    int    `json:"interval"`
}

// RequestDeviceCode initiates the device authorization flow and returns the
// code the user must enter at VerificationURI.
func RequestDeviceCode(ctx context.Context, clientID string) (*DeviceCode, error) {
	body, _ := json.Marshal(map[string]string{
		"client_id": clientID,
		"scope":     "read:user",
	})
	req, err := http.NewRequestWithContext(ctx, "POST", deviceURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device code request failed: %s", resp.Status)
	}

	var dc DeviceCode
	if err := json.NewDecoder(resp.Body).Decode(&dc); err != nil {
		return nil, err
	}
	if dc.Interval == 0 {
		dc.Interval = 5
	}
	return &dc, nil
}

// PollForToken polls GitHub until the user completes authorization or the
// context is cancelled. Returns the GitHub OAuth access token on success.
func PollForToken(ctx context.Context, clientID string, dc *DeviceCode) (string, error) {
	// Add 3s safety margin to avoid hitting the server slightly too early.
	interval := time.Duration(dc.Interval)*time.Second + 3*time.Second

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(interval):
		}

		body, _ := json.Marshal(map[string]string{
			"client_id":   clientID,
			"device_code": dc.DeviceCode,
			"grant_type":  "urn:ietf:params:oauth:grant-type:device_code",
		})
		req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, bytes.NewReader(body))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return "", err
		}
		var tr tokenResponse
		json.NewDecoder(resp.Body).Decode(&tr) //nolint:errcheck
		resp.Body.Close()

		switch {
		case tr.AccessToken != "":
			return tr.AccessToken, nil
		case tr.Error == "authorization_pending":
			// keep waiting
		case tr.Error == "slow_down":
			// RFC 8628 §3.5: add 5 seconds; use server hint if provided
			if tr.Interval > 0 {
				interval = time.Duration(tr.Interval)*time.Second + 3*time.Second
			} else {
				interval += 5 * time.Second
			}
		case tr.Error != "":
			return "", fmt.Errorf("auth error: %s", tr.Error)
		}
	}
}
