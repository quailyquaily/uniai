package uniai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/quailyquaily/uniai/internal/httputil"
)

func TestListModelsRedirects(t *testing.T) {
	for _, provider := range []struct{ name, authHeader, authValue, body string }{
		{"anthropic", "x-api-key", "key", `{"data":[]}`},
		{"gemini", "x-goog-api-key", "key", `{"models":[]}`},
		{"openai", "Authorization", "Bearer key", `{"data":[]}`},
		{"cloudflare", "Authorization", "Bearer key", `{"success":true,"result":[]}`},
	} {
		for _, customClient := range []bool{false, true} {
			for _, tt := range []struct {
				name, target string
				wantCalls    int
				wantErr      bool
			}{
				{"same origin", "/redirected-models", 2, false},
				{"other host", "https://unrelated.example/models", 1, true},
				{"subdomain", "https://sub.provider.example/models", 1, true},
				{"other port", "https://provider.example:8443/models", 1, true},
				{"downgrade", "http://provider.example/models", 1, true},
				{"userinfo", "https://user:password@provider.example/models", 1, true},
				{"loop", "/loop", 10, true},
			} {
				t.Run(fmt.Sprintf("%s/custom=%t/%s", provider.name, customClient, tt.name), func(t *testing.T) {
					calls := 0
					httpClient := &http.Client{Transport: rootRoundTripFunc(func(r *http.Request) (*http.Response, error) {
						calls++
						if r.Header.Get(provider.authHeader) != provider.authValue || r.Header.Get("X-Proxy-Key") != "proxy-key" {
							t.Error("lost authentication headers on same-origin request")
						}
						if calls == 1 || tt.name == "loop" {
							return &http.Response{StatusCode: 302, Header: http.Header{"Location": {tt.target}}, Body: io.NopCloser(strings.NewReader(""))}, nil
						}
						return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(provider.body))}, nil
					})}
					cfg := Config{Provider: provider.name, OpenAIAPIKey: "key", AnthropicAPIKey: "key", GeminiAPIKey: "key", CloudflareAPIToken: "key", CloudflareAccountID: "account", OpenAIAPIBase: "https://provider.example", AnthropicAPIBase: "https://provider.example", GeminiAPIBase: "https://provider.example", CloudflareAPIBase: "https://provider.example", ModelsHeaders: map[string]string{"X-Proxy-Key": "proxy-key"}}
					if customClient {
						cfg.ModelsHTTPClient = httpClient
					} else {
						original := httputil.DefaultClient
						httputil.DefaultClient = httpClient
						t.Cleanup(func() { httputil.DefaultClient = original })
					}
					got, err := New(cfg).ListModels(context.Background(), "")
					if (err != nil) != tt.wantErr || calls != tt.wantCalls {
						t.Fatalf("calls=%d, error=%v; want calls=%d, error=%t", calls, err, tt.wantCalls, tt.wantErr)
					}
					if tt.wantErr && got != nil {
						t.Errorf("failed redirect returned a partial catalog: %#v", got)
					}
					if httpClient.CheckRedirect != nil {
						t.Error("modified the original HTTP client's redirect policy")
					}
				})
			}
		}
	}
}

func TestListModelsCustomRedirectPolicy(t *testing.T) {
	blocked := errors.New("redirect rejected by caller")
	for _, tt := range []struct {
		name, target string
		policyErr    error
		wantCalls    int
		wantErr      bool
	}{
		{"allow same origin", "/redirected", nil, 2, false},
		{"cannot allow other origin", "https://unrelated.example/models", nil, 1, true},
		{"caller rejection", "/redirected", blocked, 1, true},
		{"return redirect response", "/redirected", http.ErrUseLastResponse, 1, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls, policyCalls := 0, 0
			client := &http.Client{
				CheckRedirect: func(r *http.Request, via []*http.Request) error {
					policyCalls++
					return tt.policyErr
				},
				Transport: rootRoundTripFunc(func(r *http.Request) (*http.Response, error) {
					calls++
					if calls == 1 {
						return &http.Response{StatusCode: 302, Header: http.Header{"Location": {tt.target}}, Body: io.NopCloser(strings.NewReader(""))}, nil
					}
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":[]}`))}, nil
				}),
			}
			_, err := New(Config{OpenAIAPIKey: "key", ModelsHTTPClient: client}).ListModels(context.Background(), "")
			if (err != nil) != tt.wantErr || calls != tt.wantCalls {
				t.Fatalf("calls=%d, error=%v; want calls=%d, error=%t", calls, err, tt.wantCalls, tt.wantErr)
			}
			if tt.policyErr == blocked && !errors.Is(err, blocked) {
				t.Errorf("lost caller's redirect error: %v", err)
			}
			if tt.target == "/redirected" && policyCalls != 1 {
				t.Errorf("custom redirect policy called %d times", policyCalls)
			}
		})
	}
}

