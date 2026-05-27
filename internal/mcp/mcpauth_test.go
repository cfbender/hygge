package mcp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func hermeticMCPAuth(t *testing.T) (string, AuthLoadOptions) {
	t.Helper()
	home := t.TempDir()
	xdgState := filepath.Join(home, ".local", "state")
	return home, AuthLoadOptions{HomeDir: home, XDGStateHome: xdgState}
}

// ---------------------------------------------------------------------------
// Path
// ---------------------------------------------------------------------------

func TestAuthPath_UsesXDGStateHome(t *testing.T) {
	home := t.TempDir()
	xdgState := filepath.Join(home, "custom-state")
	opts := AuthLoadOptions{HomeDir: home, XDGStateHome: xdgState}
	p, err := AuthPath(opts)
	if err != nil {
		t.Fatalf("AuthPath: %v", err)
	}
	want := filepath.Join(xdgState, "hygge", "mcp-auth.json")
	if p != want {
		t.Errorf("got %q, want %q", p, want)
	}
}

// ---------------------------------------------------------------------------
// Load — missing file is not an error
// ---------------------------------------------------------------------------

func TestLoadAuth_MissingFile(t *testing.T) {
	_, opts := hermeticMCPAuth(t)
	s, err := LoadAuth(opts)
	if err != nil {
		t.Fatalf("LoadAuth: %v", err)
	}
	if s == nil {
		t.Fatal("store is nil")
	}
	if len(s.Servers) != 0 {
		t.Errorf("want empty Servers, got %v", s.Servers)
	}
}

