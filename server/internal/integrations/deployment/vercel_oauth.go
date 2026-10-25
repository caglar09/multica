package deployment

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	vercelOAuthAuthorizationEndpoint = "https://vercel.com/oauth/authorize"
	vercelOAuthTokenEndpoint         = "https://api.vercel.com/login/oauth/token"
	vercelOAuthRevokeEndpoint        = "https://api.vercel.com/login/oauth/token/revoke"
	vercelOAuthScopes                = "openid email profile offline_access"
)

// VercelOAuthEndpoints is injectable only for deterministic tests. Production
// uses the endpoints advertised by Vercel's OpenID configuration.
type VercelOAuthEndpoints struct {
	Authorization string
	Token         string
	Revoke        string
}

type VercelOAuthClient struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	HTTPClient   *http.Client
	Endpoints    VercelOAuthEndpoints
}

type VercelOAuthTokenSet struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

func NewVercelOAuthClient(clientID, clientSecret, redirectURL string, clients ...*http.Client) *VercelOAuthClient {
	client := http.DefaultClient
	if len(clients) > 0 && clients[0] != nil {
		client = clients[0]
	}
	return &VercelOAuthClient{
		ClientID: clientID, ClientSecret: clientSecret, RedirectURL: redirectURL,
		HTTPClient: client,
		Endpoints: VercelOAuthEndpoints{
			Authorization: vercelOAuthAuthorizationEndpoint,
			Token:         vercelOAuthTokenEndpoint,
			Revoke:        vercelOAuthRevokeEndpoint,
		},
	}
}

func NewVercelPKCE() (verifier, challenge string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", errors.New("generate oauth verifier")
	}
	verifier = base64.RawURLEncoding.EncodeToString(raw)
	digest := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(digest[:])
	return verifier, challenge, nil
}

func NewVercelOAuthState() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", errors.New("generate oauth state")
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (c *VercelOAuthClient) AuthorizationURL(state, challenge string) (string, error) {
	if strings.TrimSpace(c.ClientID) == "" || strings.TrimSpace(c.RedirectURL) == "" || strings.TrimSpace(state) == "" || strings.TrimSpace(challenge) == "" {
		return "", errors.New("vercel oauth is not configured")
	}
	if !isAllowedOAuthRedirect(c.RedirectURL) {
		return "", errors.New("vercel oauth redirect is invalid")
	}
	endpoint := strings.TrimSpace(c.Endpoints.Authorization)
	if endpoint == "" {
		return "", errors.New("vercel oauth authorization endpoint is not configured")
	}
	values := url.Values{
		"client_id":             {c.ClientID},
		"redirect_uri":          {c.RedirectURL},
		"response_type":         {"code"},
		"scope":                 {vercelOAuthScopes},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	return endpoint + "?" + values.Encode(), nil
}

func (c *VercelOAuthClient) ExchangeCode(ctx context.Context, code, verifier string) (VercelOAuthTokenSet, error) {
	if strings.TrimSpace(code) == "" || strings.TrimSpace(verifier) == "" {
		return VercelOAuthTokenSet{}, errors.New("vercel oauth callback is invalid")
	}
	return c.tokenRequest(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {c.RedirectURL},
		"code_verifier": {verifier},
	})
}

func (c *VercelOAuthClient) Refresh(ctx context.Context, refreshToken string) (VercelOAuthTokenSet, error) {
	if strings.TrimSpace(refreshToken) == "" {
		return VercelOAuthTokenSet{}, errors.New("vercel oauth refresh token is missing")
	}
	return c.tokenRequest(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	})
}

func (c *VercelOAuthClient) Revoke(ctx context.Context, token string) error {
	if strings.TrimSpace(token) == "" {
		return errors.New("vercel oauth token is missing")
	}
	if strings.TrimSpace(c.ClientID) == "" || strings.TrimSpace(c.ClientSecret) == "" {
		return errors.New("vercel oauth is not configured")
	}
	form := url.Values{"token": {token}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoints.Revoke, strings.NewReader(form.Encode()))
	if err != nil {
		return errors.New("create vercel oauth revoke request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(c.ClientID, c.ClientSecret)
	response, err := c.httpClient().Do(req)
	if err != nil {
		return errors.New("vercel oauth revoke request failed")
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return errors.New("vercel oauth revoke request failed")
	}
	return nil
}

func EncodeVercelOAuthCredentials(tokens VercelOAuthTokenSet) ([]byte, error) {
	if strings.TrimSpace(tokens.AccessToken) == "" || strings.TrimSpace(tokens.RefreshToken) == "" || tokens.ExpiresAt.IsZero() {
		return nil, errors.New("vercel oauth token response is incomplete")
	}
	return json.Marshal(tokens)
}

func DecodeVercelOAuthCredentials(raw []byte) (VercelOAuthTokenSet, error) {
	var tokens VercelOAuthTokenSet
	if err := json.Unmarshal(raw, &tokens); err != nil || strings.TrimSpace(tokens.AccessToken) == "" || strings.TrimSpace(tokens.RefreshToken) == "" || tokens.ExpiresAt.IsZero() {
		return VercelOAuthTokenSet{}, errors.New("stored vercel oauth credentials are invalid")
	}
	return tokens, nil
}

func (t VercelOAuthTokenSet) NeedsRefresh(now time.Time) bool {
	return !t.ExpiresAt.After(now.Add(time.Minute))
}

func (c *VercelOAuthClient) tokenRequest(ctx context.Context, form url.Values) (VercelOAuthTokenSet, error) {
	if strings.TrimSpace(c.ClientID) == "" || strings.TrimSpace(c.ClientSecret) == "" || !isAllowedOAuthRedirect(c.RedirectURL) {
		return VercelOAuthTokenSet{}, errors.New("vercel oauth is not configured")
	}
	if strings.TrimSpace(c.Endpoints.Token) == "" {
		return VercelOAuthTokenSet{}, errors.New("vercel oauth token endpoint is not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoints.Token, bytes.NewBufferString(form.Encode()))
	if err != nil {
		return VercelOAuthTokenSet{}, errors.New("create vercel oauth token request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(c.ClientID, c.ClientSecret)
	response, err := c.httpClient().Do(req)
	if err != nil {
		return VercelOAuthTokenSet{}, errors.New("vercel oauth token request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return VercelOAuthTokenSet{}, errors.New("vercel oauth token request failed")
	}
	var responseBody struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&responseBody); err != nil {
		return VercelOAuthTokenSet{}, errors.New("vercel oauth token response is invalid")
	}
	if responseBody.ExpiresIn <= 0 {
		return VercelOAuthTokenSet{}, errors.New("vercel oauth token response is missing expiry")
	}
	return VercelOAuthTokenSet{
		AccessToken: responseBody.AccessToken, RefreshToken: responseBody.RefreshToken,
		ExpiresAt: time.Now().Add(time.Duration(responseBody.ExpiresIn) * time.Second),
	}, nil
}

func (c *VercelOAuthClient) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

func isAllowedOAuthRedirect(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	return parsed.Scheme == "https" || (parsed.Scheme == "http" && parsed.Hostname() == "localhost")
}