func TestListModelsProviders(t *testing.T) {
	for _, tt := range []struct {
		provider, base, path, authHeader, authValue string
		pages                                       []string
		queries                                     []string
		want                                        []ModelInfo
	}{
		{provider: "openai", base: "https://proxy.example/custom/v1/", path: "/custom/v1/models", authHeader: "Authorization", authValue: "Bearer openai-key",
			pages: []string{`{"object":"list","data":[{"id":"custom-model","owned_by":"vendor","created":123,"extra":{"thinking":true}}]}`}, queries: []string{""}, want: []ModelInfo{{ID: "custom-model", OwnedBy: "vendor"}}},
		{provider: "anthropic", base: "https://proxy.example/claude/v1/", path: "/claude/v1/models", authHeader: "x-api-key", authValue: "anthropic-key",
			pages:   []string{`{"data":[{"id":"claude-sonnet-5-5","display_name":"Sonnet 5.5","max_input_tokens":1000000,"max_tokens":128000,"capabilities":{"thinking":{"supported":true}}}],"has_more":true,"last_id":"claude-sonnet-5-5"}`, `{"data":[{"id":"older","display_name":"Older"}],"has_more":false}`},
			queries: []string{"limit=1000", "after_id=claude-sonnet-5-5&limit=1000"}, want: []ModelInfo{{ID: "claude-sonnet-5-5", DisplayName: "Sonnet 5.5", InputTokenLimit: 1000000, OutputTokenLimit: 128000}, {ID: "older", DisplayName: "Older"}}},
		{provider: "gemini", base: "https://proxy.example/google/", path: "/google/v1beta/models", authHeader: "x-goog-api-key", authValue: "gemini-key",
			pages:   []string{`{"models":[{"name":"models/gemini-example","displayName":"Gemini Example","description":"test","inputTokenLimit":1000000,"outputTokenLimit":65536,"supportedGenerationMethods":["generateContent"]}],"nextPageToken":"next+/="}`, `{"models":[{"name":"models/embedding-example","supportedGenerationMethods":["embedContent"]}]}`},
			queries: []string{"pageSize=1000", "pageSize=1000&pageToken=next%2B%2F%3D"}, want: []ModelInfo{{ID: "gemini-example", DisplayName: "Gemini Example", Description: "test", InputTokenLimit: 1000000, OutputTokenLimit: 65536}, {ID: "embedding-example"}}},
		{provider: "cloudflare", base: "https://proxy.example/client/v4/", path: "/client/v4/accounts/account-123/ai/models/search", authHeader: "Authorization", authValue: "Bearer cf-key",
			pages:   []string{`{"success":true,"result":[{"id":"internal-uuid","name":"@cf/vendor/model-one","description":"one","task":{"name":"Text Generation"}}]}`, `{"success":true,"result":[{"id":"another-uuid","name":"@cf/vendor/model-two"}]}`, `{"success":true,"result":[]}`},
			queries: []string{"page=1&per_page=100", "page=2&per_page=100", "page=3&per_page=100"}, want: []ModelInfo{{ID: "@cf/vendor/model-one", Description: "one"}, {ID: "@cf/vendor/model-two"}}},
	} {
		t.Run(tt.provider, func(t *testing.T) {
			requests := 0
			headers := map[string]string{"X-Models-Test": "original"}
			cfg := Config{Provider: tt.provider, OpenAIAPIKey: "openai-key", AnthropicAPIKey: "anthropic-key", GeminiAPIKey: "gemini-key", CloudflareAPIToken: "cf-key", CloudflareAccountID: "account-123", OpenAIAPIBase: tt.base, AnthropicAPIBase: tt.base, GeminiAPIBase: tt.base, CloudflareAPIBase: tt.base, ModelsHeaders: headers, ChatHeaders: map[string]string{"X-Chat-Only": "secret"}}
			cfg.ModelsHTTPClient = &http.Client{Transport: rootRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				if requests >= len(tt.pages) {
					t.Fatal("unexpected extra request")
				}
				if r.Method != "GET" || r.URL.Path != tt.path || r.URL.RawQuery != tt.queries[requests] {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				if r.Header.Get(tt.authHeader) != tt.authValue || r.Header.Get("X-Models-Test") != "original" || r.Header.Get("X-Chat-Only") != "" {
					t.Error("incorrect authentication or custom headers")
				}
				if tt.provider == "anthropic" && r.Header.Get("anthropic-version") != "2023-06-01" {
					t.Error("missing anthropic version")
				}
				body := tt.pages[requests]
				requests++
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})}
			client := New(cfg)
			headers["X-Models-Test"] = "mutated"
			got, err := client.ListModels(context.Background(), "")
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tt.want) || requests != len(tt.pages) {
				t.Fatalf("got %d models and %d requests", len(got), requests)
			}
			for i := range got {
				if !json.Valid(got[i].Raw) {
					t.Fatalf("invalid raw metadata: %s", got[i].Raw)
				}
				if tt.provider == "openai" && !strings.Contains(string(got[i].Raw), `"extra"`) {
					t.Error("lost unknown metadata")
				}
				got[i].Raw = nil
				if !reflect.DeepEqual(got[i], tt.want[i]) {
					t.Errorf("got %#v, want %#v", got[i], tt.want[i])
				}
			}
		})
	}
}

