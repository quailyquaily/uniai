package xai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultIssuer   = "https://auth.x.ai"
	DefaultAPIBase  = "https://api.x.ai/v1"
	DefaultClientID = "b1a00492-073a-47ea-816f-4c329264a828"
	DefaultScope    = "openid profile offline_access grok-cli:access api:access"

	deviceCodeGrantType = "urn:ietf:params:oauth:grant-type:device_code"
	defaultPollInterval = 5 * time.Second
)

var (
	ErrAuthorizationPending  = errors.New("xAI device authorization pending")
	ErrSlowDown              = errors.New("xAI device authorization polling must slow down")
	ErrAccessDenied          = errors.New("xAI device authorization denied")
	ErrDeviceCodeExpired     = errors.New("xAI device authorization expired")
	ErrNotLoggedIn           = errors.New("xAI OAuth is not logged in")
	ErrRevocationUnsupported = errors.New("xAI OAuth revocation endpoint is unavailable")
)

type OAuthConfig struct {
	ClientID   string
	Scope      string
	HTTPClient *http.Client
	Now        func() time.Time
}

type DeviceCode struct {
	VerificationURL         string
	VerificationURLComplete string
	UserCode                string
	Interval                time.Duration
	ExpiresAt               time.Time

	deviceCode    string
	tokenEndpoint string
}

