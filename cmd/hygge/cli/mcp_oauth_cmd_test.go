package cli

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/cfbender/hygge/internal/mcp"
)

func TestClearMCPOAuthTransientState_PreservesTokensAndClient(t *testing.T) {
	home := hermeticHome(t)
	xdgState := filepath.Join(home, ".local", "state")
	opts := mcp.AuthLoadOptions{HomeDir: home, XDGStateHome: xdgState}
	entry := mcp.AuthEntry{
		Tokens:       &mcp.OAuthTokens{AccessToken: "access", RefreshToken: "refresh", TokenURL: "https://auth.example/token", ExpiresAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC).UnixMilli()},
		ClientInfo:   &mcp.OAuthClientInfo{ClientID: "client"},
		OAuthState:   "state",
		CodeVerifier: "verifier",
	}
	if err := mcp.SetAuth("oauth-srv", entry, opts); err != nil {
		t.Fatalf("SetAuth: %v", err)
	}

	clearMCPOAuthTransientState("oauth-srv", opts)

	store, err := mcp.LoadAuth(opts)
	if err != nil {
		t.Fatalf("LoadAuth: %v", err)
	}
	got, ok := store.GetAuth("oauth-srv")
	if !ok {
		t.Fatal("oauth-srv auth missing")
	}
	if got.OAuthState != "" || got.CodeVerifier != "" {
		t.Fatalf("transient state not cleared: state=%q verifier=%q", got.OAuthState, got.CodeVerifier)
	}
	if got.Tokens == nil || got.Tokens.AccessToken != "access" {
		t.Fatalf("tokens not preserved: %#v", got.Tokens)
	}
	if got.ClientInfo == nil || got.ClientInfo.ClientID != "client" {
		t.Fatalf("client info not preserved: %#v", got.ClientInfo)
	}
}

func TestOAuthCallbackReportsBrowserErrorsToWait(t *testing.T) {
	for name, path := range map[string]string{
		"missing-state": "?code=abc",
		"invalid-state": "?state=wrong&code=abc",
		"missing-code":  "?state=want",
	} {
		t.Run(name, func(t *testing.T) {
			callback, err := startMCPOAuthCallback("http://127.0.0.1:0/mcp/oauth/callback", "want")
			if err != nil {
				t.Fatalf("startMCPOAuthCallback: %v", err)
			}
			defer callback.Close()

			resp, err := http.Get("http://" + callback.server.Addr + "/mcp/oauth/callback" + path)
			if err != nil {
				t.Fatalf("Get callback: %v", err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", resp.StatusCode)
			}
			if _, err := callback.Wait(context.Background()); err == nil {
				t.Fatal("Wait error = nil")
			}
		})
	}
}

func TestClientSecretExpiredUsesUnixMilliseconds(t *testing.T) {
	if clientSecretExpired(&mcp.OAuthClientInfo{ClientSecretExpiresAt: 4_102_444_800_000}) {
		t.Fatal("future millisecond expiry should not be expired")
	}
	if !clientSecretExpired(&mcp.OAuthClientInfo{ClientSecretExpiresAt: 946_684_800_000}) {
		t.Fatal("past millisecond expiry should be expired")
	}
}
