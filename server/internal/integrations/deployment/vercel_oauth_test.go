package deployment

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestVercelOAuthAuthorizationUsesStateAndS256PKCE(t *testing.T) {
	verifier, challenge, err := NewVercelPKCE()
	if err != nil {
		t.Fatal(err)
	}
	state, err := NewVercelOAuthState()
	if err != nil {
		t.Fatal(err)
	}
	client := NewVercelOAuthClient("client-id", "client-secret", "https://api.example.test/api/integrations/deployment/vercel/callback")
	redirect, err := client.AuthorizationURL(state, challenge)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(redirect)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if query.Get("state") != state || query.Get("code_challenge") != challenge || query.Get("code_challenge_method") != "S256" {
		t.Fatalf("authorization query did not preserve state/PKCE: %v", query)
	}
	if query.Get("scope") != vercelOAuthScopes {
		t.Fatalf("scope = %q, want %q", query.Get("scope"), vercelOAuthScopes)
	}
	if strings.Contains(redirect, "client-secret") || strings.Contains(redirect, verifier) {
		t.Fatal("authorization URL leaked a client secret or PKCE verifier")
	}
	digest := sha256.Sum256([]byte(verifier))
	if want := base64.RawURLEncoding.EncodeToString(digest[:]); want != challenge {
		t.Fatalf("challenge = %q, want %q", challenge, want)
	}
}

func TestVercelOAuthTokenRefreshAndRevoke(t *testing.T) {
	const accessToken = "access-secret"
	const refreshToken = "refresh-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, password, ok := r.BasicAuth(); !ok || user != "client-id" || password != "client-secret" {
			t.Fatalf("oauth client authentication was not basic auth")
		}
		switch r.URL.Path {
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("grant_type") == "authorization_code" && r.Form.Get("code_verifier") == "verifier" {
				writeOAuthTokenResponse(w, accessToken, refreshToken)
				return
			}
			if r.Form.Get("grant_type") == "refresh_token" && r.Form.Get("refresh_token") == refreshToken {
				writeOAuthTokenResponse(w, "rotated-access", "rotated-refresh")
				return
			}
			w.WriteHeader(http.StatusBadRequest)
		case "/revoke":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("token") != "rotated-access" {
				t.Fatalf("revoke token = %q", r.Form.Get("token"))
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewVercelOAuthClient("client-id", "client-secret", "https://api.example.test/callback", server.Client())
	client.Endpoints.Token = server.URL + "/token"
	client.Endpoints.Revoke = server.URL + "/revoke"
	first, err := client.ExchangeCode(context.Background(), "one-time-code", "verifier")
	if err != nil {
		t.Fatal(err)
	}
	if first.AccessToken != accessToken || first.RefreshToken != refreshToken {
		t.Fatalf("exchange token set = %#v", first)
	}
	rotated, err := client.Refresh(context.Background(), first.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.AccessToken != "rotated-access" || rotated.RefreshToken != "rotated-refresh" {
		t.Fatalf("refresh token set = %#v", rotated)
	}
	encoded, err := EncodeVercelOAuthCredentials(rotated)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeVercelOAuthCredentials(encoded)
	if err != nil || decoded.AccessToken != rotated.AccessToken || decoded.RefreshToken != rotated.RefreshToken {
		t.Fatalf("stored token envelope did not round-trip: %#v, %v", decoded, err)
	}
	if err := client.Revoke(context.Background(), rotated.AccessToken); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "client-secret") {
		t.Fatal("stored token envelope leaked the OAuth client secret")
	}
}

func TestVercelOAuthTokenResponseRejectsIncompleteCredentials(t *testing.T) {
	_, err := EncodeVercelOAuthCredentials(VercelOAuthTokenSet{AccessToken: "access", ExpiresAt: time.Now().Add(time.Hour)})
	if err == nil {
		t.Fatal("incomplete token response was accepted")
	}
	if _, err := DecodeVercelOAuthCredentials([]byte(`{"access_token":"access"}`)); err == nil {
		t.Fatal("incomplete stored credentials were accepted")
	}
	if _, err := json.Marshal(VercelOAuthTokenSet{AccessToken: "access", RefreshToken: "refresh"}); err != nil {
		t.Fatal(err)
	}
}

func writeOAuthTokenResponse(w http.ResponseWriter, accessToken, refreshToken string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"access_token":"` + accessToken + `","refresh_token":"` + refreshToken + `","expires_in":3600}`))
}
