// Package claude implements the Claude subscription authorization-code flow.
// Callers own credential storage and synchronization of refresh-token rotation.
package claude

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	DefaultClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	AuthorizeURL    = "https://claude.com/cai/oauth/authorize"
	TokenURL        = "https://platform.claude.com/v1/oauth/token"
	RedirectURI     = "https://platform.claude.com/oauth/code/callback"
	DefaultScope    = "user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"
)

var ErrNotLoggedIn = errors.New("Claude OAuth is not logged in; log in again")

type OAuthConfig struct {
	ClientID   string
	HTTPClient *http.Client
	Now        func() time.Time
}

// Authorization contains ephemeral PKCE secrets. Do not log or persist it.
type Authorization struct {
	URL          string
	State        string
	CodeVerifier string
	ExpiresAt    time.Time
}

type Token struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	AccountID    string    `json:"account_id,omitempty"`
	Scope        string    `json:"scope,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	CreatedAt    time.Time `json:"created_at,omitempty"`
	UpdatedAt    time.Time `json:"updated_at,omitempty"`
}

func (t Token) IsAccessTokenUsable(now time.Time) bool {
	return strings.TrimSpace(t.AccessToken) != "" && now.Add(time.Minute).Before(t.ExpiresAt)
}

func BeginAuthorization(cfg OAuthConfig) (Authorization, error) {
	secrets := make([]byte, 64)
	if _, err := rand.Read(secrets); err != nil {
		return Authorization{}, err
	}
	auth := Authorization{
		State:        base64.RawURLEncoding.EncodeToString(secrets[:32]),
		CodeVerifier: base64.RawURLEncoding.EncodeToString(secrets[32:]),
		ExpiresAt:    cfg.currentTime().Add(15 * time.Minute),
	}
	digest := sha256.Sum256([]byte(auth.CodeVerifier))
	query := url.Values{
		"code": {"true"}, "client_id": {cfg.clientID()}, "response_type": {"code"},
		"redirect_uri": {RedirectURI}, "scope": {DefaultScope}, "state": {auth.State},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(digest[:])}, "code_challenge_method": {"S256"},
	}
	auth.URL = AuthorizeURL + "?" + query.Encode()
	return auth, nil
}

// ExchangeCode accepts code#state or a callback URL containing code and state.
// Bare codes are rejected: they do not prove which login attempt was approved.
func ExchangeCode(ctx context.Context, cfg OAuthConfig, auth Authorization, callback string) (Token, error) {
	if auth.State == "" || auth.CodeVerifier == "" || !cfg.currentTime().Before(auth.ExpiresAt) {
		return Token{}, fmt.Errorf("Claude authorization attempt is invalid or expired")
	}
	callback = strings.TrimSpace(callback)
	code, state, _ := strings.Cut(callback, "#")
	if u, err := url.Parse(callback); err == nil && u.IsAbs() {
		code, state = u.Query().Get("code"), u.Query().Get("state")
	}
	if strings.TrimSpace(code) == "" || subtle.ConstantTimeCompare([]byte(state), []byte(auth.State)) != 1 {
		return Token{}, fmt.Errorf("Claude callback must contain an authorization code and matching state")
	}
	return requestToken(ctx, cfg, map[string]string{
		"grant_type": "authorization_code", "client_id": cfg.clientID(), "code": code,
		"redirect_uri": RedirectURI, "code_verifier": auth.CodeVerifier, "state": auth.State,
	})
}

func RefreshToken(ctx context.Context, cfg OAuthConfig, refreshToken string) (Token, error) {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return Token{}, ErrNotLoggedIn
	}
	token, err := requestToken(ctx, cfg, map[string]string{
		"grant_type": "refresh_token", "client_id": cfg.clientID(), "refresh_token": refreshToken,
	})
	if err == nil && token.RefreshToken == "" {
		token.RefreshToken = refreshToken
	}
	return token, err
}

func requestToken(ctx context.Context, cfg OAuthConfig, body map[string]string) (Token, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return Token{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, TokenURL, bytes.NewReader(data))
	if err != nil {
		return Token{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	client := http.Client{Timeout: 30 * time.Second}
	if cfg.HTTPClient != nil {
		client = *cfg.HTTPClient
	}
	// A redirect must never forward the authorization code or refresh token.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Token{}, ctx.Err()
		}
		return Token{}, fmt.Errorf("Claude OAuth token request failed")
	}
	defer resp.Body.Close()
	const limit = 1 << 20
	data, err = io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || len(data) > limit {
		return Token{}, fmt.Errorf("cannot read Claude OAuth response")
	}
	if resp.StatusCode != http.StatusOK {
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &failure)
		if resp.StatusCode == http.StatusUnauthorized || (resp.StatusCode == http.StatusBadRequest && failure.Error == "invalid_grant") {
			return Token{}, ErrNotLoggedIn
		}
		return Token{}, fmt.Errorf("Claude OAuth token request failed with HTTP %d", resp.StatusCode)
	}
	var response struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		Scope        string `json:"scope"`
		Account      struct {
			UUID string `json:"uuid"`
		} `json:"account"`
	}
	if json.Unmarshal(data, &response) != nil || strings.TrimSpace(response.AccessToken) == "" || response.ExpiresIn <= 0 || response.ExpiresIn > 10*365*24*60*60 {
		return Token{}, fmt.Errorf("invalid Claude OAuth token response")
	}
	now := cfg.currentTime()
	return Token{AccessToken: strings.TrimSpace(response.AccessToken), RefreshToken: strings.TrimSpace(response.RefreshToken),
		AccountID: strings.TrimSpace(response.Account.UUID), Scope: response.Scope,
		ExpiresAt: now.Add(time.Duration(response.ExpiresIn) * time.Second), CreatedAt: now, UpdatedAt: now}, nil
}

func (cfg OAuthConfig) currentTime() time.Time {
	if cfg.Now != nil {
		return cfg.Now().UTC()
	}
	return time.Now().UTC()
}

func (cfg OAuthConfig) clientID() string {
	if id := strings.TrimSpace(cfg.ClientID); id != "" {
		return id
	}
	return DefaultClientID
}
