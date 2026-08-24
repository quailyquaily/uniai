package xai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDeviceCodeLoginFlow(t *testing.T) {
	now := time.Date(2026, 8, 24, 4, 0, 0, 0, time.UTC)
	var polls atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			writeTestJSON(w, validDiscovery())
		case "/oauth2/device/code":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("client_id") != DefaultClientID || r.Form.Get("scope") != DefaultScope {
				t.Fatalf("device form = %v", r.Form)
			}
			writeTestJSON(w, map[string]any{
				"device_code":               "device-secret",
				"user_code":                 "ABCD-EFGH",
				"verification_uri":          "https://accounts.x.ai/activate",
				"verification_uri_complete": "https://accounts.x.ai/activate?code=ABCD-EFGH",
				"expires_in":                900,
				"interval":                  3,
			})
		case "/oauth2/token":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("grant_type") != deviceCodeGrantType || r.Form.Get("device_code") != "device-secret" {
				t.Fatalf("token form = %v", r.Form)
			}
			if polls.Add(1) == 1 {
				w.WriteHeader(http.StatusBadRequest)
				writeTestJSON(w, map[string]string{"error": "authorization_pending"})
				return
			}
			writeTestJSON(w, map[string]any{
				"access_token":  "access-secret",
				"refresh_token": "refresh-secret",
				"token_type":    "Bearer",
				"scope":         DefaultScope,
				"expires_in":    3600,
			})
		default:
			http.NotFound(w, r)
		}
	})
	cfg := OAuthConfig{HTTPClient: testHTTPClient(handler), Now: func() time.Time { return now }}
	code, err := RequestDeviceCode(context.Background(), cfg)
	if err != nil {
		t.Fatalf("RequestDeviceCode() error = %v", err)
	}
	if code.VerificationURL != "https://accounts.x.ai/activate" || code.UserCode != "ABCD-EFGH" || code.Interval != 3*time.Second {
		t.Fatalf("device code = %+v", code)
	}
	_, err = PollDeviceCode(context.Background(), cfg, code)
	if !errors.Is(err, ErrAuthorizationPending) {
		t.Fatalf("first PollDeviceCode() error = %v", err)
	}
	token, err := PollDeviceCode(context.Background(), cfg, code)
	if err != nil {
		t.Fatalf("second PollDeviceCode() error = %v", err)
	}
	if token.AccessToken != "access-secret" || token.RefreshToken != "refresh-secret" || !token.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("token = %+v", token)
	}
}

func TestRequestDeviceCodeRejectsUntrustedDiscoveryEndpoint(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		discovery := validDiscovery()
		discovery["token_endpoint"] = "https://attacker.example/oauth2/token"
		writeTestJSON(w, discovery)
	})
	_, err := RequestDeviceCode(context.Background(), OAuthConfig{HTTPClient: testHTTPClient(handler)})
	if err == nil || !strings.Contains(err.Error(), "token_endpoint") {
		t.Fatalf("RequestDeviceCode() error = %v", err)
	}
}

func TestRequestDeviceCodeDoesNotFollowRedirect(t *testing.T) {
	var requests atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Redirect(w, r, "https://attacker.example/discovery", http.StatusFound)
	})
	_, err := RequestDeviceCode(context.Background(), OAuthConfig{HTTPClient: testHTTPClient(handler)})
	if err == nil || !strings.Contains(err.Error(), "HTTP 302") {
		t.Fatalf("RequestDeviceCode() error = %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("request count = %d", requests.Load())
	}
}

func TestRefreshTokenKeepsUnrotatedRefreshToken(t *testing.T) {
	now := time.Date(2026, 8, 24, 5, 0, 0, 0, time.UTC)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			writeTestJSON(w, validDiscovery())
		case "/oauth2/token":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("refresh_token") != "refresh-secret" {
				t.Fatalf("refresh token = %q", r.Form.Get("refresh_token"))
			}
			writeTestJSON(w, map[string]any{"access_token": "access-new", "expires_in": 3600})
		}
	})
	token, err := RefreshToken(context.Background(), OAuthConfig{
		HTTPClient: testHTTPClient(handler),
		Now:        func() time.Time { return now },
	}, "refresh-secret")
	if err != nil {
		t.Fatalf("RefreshToken() error = %v", err)
	}
	if token.RefreshToken != "refresh-secret" {
		t.Fatalf("refresh token = %q", token.RefreshToken)
	}
}

func TestRefreshTokenDoesNotLeakSecret(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			writeTestJSON(w, validDiscovery())
		case "/oauth2/token":
			w.WriteHeader(http.StatusBadRequest)
			writeTestJSON(w, map[string]string{
				"error":             "invalid_grant_refresh-secret",
				"error_description": "refresh-secret was revoked",
			})
		}
	})
	_, err := RefreshToken(context.Background(), OAuthConfig{HTTPClient: testHTTPClient(handler)}, "refresh-secret")
	if err == nil {
		t.Fatal("RefreshToken() expected error")
	}
	if strings.Contains(err.Error(), "refresh-secret") {
		t.Fatalf("RefreshToken() leaked secret: %v", err)
	}
}

func TestRevokeTokenUsesRefreshToken(t *testing.T) {
	var revoked atomic.Bool
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			writeTestJSON(w, validDiscovery())
		case "/oauth2/revoke":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("token") != "refresh-secret" || r.Form.Get("token_type_hint") != "refresh_token" {
				t.Fatalf("revoke form = %v", r.Form)
			}
			revoked.Store(true)
			w.WriteHeader(http.StatusOK)
		}
	})
	err := RevokeToken(context.Background(), OAuthConfig{HTTPClient: testHTTPClient(handler)}, Token{
		AccessToken: "access-secret", RefreshToken: "refresh-secret",
	})
	if err != nil {
		t.Fatalf("RevokeToken() error = %v", err)
	}
	if !revoked.Load() {
		t.Fatal("revocation endpoint was not called")
	}
}

func validDiscovery() map[string]any {
	return map[string]any{
		"issuer":                        DefaultIssuer,
		"device_authorization_endpoint": DefaultIssuer + "/oauth2/device/code",
		"token_endpoint":                DefaultIssuer + "/oauth2/token",
		"revocation_endpoint":           DefaultIssuer + "/oauth2/revoke",
	}
}

func testHTTPClient(handler http.Handler) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req.Clone(req.Context()))
		return recorder.Result(), nil
	})}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func writeTestJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