func TestLoadAuth_EmptyFileIsEmptyStore(t *testing.T) {
	_, opts := hermeticMCPAuth(t)
	p, err := AuthPath(opts)
	if err != nil {
		t.Fatalf("AuthPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	s, err := LoadAuth(opts)
	if err != nil {
		t.Fatalf("LoadAuth: %v", err)
	}
	if len(s.Servers) != 0 {
		t.Fatalf("got %d servers, want empty store", len(s.Servers))
	}
}

// ---------------------------------------------------------------------------
// SetAuth + LoadAuth round-trip
// ---------------------------------------------------------------------------

func TestSetAuth_RoundTrip(t *testing.T) {
	_, opts := hermeticMCPAuth(t)

	entry := AuthEntry{
		Headers: map[string]string{
			"Authorization": "Bearer tok-secret",
			"X-Custom":      "abc",
		},
		AddedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	if err := SetAuth("my-server", entry, opts); err != nil {
		t.Fatalf("SetAuth: %v", err)
	}

	s, err := LoadAuth(opts)
	if err != nil {
		t.Fatalf("LoadAuth: %v", err)
	}
	got, ok := s.GetAuth("my-server")
	if !ok {
		t.Fatal("server not found after set")
	}
	if got.Headers["Authorization"] != "Bearer tok-secret" {
		t.Errorf("Authorization: got %q", got.Headers["Authorization"])
	}
	if got.Headers["X-Custom"] != "abc" {
		t.Errorf("X-Custom: got %q", got.Headers["X-Custom"])
	}
}

// ---------------------------------------------------------------------------
// SetAuth fills AddedAt when zero
// ---------------------------------------------------------------------------

func TestSetAuth_FillsAddedAt(t *testing.T) {
	_, opts := hermeticMCPAuth(t)
	before := time.Now()
	if err := SetAuth("srv", AuthEntry{Headers: map[string]string{"k": "v"}}, opts); err != nil {
		t.Fatalf("SetAuth: %v", err)
	}
	s, _ := LoadAuth(opts)
	got, _ := s.GetAuth("srv")
	if got.AddedAt.IsZero() {
		t.Error("AddedAt should be filled, got zero")
	}
	if got.AddedAt.Before(before) {
		t.Errorf("AddedAt %v is before test started %v", got.AddedAt, before)
	}
}

// ---------------------------------------------------------------------------
// Multiple servers coexist
// ---------------------------------------------------------------------------

func TestSetAuth_MultipleServers(t *testing.T) {
	_, opts := hermeticMCPAuth(t)

	for _, name := range []string{"alpha", "beta", "gamma"} {
		if err := SetAuth(name, AuthEntry{Headers: map[string]string{"key": name + "-tok"}}, opts); err != nil {
			t.Fatalf("SetAuth(%s): %v", name, err)
		}
	}

	s, err := LoadAuth(opts)
	if err != nil {
		t.Fatalf("LoadAuth: %v", err)
	}
	for _, name := range []string{"alpha", "beta", "gamma"} {
		e, ok := s.GetAuth(name)
		if !ok {
			t.Errorf("server %q not found", name)
			continue
		}
		if e.Headers["key"] != name+"-tok" {
			t.Errorf("server %q: got %q", name, e.Headers["key"])
		}
	}
}

// ---------------------------------------------------------------------------
// RemoveAuth
// ---------------------------------------------------------------------------

func TestRemoveAuth_Removes(t *testing.T) {
	_, opts := hermeticMCPAuth(t)
	if err := SetAuth("to-remove", AuthEntry{Headers: map[string]string{"k": "v"}}, opts); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := RemoveAuth("to-remove", opts); err != nil {
		t.Fatalf("remove: %v", err)
	}
	s, _ := LoadAuth(opts)
	if _, ok := s.GetAuth("to-remove"); ok {
		t.Error("server should be removed")
	}
}

func TestRemoveAuth_Idempotent(t *testing.T) {
	_, opts := hermeticMCPAuth(t)
	// remove a server that was never added
	if err := RemoveAuth("ghost", opts); err != nil {
		t.Errorf("RemoveAuth on absent server should be a no-op: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Empty server name → error
// ---------------------------------------------------------------------------

func TestSetAuth_EmptyName(t *testing.T) {
	_, opts := hermeticMCPAuth(t)
	if err := SetAuth("", AuthEntry{}, opts); err == nil {
		t.Error("expected error for empty server name")
	}
}

// ---------------------------------------------------------------------------
// File has mode 0600
// ---------------------------------------------------------------------------

func TestMCPAuthFile_Mode(t *testing.T) {
	_, opts := hermeticMCPAuth(t)
	if err := SetAuth("srv", AuthEntry{Headers: map[string]string{"k": "v"}}, opts); err != nil {
		t.Fatalf("set: %v", err)
	}
	p, _ := AuthPath(opts)
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode: got %o, want 0600", info.Mode().Perm())
	}
}

// ---------------------------------------------------------------------------
// GetAuth on nil store is safe
// ---------------------------------------------------------------------------

func TestGetAuth_NilSafe(t *testing.T) {
	var s *AuthStore
	if _, ok := s.GetAuth("any"); ok {
		t.Error("nil store should return not-found")
	}
}

func TestSetAuth_OAuthRoundTrip(t *testing.T) {
	_, opts := hermeticMCPAuth(t)
	expires := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC).UnixMilli()
	entry := AuthEntry{
		OAuth: &OAuthCredential{
			AccessToken:  "access-token",
			RefreshToken: "refresh-token",
			TokenURL:     "https://auth.example.com/token",
			ClientID:     "client-123",
			ExpiresAt:    expires,
		},
	}
	if err := SetAuth("test-server", entry, opts); err != nil {
		t.Fatalf("SetAuth: %v", err)
	}
	store, err := LoadAuth(opts)
	if err != nil {
		t.Fatalf("LoadAuth: %v", err)
	}
	got, ok := store.GetAuth("test-server")
	if !ok {
		t.Fatal("test-server auth not found")
	}
	if got.OAuth == nil {
		t.Fatal("OAuth credential missing")
	}
	if got.OAuth.AccessToken != "access-token" || got.OAuth.RefreshToken != "refresh-token" || got.OAuth.TokenURL != "https://auth.example.com/token" || got.OAuth.ClientID != "client-123" {
		t.Fatalf("OAuth credential mismatch: %#v", got.OAuth)
	}
	if got.OAuth.ExpiresAt != expires {
		t.Fatalf("ExpiresAt = %v, want %v", got.OAuth.ExpiresAt, expires)
	}
}

func TestAuthEntryHeadersWithOAuth(t *testing.T) {
	entry := AuthEntry{OAuth: &OAuthCredential{AccessToken: "tok"}}
	got := entry.HeadersWithOAuth()
	if got["Authorization"] != "Bearer tok" {
		t.Fatalf("Authorization = %q", got["Authorization"])
	}

	entry = AuthEntry{
		Headers: map[string]string{"Authorization": "Bearer explicit"},
		OAuth:   &OAuthCredential{AccessToken: "tok"},
	}
	got = entry.HeadersWithOAuth()
	if got["Authorization"] != "Bearer explicit" {
		t.Fatalf("explicit Authorization overwritten: %q", got["Authorization"])
	}

	entry = AuthEntry{
		Headers: map[string]string{"authorization": "Bearer lower"},
		Tokens:  &OAuthTokens{AccessToken: "tok"},
	}
	got = entry.HeadersWithOAuth()
	if got["authorization"] != "Bearer lower" {
		t.Fatalf("lower-case authorization header overwritten: %#v", got)
	}
	if _, ok := got["Authorization"]; ok {
		t.Fatalf("unexpected injected canonical Authorization header: %#v", got)
	}
}

func TestRefreshOAuth_RefreshesExpiredToken(t *testing.T) {
	var gotRefreshToken string
	errCh := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			errCh <- fmt.Errorf("ParseForm: %w", err)
			return
		}
		gotRefreshToken = r.Form.Get("refresh_token")
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("client_id") != "client-123" {
			errCh <- fmt.Errorf("bad form: %v", r.Form)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"fresh-access","refresh_token":"fresh-refresh","expires_in":3600}`))
		errCh <- nil
	}))
	defer server.Close()

	now := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	entry := AuthEntry{OAuth: &OAuthCredential{
		AccessToken:  "old-access",
		RefreshToken: "old-refresh",
		TokenURL:     server.URL,
		ClientID:     "client-123",
		ExpiresAt:    now.Add(-time.Minute).UnixMilli(),
	}}
	changed, err := entry.RefreshOAuth(server.Client(), now)
	if err != nil {
		t.Fatalf("RefreshOAuth: %v", err)
	}
	if handlerErr := <-errCh; handlerErr != nil {
		t.Fatalf("handler validation: %v", handlerErr)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}
	if gotRefreshToken != "old-refresh" {
		t.Fatalf("refresh token sent = %q", gotRefreshToken)
	}
	if entry.OAuth.AccessToken != "fresh-access" || entry.OAuth.RefreshToken != "fresh-refresh" {
		t.Fatalf("entry not refreshed: %#v", entry.OAuth)
	}
	wantExpiry := now.Add(time.Hour).UnixMilli()
	if entry.OAuth.ExpiresAt != wantExpiry {
		t.Fatalf("ExpiresAt = %v, want %v", entry.OAuth.ExpiresAt, wantExpiry)
	}
}

func TestOAuthDiscoveryFromChallengeAndMetadata(t *testing.T) {
	t.Parallel()

	var baseURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mcp":
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+baseURL+`/.well-known/oauth-protected-resource/mcp"`)
			w.WriteHeader(http.StatusUnauthorized)
		case "/.well-known/oauth-protected-resource/mcp":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"resource":"` + baseURL + `/mcp","authorization_servers":["` + baseURL + `/as"]}`))
		case "/as/.well-known/oauth-authorization-server":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"issuer":"` + baseURL + `/as","authorization_endpoint":"` + baseURL + `/authorize","token_endpoint":"` + baseURL + `/token","registration_endpoint":"` + baseURL + `/register"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	baseURL = srv.URL

	endpoints, err := DiscoverOAuthEndpoints(context.Background(), srv.Client(), srv.URL+"/mcp")
	if err != nil {
		t.Fatalf("DiscoverOAuthEndpoints: %v", err)
	}
	if endpoints.AuthorizationEndpoint != srv.URL+"/authorize" || endpoints.TokenEndpoint != srv.URL+"/token" || endpoints.RegistrationEndpoint != srv.URL+"/register" {
		t.Fatalf("bad endpoints: %#v", endpoints)
	}
	if endpoints.Resource != srv.URL+"/mcp" {
		t.Fatalf("resource = %q", endpoints.Resource)
	}
}

func TestBuildAuthorizationURLIncludesPKCEAndResource(t *testing.T) {
	t.Parallel()
	got, err := BuildAuthorizationURL(OAuthEndpoints{AuthorizationEndpoint: "https://auth.example/authorize", Resource: "https://mcp.example/mcp"}, "client", "http://127.0.0.1:19876/mcp/oauth/callback", "tools", "state", "challenge")
	if err != nil {
		t.Fatalf("BuildAuthorizationURL: %v", err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	q := u.Query()
	for key, want := range map[string]string{
		"response_type":         "code",
		"client_id":             "client",
		"redirect_uri":          "http://127.0.0.1:19876/mcp/oauth/callback",
		"scope":                 "tools",
		"state":                 "state",
		"code_challenge":        "challenge",
		"code_challenge_method": "S256",
		"resource":              "https://mcp.example/mcp",
	} {
		if got := q.Get(key); got != want {
			t.Fatalf("%s = %q, want %q in %s", key, got, want, u.String())
		}
	}
}

func TestBuildAuthorizationURLRequiresOAuthFields(t *testing.T) {
	t.Parallel()
	endpoint := OAuthEndpoints{AuthorizationEndpoint: "https://auth.example/authorize"}
	cases := []struct {
		name          string
		clientID      string
		redirectURI   string
		state         string
		codeChallenge string
		want          string
	}{
		{name: "client", redirectURI: "http://127.0.0.1/callback", state: "state", codeChallenge: "challenge", want: "client_id is required"},
		{name: "redirect", clientID: "client", state: "state", codeChallenge: "challenge", want: "redirect_uri is required"},
		{name: "state", clientID: "client", redirectURI: "http://127.0.0.1/callback", codeChallenge: "challenge", want: "state is required"},
		{name: "challenge", clientID: "client", redirectURI: "http://127.0.0.1/callback", state: "state", want: "code_challenge is required"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := BuildAuthorizationURL(endpoint, tt.clientID, tt.redirectURI, "", tt.state, tt.codeChallenge)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestRefreshOAuth_ClearsStaleTokenExpiryWhenOmitted(t *testing.T) {
	errCh := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			errCh <- fmt.Errorf("ParseForm: %w", err)
			return
		}
		if r.Form.Get("refresh_token") != "old-refresh" {
			errCh <- fmt.Errorf("refresh token = %q", r.Form.Get("refresh_token"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"fresh-access","refresh_token":"fresh-refresh"}`))
		errCh <- nil
	}))
	defer server.Close()

	now := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	entry := AuthEntry{Tokens: &OAuthTokens{
		AccessToken:  "old-access",
		RefreshToken: "old-refresh",
		TokenURL:     server.URL,
		ExpiresAt:    now.Add(-time.Minute).UnixMilli(),
	}}
	changed, err := entry.RefreshOAuth(server.Client(), now)
	if err != nil {
		t.Fatalf("RefreshOAuth: %v", err)
	}
	if handlerErr := <-errCh; handlerErr != nil {
		t.Fatalf("handler validation: %v", handlerErr)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}
	if entry.Tokens.ExpiresAt != 0 {
		t.Fatalf("ExpiresAt = %v, want zero when expires_in omitted", entry.Tokens.ExpiresAt)
	}
}

