package claude

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func oauthResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestAuthorizationPKCEAndExchange(t *testing.T) {
	now := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	cfg := OAuthConfig{Now: func() time.Time { return now }}
	auth, err := BeginAuthorization(cfg)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(auth.URL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	digest := sha256.Sum256([]byte(auth.CodeVerifier))
	if u.Scheme != "https" || q.Get("client_id") != DefaultClientID || q.Get("redirect_uri") != RedirectURI ||
		q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(digest[:]) ||
		q.Get("state") != auth.State || len(auth.State) < 32 || len(auth.CodeVerifier) < 43 {
		t.Fatal("invalid authorization URL or PKCE parameters")
	}
	other, _ := BeginAuthorization(cfg)
	if auth.State == other.State || auth.CodeVerifier == other.CodeVerifier {
		t.Fatal("authorization randomness reused")
	}
	cfg.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != TokenURL || r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("request = %s %s", r.Method, r.URL)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["grant_type"] != "authorization_code" || body["code"] != "the-code" || body["code_verifier"] != auth.CodeVerifier || body["state"] != auth.State {
			t.Fatal("incorrect code exchange")
		}
		return oauthResponse(200, `{"access_token":"access","refresh_token":"refresh","expires_in":3600,"scope":"user:inference","account":{"uuid":"account"}}`), nil
	})}
	token, err := ExchangeCode(context.Background(), cfg, auth, "the-code#"+auth.State)
	if err != nil {
		t.Fatal(err)
	}
	if token.AccountID != "account" || token.RefreshToken != "refresh" || token.ExpiresAt != now.Add(time.Hour) || !token.IsAccessTokenUsable(now) || token.IsAccessTokenUsable(now.Add(time.Hour-time.Second)) {
		t.Fatalf("unexpected token metadata: %+v", token)
	}
}

func TestExchangeRejectsMissingWrongOrExpiredStateBeforeNetwork(t *testing.T) {
	now := time.Now()
	cfg := OAuthConfig{Now: func() time.Time { return now }, HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("unexpected network request"); return nil, nil })}}
	auth, _ := BeginAuthorization(cfg)
	for _, input := range []string{"", "bare-code", "code#wrong", "https://example.com/?code=x&state=wrong"} {
		if _, err := ExchangeCode(context.Background(), cfg, auth, input); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
	now = now.Add(time.Hour)
	if _, err := ExchangeCode(context.Background(), cfg, auth, "code#"+auth.State); err == nil {
		t.Fatal("accepted expired authorization")
	}
}

func TestRefreshAndSafeErrors(t *testing.T) {
	for _, status := range []int{200, 400, 401, 429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			cfg := OAuthConfig{HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				var body map[string]string
				json.NewDecoder(r.Body).Decode(&body)
				if body["grant_type"] != "refresh_token" || body["refresh_token"] != "secret-refresh" || body["client_id"] != DefaultClientID {
					t.Fatal("incorrect refresh request")
				}
				if status == 200 {
					return oauthResponse(status, `{"access_token":"next","expires_in":3600}`), nil
				}
				return oauthResponse(status, `{"error":"invalid_grant","error_description":"secret-refresh"}`), nil
			})}}
			token, err := RefreshToken(context.Background(), cfg, "secret-refresh")
			if status == 200 {
				if err != nil || token.RefreshToken != "secret-refresh" {
					t.Fatalf("refresh: %v", err)
				}
				return
			}
			if err == nil || strings.Contains(err.Error(), "secret-refresh") {
				t.Fatalf("unsafe error: %v", err)
			}
			if errors.Is(err, ErrNotLoggedIn) != (status == 400 || status == 401) {
				t.Fatalf("wrong auth error classification: %v", err)
			}
		})
	}
}

func TestTokenRequestsDoNotFollowRedirects(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		resp := oauthResponse(307, "")
		resp.Header.Set("Location", "https://other.example/token")
		return resp, nil
	})}
	_, err := RefreshToken(context.Background(), OAuthConfig{HTTPClient: client}, "refresh")
	if err == nil || calls != 1 || client.CheckRedirect != nil {
		t.Fatalf("redirect protection: calls=%d err=%v", calls, err)
	}
}
