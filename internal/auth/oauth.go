package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// OpenAI Codex OAuth constants.
	codexClientID       = "app_EMoamEEZ73f0CkXaXp7hrann"
	codexIssuer         = "https://auth.openai.com"
	codexAPIEndpoint    = "https://chatgpt.com/backend-api/codex/responses"
	codexPollMarginMs   = 3000
	codexDeviceEndpoint = codexIssuer + "/api/accounts/deviceauth/usercode"
	codexTokenEndpoint  = codexIssuer + "/api/accounts/deviceauth/token"
	codexOAuthToken     = codexIssuer + "/oauth/token"
)

// DeviceCodeResponse is returned by the device authorization endpoint.
type DeviceCodeResponse struct {
	DeviceAuthID string
	UserCode     string
	Interval     int    // poll interval in seconds
	VerifyURL    string // auth.openai.com/codex/device
}

// OAuthTokens is the token response from the OAuth token endpoint.
type OAuthTokens struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

// StartDeviceAuth initiates the OpenAI Codex device authorization flow.
func StartDeviceAuth(ctx context.Context) (*DeviceCodeResponse, error) {
	body := fmt.Sprintf(`{"client_id":%q}`, codexClientID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexDeviceEndpoint, strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("auth: build device code request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth: device code request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("auth: device code request failed (%d): %s", resp.StatusCode, string(respBody))
	}

	var raw struct {
		DeviceAuthID string      `json:"device_auth_id"`
		UserCode     string      `json:"user_code"`
		Interval     json.Number `json:"interval"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("auth: decode device code response: %w", err)
	}

	interval := 5
	if v, err := raw.Interval.Int64(); err == nil && v > 0 {
		interval = int(v)
	}

	return &DeviceCodeResponse{
		DeviceAuthID: raw.DeviceAuthID,
		UserCode:     raw.UserCode,
		Interval:     interval,
		VerifyURL:    codexIssuer + "/codex/device",
	}, nil
}

// PollDeviceAuth polls the token endpoint until the user approves or the
// context is cancelled.
func PollDeviceAuth(ctx context.Context, deviceAuthID, userCode string, intervalSec int) (*OAuthTokens, error) {
	if intervalSec < 1 {
		intervalSec = 5
	}
	pollInterval := time.Duration(intervalSec)*time.Second + time.Duration(codexPollMarginMs)*time.Millisecond

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pollInterval):
		}

		tokens, done, err := pollOnce(ctx, deviceAuthID, userCode)
		if err != nil {
			return nil, err
		}
		if done {
			return tokens, nil
		}
	}
}

func pollOnce(ctx context.Context, deviceAuthID, userCode string) (tokens *OAuthTokens, done bool, err error) {
	body, _ := json.Marshal(map[string]string{
		"device_auth_id": deviceAuthID,
		"user_code":      userCode,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexTokenEndpoint, strings.NewReader(string(body)))
	if err != nil {
		return nil, false, fmt.Errorf("auth: build poll request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("auth: poll request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
		return nil, false, nil
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, false, fmt.Errorf("auth: poll failed (%d): %s", resp.StatusCode, string(respBody))
	}

	var deviceResp struct {
		AuthorizationCode string `json:"authorization_code"`
		CodeVerifier      string `json:"code_verifier"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&deviceResp); err != nil {
		return nil, false, fmt.Errorf("auth: decode poll response: %w", err)
	}

	tok, err := exchangeCodeForTokens(ctx, deviceResp.AuthorizationCode, deviceResp.CodeVerifier)
	if err != nil {
		return nil, false, err
	}
	return tok, true, nil
}

func exchangeCodeForTokens(ctx context.Context, code, codeVerifier string) (*OAuthTokens, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {codexIssuer + "/deviceauth/callback"},
		"client_id":     {codexClientID},
		"code_verifier": {codeVerifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexOAuthToken, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("auth: build token exchange request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth: token exchange request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("auth: token exchange failed (%d): %s", resp.StatusCode, string(respBody))
	}

	var tokens OAuthTokens
	if err := json.NewDecoder(resp.Body).Decode(&tokens); err != nil {
		return nil, fmt.Errorf("auth: decode token response: %w", err)
	}
	return &tokens, nil
}

// RefreshAccessToken uses a refresh token to obtain new access/refresh tokens.
func RefreshAccessToken(ctx context.Context, refreshToken string) (*OAuthTokens, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {codexClientID},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexOAuthToken, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("auth: build refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth: refresh request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("auth: token refresh failed (%d): %s", resp.StatusCode, string(respBody))
	}

	var tokens OAuthTokens
	if err := json.NewDecoder(resp.Body).Decode(&tokens); err != nil {
		return nil, fmt.Errorf("auth: decode refresh response: %w", err)
	}
	return &tokens, nil
}

// CodexAPIEndpoint returns the Codex responses API endpoint.
func CodexAPIEndpoint() string {
	return codexAPIEndpoint
}

// ExtractAccountID extracts the ChatGPT account ID from a JWT token.
func ExtractAccountID(token string) string {
	parts := strings.SplitN(token, ".", 3)
	if len(parts) != 3 {
		return ""
	}
	decoded, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}

	var claims struct {
		AccountID string `json:"chatgpt_account_id"`
		Auth      *struct {
			AccountID string `json:"chatgpt_account_id"`
		} `json:"https://api.openai.com/auth"`
		Orgs []struct {
			ID string `json:"id"`
		} `json:"organizations"`
	}
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return ""
	}
	if claims.AccountID != "" {
		return claims.AccountID
	}
	if claims.Auth != nil && claims.Auth.AccountID != "" {
		return claims.Auth.AccountID
	}
	if len(claims.Orgs) > 0 {
		return claims.Orgs[0].ID
	}
	return ""
}
