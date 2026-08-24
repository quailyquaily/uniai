package codex

import (
	"bytes"
	"context"
	"encoding/base64"
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
	DefaultClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
	DefaultIssuer   = "https://auth.openai.com"
	DefaultAPIBase  = "https://chatgpt.com/backend-api/codex"

	defaultDeviceTTL    = 15 * time.Minute
	defaultPollInterval = 5 * time.Second
)

var (
	ErrAuthorizationPending = errors.New("codex device authorization pending")
	ErrDeviceCodeExpired    = errors.New("codex device authorization expired")
	ErrNotLoggedIn          = errors.New("codex OAuth is not logged in")
)

type OAuthConfig struct {
	ClientID   string
	HTTPClient *http.Client
	Now        func() time.Time
}

type DeviceCode struct {
	VerificationURL string
	UserCode        string
	DeviceAuthID    string
	Interval        time.Duration
	ExpiresAt       time.Time
}

type Token struct {
	IDToken      string    `json:"id_token,omitempty"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	AccountID    string    `json:"account_id,omitempty"`
	PlanType     string    `json:"plan_type,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	CreatedAt    time.Time `json:"created_at,omitempty"`
	UpdatedAt    time.Time `json:"updated_at,omitempty"`
}

type userCodeResponse struct {
	DeviceAuthID string `json:"device_auth_id"`
	UserCode     string `json:"user_code"`
	UserCodeAlt  string `json:"usercode"`
	Interval     any    `json:"interval"`
	ExpiresIn    any    `json:"expires_in"`
}

type tokenPollResponse struct {
	AuthorizationCode string `json:"authorization_code"`
	CodeVerifier      string `json:"code_verifier"`
}

type tokenResponse struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    any    `json:"expires_in"`
}

func RequestDeviceCode(ctx context.Context, cfg OAuthConfig) (DeviceCode, error) {
	cfg = normalizeOAuthConfig(cfg)
	var response userCodeResponse
	if err := postJSON(ctx, cfg.HTTPClient, DefaultIssuer+"/api/accounts/deviceauth/usercode", map[string]string{
		"client_id": cfg.ClientID,
	}, &response); err != nil {
		return DeviceCode{}, err
	}
	userCode := firstNonEmpty(response.UserCode, response.UserCodeAlt)
	deviceAuthID := strings.TrimSpace(response.DeviceAuthID)
	if deviceAuthID == "" || userCode == "" {
		return DeviceCode{}, fmt.Errorf("codex device auth response missing device id or user code")
	}
	now := cfg.now()
	return DeviceCode{
		VerificationURL: DefaultIssuer + "/codex/device",
		UserCode:        userCode,
		DeviceAuthID:    deviceAuthID,
		Interval:        durationFromAny(response.Interval, defaultPollInterval),
		ExpiresAt:       now.Add(durationFromAny(response.ExpiresIn, defaultDeviceTTL)),
	}, nil
}