func TestListModelsRouting(t *testing.T) {
	for _, tt := range []struct{ provider, host, path string }{
		{"", "api.openai.com", "/v1/models"}, {"openai_resp", "api.openai.com", "/v1/models"},
		{"openai_codex", "api.openai.com", "/v1/models"}, {"deepseek", "api.deepseek.com", "/models"},
		{"xai", "api.x.ai", "/v1/models"}, {"groq", "api.groq.com", "/openai/v1/models"},
		{"meta", "api.ai.meta.com", "/v1/models"}, {"sakana", "api.sakana.ai", "/v1/models"},
	} {
		t.Run(tt.provider, func(t *testing.T) {
			c := New(Config{Provider: "anthropic", OpenAIAPIKey: "test", ModelsHTTPClient: &http.Client{Transport: rootRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != tt.host || r.URL.Path != tt.path {
					t.Errorf("unexpected endpoint: %s", r.URL)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":[]}`)), Header: make(http.Header)}, nil
			})}})
			provider := tt.provider
			if provider == "" {
				c.cfg.Provider = ""
			}
			got, err := c.ListModels(context.Background(), provider)
			if err != nil || got == nil || len(got) != 0 {
				t.Fatalf("got %#v, %v", got, err)
			}
		})
	}
}

func TestListModelsFailures(t *testing.T) {
	for _, tt := range []struct {
		name, provider string
		status         int
		body           string
	}{
		{"unauthorized", "openai", 401, `{"error":{"message":"invalid key"}}`},
		{"missing endpoint", "openai", 404, `not found`},
		{"bad json", "openai", 200, `<html>`},
		{"null response", "openai", 200, `null`},
		{"missing data", "openai", 200, `{}`},
		{"invalid model", "openai", 200, `{"data":[{}]}`},
		{"error envelope", "gemini", 200, `{"error":{"message":"denied"}}`},
		{"cloudflare failure", "cloudflare", 200, `{"success":false,"errors":[{"message":"denied"}],"result":[]}`},
		{"missing success", "cloudflare", 200, `{"result":[]}`},
		{"missing cursor", "anthropic", 200, `{"data":[{"id":"one"}],"has_more":true}`},
		{"repeated cursor", "anthropic", 200, `{"data":[{"id":"one"}],"has_more":true,"last_id":"one"}`},
		{"repeated token", "gemini", 200, `{"models":[{"name":"models/one"}],"nextPageToken":"repeat"}`},
		{"repeated page", "cloudflare", 200, `{"success":true,"result":[{"name":"@cf/one"}]}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			c := New(Config{Provider: tt.provider, OpenAIAPIKey: "key", AnthropicAPIKey: "key", GeminiAPIKey: "key", CloudflareAPIToken: "key", CloudflareAccountID: "acct", ModelsHTTPClient: &http.Client{Transport: rootRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if calls > 3 {
					t.Fatal("pagination failed to terminate")
				}
				return &http.Response{StatusCode: tt.status, Body: io.NopCloser(strings.NewReader(tt.body)), Header: make(http.Header)}, nil
			})}})
			got, err := c.ListModels(context.Background(), "")
			if err == nil || got != nil {
				t.Fatalf("expected error without partial list, got %#v, %v", got, err)
			}
			if tt.status != 200 && !strings.Contains(err.Error(), fmt.Sprint(tt.status)) {
				t.Errorf("missing HTTP status: %v", err)
			}
		})
	}
}

func TestListModelsCancellationAndUnsupported(t *testing.T) {
	calls := 0
	c := New(Config{OpenAIAPIKey: "key", ModelsHTTPClient: &http.Client{Transport: rootRoundTripFunc(func(r *http.Request) (*http.Response, error) { calls++; return nil, r.Context().Err() })}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.ListModels(ctx, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	for _, provider := range []string{"azure", "bedrock", "claude_oauth", "xai_oauth", "unknown"} {
		if _, err := c.ListModels(context.Background(), provider); err == nil || !strings.Contains(err.Error(), "not supported") {
			t.Errorf("%s: %v", provider, err)
		}
	}
	if calls != 0 {
		t.Errorf("unexpected network calls: %d", calls)
	}
}

func TestListModelsLaterPageFailure(t *testing.T) {
	calls := 0
	c := New(Config{Provider: "anthropic", AnthropicAPIKey: "key", ModelsHTTPClient: &http.Client{Transport: rootRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"one"}],"has_more":true,"last_id":"one"}`))}, nil
		}
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader(`unavailable`))}, nil
	})}})
	if got, err := c.ListModels(context.Background(), ""); err == nil || got != nil || calls != 2 {
		t.Fatalf("got %#v, %v, calls=%d", got, err, calls)
	}
}

