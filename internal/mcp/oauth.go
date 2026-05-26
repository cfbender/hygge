package mcp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
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
	defaultOAuthCallbackPort = 19876
	defaultOAuthCallbackPath = "/mcp/oauth/callback"
	defaultOAuthRedirectURI  = "http://127.0.0.1:19876/mcp/oauth/callback"
)

// OAuthConfig is optional per-server OAuth configuration from mcp.toml.
type OAuthConfig struct {
	Enabled      bool
	ClientID     string
	ClientSecret string
	Scope        string
	RedirectURI  string
}

// OAuthRefreshRequest is the token-endpoint request used to refresh an MCP
// OAuth access token.
type OAuthRefreshRequest struct {
	TokenURL     string
	ClientID     string
	ClientSecret string
	RefreshToken string
}

// OAuthTokenResponse is the subset of RFC 6749 token responses Hygge needs.
type OAuthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresIn    int    `json:"expires_in,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

// OAuthEndpoints contains the discovered endpoints needed for an MCP OAuth flow.
type OAuthEndpoints struct {
	ResourceEndpoint      string
	Resource              string
	AuthorizationServer   string
	AuthorizationEndpoint string
	TokenEndpoint         string
	RegistrationEndpoint  string
}

type protectedResourceMetadata struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
}

type authorizationServerMetadata struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	RegistrationEndpoint  string `json:"registration_endpoint"`
}

type dynamicClientRegistrationRequest struct {
	RedirectURIs            []string `json:"redirect_uris"`
	ClientName              string   `json:"client_name"`
	ClientURI               string   `json:"client_uri,omitempty"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

// DynamicClientRegistrationResponse is an OAuth dynamic client registration response.
type DynamicClientRegistrationResponse struct {
	ClientID              string `json:"client_id"`
	ClientSecret          string `json:"client_secret,omitempty"`
	ClientIDIssuedAt      int64  `json:"client_id_issued_at,omitempty"`
	ClientSecretExpiresAt int64  `json:"client_secret_expires_at,omitempty"`
}