func TestAuthorizationServerMetadataCandidates_PathScoped(t *testing.T) {
	got, err := authorizationServerMetadataCandidates("https://example.com/mcp")
	if err != nil {
		t.Fatalf("authorizationServerMetadataCandidates: %v", err)
	}
	want := []string{
		"https://example.com/.well-known/oauth-authorization-server",
		"https://example.com/mcp/.well-known/oauth-authorization-server",
		"https://example.com/mcp/.well-known/openid-configuration",
		"https://example.com/.well-known/openid-configuration",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d candidates %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("candidate[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestDiscoverOAuthEndpointsRejectsIssuerMismatch(t *testing.T) {
	t.Parallel()

	var baseURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"resource":"` + baseURL + `/mcp","authorization_servers":["` + baseURL + `/as"]}`))
		case "/as/.well-known/oauth-authorization-server":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"issuer":"` + baseURL + `/other","authorization_endpoint":"` + baseURL + `/authorize","token_endpoint":"` + baseURL + `/token"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	baseURL = srv.URL

	_, err := DiscoverOAuthEndpoints(context.Background(), srv.Client(), srv.URL+"/mcp")
	if err == nil {
		t.Fatal("expected issuer mismatch error")
	}
	if !strings.Contains(err.Error(), "meta.Issuer") || !strings.Contains(err.Error(), baseURL+"/as") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDiscoverOAuthEndpoints_TriesNextAuthorizationServer(t *testing.T) {
	t.Parallel()

	var baseURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"resource":"` + baseURL + `/mcp","authorization_servers":["` + baseURL + `/broken","` + baseURL + `/working"]}`))
		case "/broken/.well-known/oauth-authorization-server":
			w.WriteHeader(http.StatusInternalServerError)
		case "/working/.well-known/oauth-authorization-server":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"issuer":"` + baseURL + `/working","authorization_endpoint":"` + baseURL + `/authorize","token_endpoint":"` + baseURL + `/token"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	baseURL = srv.URL

	endpoints, err := DiscoverOAuthEndpoints(context.Background(), srv.Client(), srv.URL+"/mcp")
	if err != nil {
		t.Fatalf("DiscoverOAuthEndpoints: %v", err)
	}
	if endpoints.AuthorizationServer != srv.URL+"/working" || endpoints.AuthorizationEndpoint != srv.URL+"/authorize" {
		t.Fatalf("bad endpoints: %#v", endpoints)
	}
}