func TestListModelsValidation(t *testing.T) {
	for _, cfg := range []Config{
		{Provider: "openai"}, {Provider: "anthropic"}, {Provider: "gemini"},
		{Provider: "cloudflare", CloudflareAPIToken: "key"},
		{Provider: "cloudflare", CloudflareAccountID: "account"},
		{Provider: "openai_codex", OpenAIAPIKey: "key", CodexSubscription: &rootCredentialSource{}},
		{OpenAIAPIKey: "key", OpenAIAPIBase: "file:///models"},
		{OpenAIAPIKey: "key", OpenAIAPIBase: "https://user:password@example.com/v1"},
		{OpenAIAPIKey: "key", OpenAIAPIBase: "https://example.com/v1?key=secret"},
		{OpenAIAPIKey: "key", OpenAIAPIBase: "https://example.com/v1#fragment"},
	} {
		cfg.ModelsHTTPClient = &http.Client{Transport: rootRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			t.Fatal("invalid configuration made an HTTP request")
			return nil, nil
		})}
		if _, err := New(cfg).ListModels(context.Background(), ""); err == nil {
			t.Errorf("expected configuration error for provider %q", cfg.Provider)
		}
	}
}

func TestListModelsGeminiFallbackKeyAndEmptyList(t *testing.T) {
	c := New(Config{Provider: "gemini", OpenAIAPIKey: "fallback", ModelsHTTPClient: &http.Client{Transport: rootRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("x-goog-api-key") != "fallback" || r.URL.Query().Has("key") {
			t.Error("wrong Gemini authentication")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})}})
	got, err := c.ListModels(context.Background(), "")
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("got %#v, %v", got, err)
	}
}

func TestListModelsCloudflarePaginationMetadata(t *testing.T) {
	calls := 0
	c := New(Config{Provider: "cloudflare", CloudflareAccountID: "account", CloudflareAPIToken: "key", ModelsHTTPClient: &http.Client{Transport: rootRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls > 2 {
			t.Fatal("requested a page beyond total_pages")
		}
		body := fmt.Sprintf(`{"success":true,"result":[{"name":"@cf/model-%d"}],"result_info":{"total_pages":2}}`, calls)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}})
	got, err := c.ListModels(context.Background(), "")
	if err != nil || len(got) != 2 || calls != 2 {
		t.Fatalf("got %#v, %v, calls=%d", got, err, calls)
	}
}