type Token struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type,omitempty"`
	Scope        string    `json:"scope,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
	CreatedAt    time.Time `json:"created_at,omitempty"`
	UpdatedAt    time.Time `json:"updated_at,omitempty"`
}

type discoveryDocument struct {
	Issuer                      string `json:"issuer"`
	DeviceAuthorizationEndpoint string `json:"device_authorization_endpoint"`
	TokenEndpoint               string `json:"token_endpoint"`
	RevocationEndpoint          string `json:"revocation_endpoint"`
}

type deviceCodeResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               any    `json:"expires_in"`
	Interval                any    `json:"interval"`
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	ExpiresIn    any    `json:"expires_in"`
}

func RequestDeviceCode(ctx context.Context, cfg OAuthConfig) (DeviceCode, error) {
	cfg = normalizeOAuthConfig(cfg)
	discovery, err := discover(ctx, cfg)
	if err != nil {
		return DeviceCode{}, err
	}
	form := url.Values{}
	form.Set("client_id", cfg.ClientID)
	form.Set("scope", cfg.Scope)
	var response deviceCodeResponse
	if err := postForm(ctx, cfg.HTTPClient, discovery.DeviceAuthorizationEndpoint, form, &response); err != nil {
		return DeviceCode{}, fmt.Errorf("request xAI device code: %w", err)
	}
	deviceCode := strings.TrimSpace(response.DeviceCode)
	userCode := strings.TrimSpace(response.UserCode)
	verificationURL := strings.TrimSpace(response.VerificationURI)
	if deviceCode == "" || userCode == "" || verificationURL == "" {
		return DeviceCode{}, fmt.Errorf("xAI device code response is missing required fields")
	}
	if err := validateHTTPSURL("verification_uri", verificationURL, "accounts.x.ai", "auth.x.ai"); err != nil {
		return DeviceCode{}, err
	}
	verificationURLComplete := strings.TrimSpace(response.VerificationURIComplete)
	if verificationURLComplete != "" {
		if err := validateHTTPSURL("verification_uri_complete", verificationURLComplete, "accounts.x.ai", "auth.x.ai"); err != nil {
			return DeviceCode{}, err
		}
	}
	expiresIn := durationSeconds(response.ExpiresIn, 0)
	if expiresIn <= 0 {
		return DeviceCode{}, fmt.Errorf("xAI device code response has invalid expires_in")
	}
	return DeviceCode{
		VerificationURL:         verificationURL,
		VerificationURLComplete: verificationURLComplete,
		UserCode:                userCode,
		Interval:                durationSeconds(response.Interval, defaultPollInterval),
		ExpiresAt:               cfg.now().Add(expiresIn),
		deviceCode:              deviceCode,
		tokenEndpoint:           discovery.TokenEndpoint,
	}, nil
}

// PollDeviceCode performs one token-endpoint poll. The caller controls polling
// and applies ErrSlowDown by increasing its own interval.
func PollDeviceCode(ctx context.Context, cfg OAuthConfig, code DeviceCode) (Token, error) {
	cfg = normalizeOAuthConfig(cfg)
	if strings.TrimSpace(code.deviceCode) == "" || strings.TrimSpace(code.tokenEndpoint) == "" {
		return Token{}, fmt.Errorf("xAI device authorization session is invalid")
	}
	if !code.ExpiresAt.IsZero() && !code.ExpiresAt.After(cfg.now()) {
		return Token{}, ErrDeviceCodeExpired
	}
	if err := validateOAuthEndpoint("token_endpoint", code.tokenEndpoint); err != nil {
		return Token{}, err
	}
	form := url.Values{}
	form.Set("grant_type", deviceCodeGrantType)
	form.Set("client_id", cfg.ClientID)
	form.Set("device_code", code.deviceCode)
	var response tokenResponse
	if err := postForm(ctx, cfg.HTTPClient, code.tokenEndpoint, form, &response); err != nil {
		switch oauthErrorCode(err) {
		case "authorization_pending":
			return Token{}, ErrAuthorizationPending
		case "slow_down":
			return Token{}, ErrSlowDown
		case "access_denied":
			return Token{}, ErrAccessDenied
		case "expired_token":
			return Token{}, ErrDeviceCodeExpired
		default:
			return Token{}, fmt.Errorf("poll xAI device authorization: %w", err)
		}
	}
	return tokenFromResponse(response, cfg.now(), true)
}

func RefreshToken(ctx context.Context, cfg OAuthConfig, refreshToken string) (Token, error) {
	cfg = normalizeOAuthConfig(cfg)
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return Token{}, ErrNotLoggedIn
	}
	discovery, err := discover(ctx, cfg)
	if err != nil {
		return Token{}, err
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", cfg.ClientID)
	form.Set("refresh_token", refreshToken)
	var response tokenResponse
	if err := postForm(ctx, cfg.HTTPClient, discovery.TokenEndpoint, form, &response); err != nil {
		if oauthErrorCode(err) == "invalid_grant" {
			return Token{}, ErrNotLoggedIn
		}
		return Token{}, fmt.Errorf("refresh xAI OAuth token: %w", err)
	}
	token, err := tokenFromResponse(response, cfg.now(), false)
	if err != nil {
		return Token{}, err
	}
	if token.RefreshToken == "" {
		token.RefreshToken = refreshToken
	}
	return token, nil
}

func RevokeToken(ctx context.Context, cfg OAuthConfig, token Token) error {
	cfg = normalizeOAuthConfig(cfg)
	value := strings.TrimSpace(token.RefreshToken)
	hint := "refresh_token"
	if value == "" {
		value = strings.TrimSpace(token.AccessToken)
		hint = "access_token"
	}
	if value == "" {
		return nil
	}
	discovery, err := discover(ctx, cfg)
	if err != nil {
		return err
	}
	if strings.TrimSpace(discovery.RevocationEndpoint) == "" {
		return ErrRevocationUnsupported
	}
	form := url.Values{}
	form.Set("client_id", cfg.ClientID)
	form.Set("token", value)
	form.Set("token_type_hint", hint)
	if err := postForm(ctx, cfg.HTTPClient, discovery.RevocationEndpoint, form, nil); err != nil {
		return fmt.Errorf("revoke xAI OAuth token: %w", err)
	}
	return nil
}

func (t Token) IsAccessTokenUsable(now time.Time) bool {
	if strings.TrimSpace(t.AccessToken) == "" || t.ExpiresAt.IsZero() {
		return false
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return t.ExpiresAt.After(now.UTC().Add(time.Minute))
}

func discover(ctx context.Context, cfg OAuthConfig) (discoveryDocument, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, DefaultIssuer+"/.well-known/openid-configuration", nil)
	if err != nil {
		return discoveryDocument{}, err
	}
	req.Header.Set("Accept", "application/json")
	response, err := cfg.HTTPClient.Do(req)
	if err != nil {
		return discoveryDocument{}, fmt.Errorf("fetch xAI OIDC discovery: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return discoveryDocument{}, newOAuthHTTPError(response)
	}
	var discovery discoveryDocument
	if err := decodeJSON(response.Body, &discovery); err != nil {
		return discoveryDocument{}, fmt.Errorf("decode xAI OIDC discovery: %w", err)
	}
	if strings.TrimSpace(discovery.Issuer) != DefaultIssuer {
		return discoveryDocument{}, fmt.Errorf("xAI OIDC discovery issuer does not match %s", DefaultIssuer)
	}
	if err := validateOAuthEndpoint("device_authorization_endpoint", discovery.DeviceAuthorizationEndpoint); err != nil {
		return discoveryDocument{}, err
	}
	if err := validateOAuthEndpoint("token_endpoint", discovery.TokenEndpoint); err != nil {
		return discoveryDocument{}, err
	}
	if strings.TrimSpace(discovery.RevocationEndpoint) != "" {
		if err := validateOAuthEndpoint("revocation_endpoint", discovery.RevocationEndpoint); err != nil {
			return discoveryDocument{}, err
		}
	}
	return discovery, nil
}

func normalizeOAuthConfig(cfg OAuthConfig) OAuthConfig {
	cfg.ClientID = strings.TrimSpace(cfg.ClientID)
	if cfg.ClientID == "" {
		cfg.ClientID = DefaultClientID
	}
	cfg.Scope = strings.Join(strings.Fields(cfg.Scope), " ")
	if cfg.Scope == "" {
		cfg.Scope = DefaultScope
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = http.DefaultClient
	}
	client := *cfg.HTTPClient
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	cfg.HTTPClient = &client
	return cfg
}

func (cfg OAuthConfig) now() time.Time {
	if cfg.Now == nil {
		return time.Now().UTC()
	}
	now := cfg.Now()
	if now.IsZero() {
		return time.Now().UTC()
	}
	return now.UTC()
}

func validateOAuthEndpoint(field, rawURL string) error {
	return validateHTTPSURL(field, rawURL, "auth.x.ai")
}

func validateHTTPSURL(field, rawURL string, allowedHosts ...string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil {
		return fmt.Errorf("xAI OAuth %s must be an HTTPS URL", field)
	}
	host := strings.ToLower(parsed.Hostname())
	trusted := false
	for _, allowed := range allowedHosts {
		if host == strings.ToLower(strings.TrimSpace(allowed)) {
			trusted = true
			break
		}
	}
	if !trusted {
		return fmt.Errorf("xAI OAuth %s uses untrusted host %q", field, host)
	}
	if parsed.Port() != "" && parsed.Port() != "443" {
		return fmt.Errorf("xAI OAuth %s uses unexpected port", field)
	}
	return nil
}

func postForm(ctx context.Context, client *http.Client, endpoint string, form url.Values, output any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return newOAuthHTTPError(response)
	}
	if output == nil || response.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return nil
	}
	return decodeJSON(response.Body, output)
}

type oauthHTTPError struct {
	StatusCode int
	Code       string
}

func (err *oauthHTTPError) Error() string {
	if err == nil {
		return "xAI OAuth request failed"
	}
	if code := displayOAuthErrorCode(err.Code); code != "" {
		return fmt.Sprintf("xAI OAuth request failed with HTTP %d (%s)", err.StatusCode, code)
	}
	return fmt.Sprintf("xAI OAuth request failed with HTTP %d", err.StatusCode)
}

func displayOAuthErrorCode(code string) string {
	switch strings.TrimSpace(code) {
	case "authorization_pending", "slow_down", "access_denied", "expired_token",
		"invalid_request", "invalid_client", "invalid_grant", "unauthorized_client",
		"unsupported_grant_type", "invalid_scope":
		return strings.TrimSpace(code)
	default:
		return ""
	}
}

func newOAuthHTTPError(response *http.Response) error {
	var payload struct {
		Error string `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&payload)
	return &oauthHTTPError{StatusCode: response.StatusCode, Code: strings.TrimSpace(payload.Error)}
}