// PollDeviceCode performs one poll and exchanges a completed authorization for
// tokens. Callers decide how often to poll and how to present pending state.
func PollDeviceCode(ctx context.Context, cfg OAuthConfig, code DeviceCode) (Token, error) {
	cfg = normalizeOAuthConfig(cfg)
	if strings.TrimSpace(code.DeviceAuthID) == "" || strings.TrimSpace(code.UserCode) == "" {
		return Token{}, fmt.Errorf("codex device authorization session is invalid")
	}
	if !code.ExpiresAt.IsZero() && !code.ExpiresAt.After(cfg.now()) {
		return Token{}, ErrDeviceCodeExpired
	}

	requestBody, err := json.Marshal(map[string]string{
		"device_auth_id": strings.TrimSpace(code.DeviceAuthID),
		"user_code":      strings.TrimSpace(code.UserCode),
	})
	if err != nil {
		return Token{}, err
	}
	pollEndpoint := DefaultIssuer + "/api/accounts/deviceauth/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, pollEndpoint, bytes.NewReader(requestBody))
	if err != nil {
		return Token{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := cfg.HTTPClient.Do(req)
	if err != nil {
		return Token{}, fmt.Errorf("codex device auth request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusNotFound {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return Token{}, ErrAuthorizationPending
	}
	if response.StatusCode == http.StatusBadRequest || response.StatusCode == http.StatusTooEarly || response.StatusCode == http.StatusAccepted {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		if isPendingBody(body) {
			return Token{}, ErrAuthorizationPending
		}
		return Token{}, statusError(pollEndpoint, response.StatusCode, body)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return Token{}, statusError(pollEndpoint, response.StatusCode, body)
	}
	var poll tokenPollResponse
	if err := decodeJSON(response.Body, &poll); err != nil {
		return Token{}, fmt.Errorf("decode codex device auth response: %w", err)
	}
	if strings.TrimSpace(poll.AuthorizationCode) == "" || strings.TrimSpace(poll.CodeVerifier) == "" {
		return Token{}, fmt.Errorf("codex device auth response missing authorization code or verifier")
	}
	return exchangeAuthorizationCode(ctx, cfg, poll.AuthorizationCode, poll.CodeVerifier)
}

func RefreshToken(ctx context.Context, cfg OAuthConfig, refreshToken string) (Token, error) {
	cfg = normalizeOAuthConfig(cfg)
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return Token{}, ErrNotLoggedIn
	}
	form := url.Values{}
	form.Set("client_id", cfg.ClientID)
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)

	endpoint := DefaultIssuer + "/oauth/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := cfg.HTTPClient.Do(req)
	if err != nil {
		return Token{}, fmt.Errorf("codex refresh token request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		if refreshTokenRejected(response.StatusCode, body) {
			return Token{}, ErrNotLoggedIn
		}
		return Token{}, fmt.Errorf("codex refresh token request failed with HTTP %d", response.StatusCode)
	}
	var decoded tokenResponse
	if err := decodeJSON(response.Body, &decoded); err != nil {
		return Token{}, fmt.Errorf("decode codex refresh token response: %w", err)
	}
	token := tokenFromResponse(decoded, cfg.now())
	if token.AccessToken == "" {
		return Token{}, fmt.Errorf("codex refresh response missing access token")
	}
	if token.RefreshToken == "" {
		token.RefreshToken = refreshToken
	}
	return token, nil
}

func (t Token) IsAccessTokenUsable(now time.Time) bool {
	if strings.TrimSpace(t.AccessToken) == "" {
		return false
	}
	if t.ExpiresAt.IsZero() {
		return true
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return t.ExpiresAt.After(now.UTC().Add(time.Minute))
}

func exchangeAuthorizationCode(ctx context.Context, cfg OAuthConfig, code, verifier string) (Token, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", strings.TrimSpace(code))
	form.Set("redirect_uri", DefaultIssuer+"/deviceauth/callback")
	form.Set("client_id", cfg.ClientID)
	form.Set("code_verifier", strings.TrimSpace(verifier))

	endpoint := DefaultIssuer + "/oauth/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := cfg.HTTPClient.Do(req)
	if err != nil {
		return Token{}, fmt.Errorf("codex token exchange request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return Token{}, statusError(endpoint, response.StatusCode, body)
	}
	var decoded tokenResponse
	if err := decodeJSON(response.Body, &decoded); err != nil {
		return Token{}, fmt.Errorf("decode codex token exchange response: %w", err)
	}
	token := tokenFromResponse(decoded, cfg.now())
	if token.AccessToken == "" || token.RefreshToken == "" {
		return Token{}, fmt.Errorf("codex token exchange response missing access or refresh token")
	}
	return token, nil
}

func tokenFromResponse(response tokenResponse, now time.Time) Token {
	now = now.UTC()
	expiresAt := time.Time{}
	if duration := durationFromAny(response.ExpiresIn, 0); duration > 0 {
		expiresAt = now.Add(duration)
	} else if expiration, ok := JWTExpiration(response.AccessToken); ok {
		expiresAt = expiration
	}
	return Token{
		IDToken:      strings.TrimSpace(response.IDToken),
		AccessToken:  strings.TrimSpace(response.AccessToken),
		RefreshToken: strings.TrimSpace(response.RefreshToken),
		AccountID: firstNonEmpty(
			JWTStringClaim(response.IDToken, "chatgpt_account_id"),
			JWTStringClaim(response.AccessToken, "chatgpt_account_id"),
			JWTStringClaim(response.AccessToken, "account_id"),
		),
		PlanType: firstNonEmpty(
			JWTStringClaim(response.AccessToken, "chatgpt_plan_type"),
			JWTStringClaim(response.IDToken, "chatgpt_plan_type"),
		),
		ExpiresAt: expiresAt,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func JWTExpiration(token string) (time.Time, bool) {
	claims, ok := jwtClaims(token)
	if !ok {
		return time.Time{}, false
	}
	expiration, ok := numberClaim(claims, "exp")
	if !ok || expiration <= 0 {
		return time.Time{}, false
	}
	return time.Unix(int64(expiration), 0).UTC(), true
}

func JWTStringClaim(token, key string) string {
	claims, ok := jwtClaims(token)
	if !ok || strings.TrimSpace(key) == "" {
		return ""
	}
	if value, ok := claims[key].(string); ok {
		return strings.TrimSpace(value)
	}
	if auth, ok := claims["https://api.openai.com/auth"].(map[string]any); ok {
		if value, ok := auth[key].(string); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func jwtClaims(token string) (map[string]any, bool) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) < 2 || parts[1] == "" {
		return nil, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		if payload, err = base64.URLEncoding.DecodeString(parts[1]); err != nil {
			return nil, false
		}
	}
	var claims map[string]any
	if json.Unmarshal(payload, &claims) != nil {
		return nil, false
	}
	return claims, true
}

func numberClaim(claims map[string]any, key string) (float64, bool) {
	switch value := claims[key].(type) {
	case float64:
		return value, true
	case json.Number:
		parsed, err := value.Float64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func postJSON(ctx context.Context, client *http.Client, endpoint string, input, output any) error {
	data, err := json.Marshal(input)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("codex OAuth request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return statusError(endpoint, response.StatusCode, body)
	}
	if err := decodeJSON(response.Body, output); err != nil {
		return fmt.Errorf("decode codex OAuth response: %w", err)
	}
	return nil
}

func decodeJSON(reader io.Reader, output any) error {
	decoder := json.NewDecoder(io.LimitReader(reader, 1<<20))
	decoder.UseNumber()
	return decoder.Decode(output)
}

func statusError(endpoint string, status int, _ []byte) error {
	parsed, _ := url.Parse(endpoint)
	target := endpoint
	if parsed != nil && parsed.Host != "" {
		target = parsed.Host + parsed.Path
	}
	return fmt.Errorf("codex OAuth request to %s failed with HTTP %d", target, status)
}

func parseErrorMessage(body []byte) string {
	var decoded map[string]any
	if json.Unmarshal(bytes.TrimSpace(body), &decoded) != nil {
		return ""
	}
	for _, key := range []string{"error", "code", "message"} {
		if value, ok := decoded[key].(string); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func refreshTokenRejected(status int, body []byte) bool {
	if status != http.StatusBadRequest && status != http.StatusUnauthorized {
		return false
	}
	var payload map[string]any
	_ = json.Unmarshal(body, &payload)
	if code, _ := payload["error"].(string); strings.EqualFold(strings.TrimSpace(code), "invalid_grant") {
		return true
	}
	text := strings.ToLower(string(body))
	if !strings.Contains(text, "refresh token") {
		return false
	}
	return strings.Contains(text, "already been used") || strings.Contains(text, "expired") ||
		strings.Contains(text, "invalid") || strings.Contains(text, "revoked")
}

func isPendingBody(body []byte) bool {
	message := strings.ToLower(parseErrorMessage(body))
	return strings.Contains(message, "authorization_pending") || strings.Contains(message, "pending") || strings.Contains(message, "slow_down")
}

func normalizeOAuthConfig(cfg OAuthConfig) OAuthConfig {
	cfg.ClientID = strings.TrimSpace(cfg.ClientID)
	if cfg.ClientID == "" {
		cfg.ClientID = DefaultClientID
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

func durationFromAny(raw any, fallback time.Duration) time.Duration {
	switch value := raw.(type) {
	case float64:
		if value > 0 {
			return time.Duration(value * float64(time.Second))
		}
	case json.Number:
		if seconds, err := strconv.ParseFloat(string(value), 64); err == nil && seconds > 0 {
			return time.Duration(seconds * float64(time.Second))
		}
	case string:
		if seconds, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil && seconds > 0 {
			return time.Duration(seconds * float64(time.Second))
		}
	}
	return fallback
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
