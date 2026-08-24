package codex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDeviceCodeLoginFlow(t *testing.T) {
	now := time.Date(2026, 8, 24, 1, 2, 3, 0, time.UTC)
	accessToken := testJWT(t, map[string]any{
		"exp": now.Add(time.Hour).Unix(),
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acc_123",
			"chatgpt_plan_type":  "plus",
		},
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/api/accounts/deviceauth/usercode", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["client_id"] != DefaultClientID {
			t.Fatalf("client_id = %q", body["client_id"])
		}
		writeTestJSON(w, map[string]any{
			"device_auth_id": "dev_123",
			"user_code":      "ABCD-EFGH",
			"interval":       1,
			"expires_in":     900,
		})
	})
	mux.HandleFunc("/api/accounts/deviceauth/token", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			DeviceAuthID string `json:"device_auth_id"`
			UserCode     string `json:"user_code"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.DeviceAuthID != "dev_123" || body.UserCode != "ABCD-EFGH" {
			t.Fatalf("poll body = %+v", body)
		}
		writeTestJSON(w, map[string]any{
			"authorization_code": "code_123",
			"code_verifier":      "verifier_123",
		})
	})
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("grant_type") != "authorization_code" ||
			r.Form.Get("code") != "code_123" ||
			r.Form.Get("code_verifier") != "verifier_123" {
			t.Fatalf("token form = %v", r.Form)
		}
		writeTestJSON(w, map[string]any{
			"access_token":  accessToken,
			"refresh_token": "refresh_123",
		})
	})

	cfg := OAuthConfig{HTTPClient: testHTTPClient(mux), Now: func() time.Time { return now }}
	code, err := RequestDeviceCode(context.Background(), cfg)
	if err != nil {
		t.Fatalf("RequestDeviceCode() error = %v", err)
	}
	if code.VerificationURL != DefaultIssuer+"/codex/device" || code.UserCode != "ABCD-EFGH" {
		t.Fatalf("device code = %+v", code)
	}

	token, err := PollDeviceCode(context.Background(), cfg, code)
	if err != nil {
		t.Fatalf("PollDeviceCode() error = %v", err)
	}
	if token.AccessToken != accessToken || token.RefreshToken != "refresh_123" {
		t.Fatalf("token = %+v", token)
	}
	if token.AccountID != "acc_123" || token.PlanType != "plus" {
		t.Fatalf("claims not extracted: %+v", token)
	}
}

func TestPollDeviceCodeReportsPending(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/accounts/deviceauth/token", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	_, err := PollDeviceCode(context.Background(), OAuthConfig{
		HTTPClient: testHTTPClient(mux),
	}, DeviceCode{
		DeviceAuthID: "device",
		UserCode:     "code",
		ExpiresAt:    time.Now().Add(time.Minute),
	})
	if !errors.Is(err, ErrAuthorizationPending) {
		t.Fatalf("PollDeviceCode() error = %v, want ErrAuthorizationPending", err)
	}
}

func TestRefreshTokenKeepsUnrotatedRefreshToken(t *testing.T) {
	now := time.Date(2026, 8, 24, 2, 0, 0, 0, time.UTC)
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("refresh_token") != "refresh-secret" {
			t.Fatalf("refresh_token = %q", r.Form.Get("refresh_token"))
		}
		writeTestJSON(w, map[string]any{
			"access_token": testJWT(t, map[string]any{"exp": now.Add(time.Hour).Unix()}),
		})
	})
	token, err := RefreshToken(context.Background(), OAuthConfig{
		HTTPClient: testHTTPClient(mux),
		Now:        func() time.Time { return now },
	}, "refresh-secret")
	if err != nil {
		t.Fatalf("RefreshToken() error = %v", err)
	}
	if token.RefreshToken != "refresh-secret" {
		t.Fatalf("refresh token = %q", token.RefreshToken)
	}
}

func TestRefreshTokenRejectsInvalidGrantWithoutLeakingSecret(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		writeTestJSON(w, map[string]any{
			"error":             "invalid_grant",
			"error_description": "refresh-secret was revoked",
		})
	})
	_, err := RefreshToken(context.Background(), OAuthConfig{HTTPClient: testHTTPClient(mux)}, "refresh-secret")
	if !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("RefreshToken() error = %v, want ErrNotLoggedIn", err)
	}
	if strings.Contains(err.Error(), "refresh-secret") {
		t.Fatalf("RefreshToken() leaked secret: %v", err)
	}
}

func TestTokenExchangeErrorDoesNotLeakAuthorizationCode(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/accounts/deviceauth/token", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]string{
			"authorization_code": "authorization-secret",
			"code_verifier":      "verifier-secret",
		})
	})
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		writeTestJSON(w, map[string]string{
			"message": "authorization-secret and verifier-secret were rejected",
		})
	})
	_, err := PollDeviceCode(context.Background(), OAuthConfig{HTTPClient: testHTTPClient(mux)}, DeviceCode{
		DeviceAuthID: "device-secret",
		UserCode:     "user-code",
		ExpiresAt:    time.Now().Add(time.Minute),
	})
	if err == nil {
		t.Fatal("PollDeviceCode() expected error")
	}
	if strings.Contains(err.Error(), "authorization-secret") || strings.Contains(err.Error(), "verifier-secret") {
		t.Fatalf("PollDeviceCode() leaked token exchange secrets: %v", err)
	}
}

func TestTokenIsAccessTokenUsable(t *testing.T) {
	now := time.Date(2026, 8, 24, 3, 0, 0, 0, time.UTC)
	if (Token{AccessToken: "token", ExpiresAt: now.Add(30 * time.Second)}).IsAccessTokenUsable(now) {
		t.Fatal("token inside refresh margin should not be usable")
	}
	if !(Token{AccessToken: "token", ExpiresAt: now.Add(2 * time.Minute)}).IsAccessTokenUsable(now) {
		t.Fatal("fresh token should be usable")
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

func testJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	header, err := json.Marshal(map[string]string{"alg": "none", "typ": "JWT"})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(header) + "." +
		base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

func writeTestJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