func oauthErrorCode(err error) string {
	var target *oauthHTTPError
	if !errors.As(err, &target) {
		return ""
	}
	return target.Code
}

func tokenFromResponse(response tokenResponse, now time.Time, requireRefresh bool) (Token, error) {
	accessToken := strings.TrimSpace(response.AccessToken)
	refreshToken := strings.TrimSpace(response.RefreshToken)
	if accessToken == "" {
		return Token{}, fmt.Errorf("xAI OAuth token response is missing access_token")
	}
	if requireRefresh && refreshToken == "" {
		return Token{}, fmt.Errorf("xAI OAuth token response is missing refresh_token")
	}
	expiresIn := durationSeconds(response.ExpiresIn, 0)
	if expiresIn <= 0 {
		return Token{}, fmt.Errorf("xAI OAuth token response has invalid expires_in")
	}
	tokenType := strings.TrimSpace(response.TokenType)
	if tokenType == "" {
		tokenType = "Bearer"
	}
	now = now.UTC()
	return Token{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenType:    tokenType,
		Scope:        strings.Join(strings.Fields(response.Scope), " "),
		ExpiresAt:    now.Add(expiresIn),
		CreatedAt:    now,
		UpdatedAt:    now,
	}, nil
}

func durationSeconds(value any, fallback time.Duration) time.Duration {
	switch raw := value.(type) {
	case float64:
		if raw > 0 {
			return time.Duration(raw * float64(time.Second))
		}
	case json.Number:
		if seconds, err := strconv.ParseFloat(string(raw), 64); err == nil && seconds > 0 {
			return time.Duration(seconds * float64(time.Second))
		}
	case string:
		if seconds, err := strconv.ParseFloat(strings.TrimSpace(raw), 64); err == nil && seconds > 0 {
			return time.Duration(seconds * float64(time.Second))
		}
	}
	return fallback
}

func decodeJSON(reader io.Reader, output any) error {
	decoder := json.NewDecoder(io.LimitReader(reader, 1<<20))
	decoder.UseNumber()
	return decoder.Decode(output)
}