// RefreshOAuthToken exchanges a refresh token for a fresh MCP access token.
func RefreshOAuthToken(client *http.Client, req OAuthRefreshRequest) (*OAuthTokenResponse, error) {
	if strings.TrimSpace(req.TokenURL) == "" {
		return nil, fmt.Errorf("mcp-auth: oauth token_url is required")
	}
	if strings.TrimSpace(req.RefreshToken) == "" {
		return nil, fmt.Errorf("mcp-auth: oauth refresh_token is required")
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {req.RefreshToken},
	}
	if req.ClientID != "" {
		form.Set("client_id", req.ClientID)
	}
	if req.ClientSecret != "" {
		form.Set("client_secret", req.ClientSecret)
	}

	httpReq, err := http.NewRequestWithContext(context.Background(), http.MethodPost, req.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("mcp-auth: build oauth refresh request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("mcp-auth: oauth refresh request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("mcp-auth: oauth refresh failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var tokens OAuthTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokens); err != nil {
		return nil, fmt.Errorf("mcp-auth: decode oauth refresh response: %w", err)
	}
	if tokens.AccessToken == "" {
		return nil, fmt.Errorf("mcp-auth: oauth refresh response missing access_token")
	}
	return &tokens, nil
}

// OAuthRedirectURI returns configured or default OAuth loopback redirect URI.
func OAuthRedirectURI(configured string) string {
	if strings.TrimSpace(configured) != "" {
		return strings.TrimSpace(configured)
	}
	return defaultOAuthRedirectURI
}

// GenerateOAuthState returns a cryptographically random OAuth state value.
func GenerateOAuthState() (string, error) {
	return randomURLEncoded(32)
}

// GeneratePKCEVerifier returns a PKCE verifier and S256 challenge.
func GeneratePKCEVerifier() (string, string, error) {
	verifier, err := randomURLEncoded(32)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

func randomURLEncoded(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// BuildAuthorizationURL constructs an OAuth authorization URL with PKCE.
func BuildAuthorizationURL(endpoint OAuthEndpoints, clientID, redirectURI, scope, state, codeChallenge string) (string, error) {
	if endpoint.AuthorizationEndpoint == "" {
		return "", fmt.Errorf("mcp-auth: authorization endpoint is required")
	}
	u, err := url.Parse(endpoint.AuthorizationEndpoint)
	if err != nil {
		return "", fmt.Errorf("mcp-auth: parse authorization endpoint: %w", err)
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	q.Set("code_challenge", codeChallenge)
	q.Set("code_challenge_method", "S256")
	if scope != "" {
		q.Set("scope", scope)
	}
	if endpoint.Resource != "" {
		q.Set("resource", endpoint.Resource)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// ExchangeOAuthCode exchanges an authorization code for OAuth tokens.
func ExchangeOAuthCode(ctx context.Context, client *http.Client, endpoint OAuthEndpoints, clientID, clientSecret, redirectURI, codeVerifier, code string) (*OAuthTokenResponse, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"code_verifier": {codeVerifier},
	}
	if clientSecret != "" {
		form.Set("client_secret", clientSecret)
	}
	if endpoint.Resource != "" {
		form.Set("resource", endpoint.Resource)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("mcp-auth: build token exchange request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mcp-auth: token exchange request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("mcp-auth: token exchange failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var tokens OAuthTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokens); err != nil {
		return nil, fmt.Errorf("mcp-auth: decode token exchange response: %w", err)
	}
	if tokens.AccessToken == "" {
		return nil, fmt.Errorf("mcp-auth: token exchange response missing access_token")
	}
	return &tokens, nil
}

// RegisterOAuthClient dynamically registers Hygge as an OAuth client.
func RegisterOAuthClient(ctx context.Context, client *http.Client, registrationEndpoint, redirectURI string, confidential bool) (*DynamicClientRegistrationResponse, error) {
	if registrationEndpoint == "" {
		return nil, fmt.Errorf("mcp-auth: OAuth server does not advertise dynamic client registration")
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	authMethod := "none"
	if confidential {
		authMethod = "client_secret_post"
	}
	body, err := json.Marshal(dynamicClientRegistrationRequest{
		RedirectURIs:            []string{redirectURI},
		ClientName:              "Hygge",
		ClientURI:               "https://github.com/cfbender/hygge",
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
		TokenEndpointAuthMethod: authMethod,
	})
	if err != nil {
		return nil, fmt.Errorf("mcp-auth: encode client registration: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, registrationEndpoint, strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("mcp-auth: build client registration request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mcp-auth: client registration request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("mcp-auth: client registration failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var registered DynamicClientRegistrationResponse
	if err := json.NewDecoder(resp.Body).Decode(&registered); err != nil {
		return nil, fmt.Errorf("mcp-auth: decode client registration: %w", err)
	}
	if registered.ClientID == "" {
		return nil, fmt.Errorf("mcp-auth: client registration response missing client_id")
	}
	return &registered, nil
}

// DiscoverOAuthEndpoints discovers MCP protected-resource and OAuth server metadata.
func DiscoverOAuthEndpoints(ctx context.Context, client *http.Client, serverURL string) (*OAuthEndpoints, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resourceMetaURL, err := discoverResourceMetadataURL(ctx, client, serverURL)
	if err != nil {
		return nil, err
	}
	resource, err := fetchProtectedResourceMetadata(ctx, client, resourceMetaURL)
	if err != nil {
		return nil, err
	}
	if len(resource.AuthorizationServers) == 0 {
		return nil, fmt.Errorf("mcp-auth: protected resource metadata has no authorization_servers")
	}
	var attempts []string
	for _, asURL := range resource.AuthorizationServers {
		asMeta, err := fetchAuthorizationServerMetadata(ctx, client, asURL)
		if err != nil {
			attempts = append(attempts, fmt.Sprintf("%s: %v", asURL, err))
			continue
		}
		return &OAuthEndpoints{
			ResourceEndpoint:      resourceMetaURL,
			Resource:              firstNonEmpty(resource.Resource, serverURL),
			AuthorizationServer:   asURL,
			AuthorizationEndpoint: asMeta.AuthorizationEndpoint,
			TokenEndpoint:         asMeta.TokenEndpoint,
			RegistrationEndpoint:  asMeta.RegistrationEndpoint,
		}, nil
	}
	return nil, fmt.Errorf("mcp-auth: all authorization server metadata discovery attempts failed: %s", strings.Join(attempts, "; "))
}

func discoverResourceMetadataURL(ctx context.Context, client *http.Client, serverURL string) (string, error) {
	if fromChallenge, err := resourceMetadataFromChallenge(ctx, client, serverURL); err == nil && fromChallenge != "" {
		return fromChallenge, nil
	}
	candidates, err := protectedResourceMetadataCandidates(serverURL)
	if err != nil {
		return "", err
	}
	for _, candidate := range candidates {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, candidate, nil)
		if err != nil {
			continue
		}
		req.Header.Set("Accept", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("mcp-auth: could not discover OAuth protected resource metadata for %s", serverURL)
}

func resourceMetadataFromChallenge(ctx context.Context, client *http.Client, serverURL string) (string, error) {
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"hygge","version":"0"}}}`
	for _, method := range []string{http.MethodPost, http.MethodGet} {
		var reader io.Reader
		if method == http.MethodPost {
			reader = strings.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, serverURL, reader)
		if err != nil {
			return "", err
		}
		req.Header.Set("Accept", "application/json, text/event-stream")
		if method == http.MethodPost {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized {
			if v := parseResourceMetadata(resp.Header.Values("WWW-Authenticate")); v != "" {
				return v, nil
			}
		}
	}
	return "", nil
}

func parseResourceMetadata(headers []string) string {
	for _, header := range headers {
		lower := strings.ToLower(header)
		idx := strings.Index(lower, "resource_metadata=")
		if idx < 0 {
			continue
		}
		value := strings.TrimSpace(header[idx+len("resource_metadata="):])
		if after, ok := strings.CutPrefix(value, `"`); ok {
			value = after
			if end := strings.Index(value, `"`); end >= 0 {
				value = value[:end]
			}
		} else if end := strings.Index(value, ","); end >= 0 {
			value = value[:end]
		}
		return strings.TrimSpace(value)
	}
	return ""
}

func protectedResourceMetadataCandidates(raw string) ([]string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("mcp-auth: parse server URL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("mcp-auth: invalid server URL %q", raw)
	}
	origin := u.Scheme + "://" + u.Host
	path := strings.TrimRight(u.EscapedPath(), "/")
	out := []string{origin + "/.well-known/oauth-protected-resource"}
	if path != "" {
		out = append(out, origin+path+"/.well-known/oauth-protected-resource")
	}
	return out, nil
}

func fetchProtectedResourceMetadata(ctx context.Context, client *http.Client, metadataURL string) (*protectedResourceMetadata, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mcp-auth: protected resource metadata request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("mcp-auth: protected resource metadata failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var meta protectedResourceMetadata
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return nil, fmt.Errorf("mcp-auth: decode protected resource metadata: %w", err)
	}
	return &meta, nil
}

func fetchAuthorizationServerMetadata(ctx context.Context, client *http.Client, issuer string) (*authorizationServerMetadata, error) {
	candidates, err := authorizationServerMetadataCandidates(issuer)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, candidate := range candidates {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, candidate, nil)
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("Accept", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			lastErr = fmt.Errorf("%s: status %d: %s", candidate, resp.StatusCode, strings.TrimSpace(string(body)))
			continue
		}
		var meta authorizationServerMetadata
		err = json.NewDecoder(resp.Body).Decode(&meta)
		_ = resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if meta.AuthorizationEndpoint == "" || meta.TokenEndpoint == "" {
			lastErr = fmt.Errorf("%s: missing authorization_endpoint or token_endpoint", candidate)
			continue
		}
		return &meta, nil
	}
	return nil, fmt.Errorf("mcp-auth: discover authorization server metadata for %s: %w", issuer, lastErr)
}

func authorizationServerMetadataCandidates(raw string) ([]string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("mcp-auth: parse authorization server URL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("mcp-auth: invalid authorization server URL %q", raw)
	}
	origin := u.Scheme + "://" + u.Host
	path := strings.TrimRight(u.EscapedPath(), "/")
	out := []string{origin + "/.well-known/oauth-authorization-server"}
	if path != "" {
		out = append(out,
			origin+path+"/.well-known/oauth-authorization-server",
			origin+path+"/.well-known/openid-configuration",
		)
	}
	out = append(out, origin+"/.well-known/openid-configuration")
	return out, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
