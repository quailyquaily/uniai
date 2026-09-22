package uniai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/quailyquaily/uniai/chat"
	"github.com/quailyquaily/uniai/evaluate"
)

func emulationRequest() evaluate.Request {
	r := evalRequest()
	r.Provider = "openai"
	r.Model = "Qwen/example"
	r.EmulationMode = evaluate.EmulationForce
	r.Questions["team"] = evaluate.Question{Kind: evaluate.Choice, Instructions: map[string]any{"question": "Which team?"}, Options: map[string]any{"billing": "Payments", "other": nil}}
	r.Questions["urgency"] = evaluate.Question{Kind: evaluate.Score, Instructions: "Urgency?", Levels: []any{"low", "medium", "high"}}
	return r
}

const emulatedJSON = `{"refund":false,"team":"billing","urgency":0}`

func TestBuildEvaluateChatRequest(t *testing.T) {
	r := emulationRequest()
	r.InferenceProvider = "local"
	r.EmulationOptions = &evaluate.EmulationOptions{MaxTokens: evalPtr(128), ReasoningEffort: evalPtr(chat.ReasoningEffortLow)}
	q := r.Questions["refund"]
	q.TrueDescription = map[string]any{"meaning": "explicit request"}
	q.FalseDescription = "no request"
	r.Questions["refund"] = q
	before, _ := json.Marshal(r)
	got, err := buildEvaluateChatRequest(&r)
	if err != nil {
		t.Fatal(err)
	}
	if got.Provider != r.Provider || got.Model != r.Model || got.InferenceProvider != "local" || *got.Options.MaxTokens != 128 || *got.Options.ReasoningEffort != chat.ReasoningEffortLow {
		t.Fatalf("request=%#v", got)
	}
	if len(got.Tools) != 0 || got.ToolChoice != nil || got.Options.OnStream != nil || got.Options.ToolsEmulationMode != chat.ToolsEmulationOff {
		t.Fatal("unexpected tools or streaming")
	}
	if len(got.Messages) != 2 || got.Messages[0].Role != chat.RoleSystem || got.Messages[1].Role != chat.RoleUser {
		t.Fatal(got.Messages)
	}
	var state any
	if err := json.Unmarshal([]byte(got.Messages[1].Content), &state); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(state, r.State) {
		t.Fatal("state changed")
	}
	for _, s := range []string{`"additionalProperties":false`, `"required":["refund","team","urgency"]`, `"enum":["billing","other"]`, `"enum":[0,1,2]`, "explicit request", "no request", "Which team?"} {
		if !strings.Contains(got.Messages[0].Content, s) {
			t.Fatalf("missing %q in prompt", s)
		}
	}
	again, err := buildEvaluateChatRequest(&r)
	if err != nil || !reflect.DeepEqual(got.Messages, again.Messages) {
		t.Fatal("nondeterministic prompt")
	}
	after, _ := json.Marshal(r)
	if string(before) != string(after) {
		t.Fatal("request mutated")
	}
}

func TestParseEvaluateEmulation(t *testing.T) {
	r := emulationRequest()
	for _, text := range []string{emulatedJSON, "```json\n" + emulatedJSON + "\n```", " \n" + emulatedJSON + "\n"} {
		out, err := parseEvaluateChatResponse(&r, &chat.Result{Text: text, FinishReason: "stop", Model: "served-version"})
		if err != nil {
			t.Fatal(err)
		}
		if !out.Emulated || out.Model != "served-version" || *out.Answers["refund"].BooleanValue || out.Answers["refund"].ProbabilityTrue != nil || out.Answers["team"].Probabilities != nil || *out.Answers["urgency"].ScoreValue != 0 {
			t.Fatal(out)
		}
		if string(out.Raw) != emulatedJSON {
			t.Fatalf("raw=%s", out.Raw)
		}
	}
	for _, text := range []string{"", "null", "[]", "I cannot answer", `{"refund":false}`, `{"refund":null,"team":"billing","urgency":0}`, `{"refund":"false","team":"billing","urgency":0}`, `{"refund":false,"team":"missing","urgency":0}`, `{"refund":false,"team":"billing","urgency":0.5}`, `{"refund":false,"team":"billing","urgency":3}`, `{"refund":false,"team":"billing","urgency":-1}`, `{"refund":false,"team":"billing","urgency":0,"confidence":1}`, `{"refund":false,"refund":true,"team":"billing","urgency":0}`, `{"refund":false,"re\u0066und":true,"team":"billing","urgency":0}`, emulatedJSON + emulatedJSON, "Here is the answer: " + emulatedJSON, `<think>reasoning</think>` + emulatedJSON, emulatedJSON[:len(emulatedJSON)-1]} {
		if out, err := parseEvaluateChatResponse(&r, &chat.Result{Text: text}); out == nil || len(out.Answers) != 0 || !errors.Is(err, evaluate.ErrInvalidResponse) {
			t.Fatalf("accepted %q: out=%v err=%v", text, out, err)
		}
	}
	for _, reason := range []string{"length", "content_filter", "tool_calls", "refusal"} {
		if out, err := parseEvaluateChatResponse(&r, &chat.Result{Text: emulatedJSON, FinishReason: reason}); out == nil || len(out.Answers) != 0 || !errors.Is(err, evaluate.ErrInvalidResponse) {
			t.Fatalf("reason=%s err=%v", reason, err)
		}
	}
	if _, err := parseEvaluateChatResponse(&r, &chat.Result{Text: emulatedJSON, ToolCalls: []chat.ToolCall{{ID: "unexpected"}}}); !errors.Is(err, evaluate.ErrInvalidResponse) {
		t.Fatal(err)
	}
	if _, err := parseEvaluateChatResponse(&r, nil); !errors.Is(err, evaluate.ErrInvalidResponse) {
		t.Fatal(err)
	}
	out, err := parseEvaluateChatResponse(&r, &chat.Result{Text: emulatedJSON})
	if err != nil || out.Model != "" {
		t.Fatalf("model alias substituted: %v %v", out, err)
	}
}

func TestEvaluateEmulationRoutesAndOptions(t *testing.T) {
	for _, mode := range []evaluate.EmulationMode{evaluate.EmulationOff, evaluate.EmulationFallback, evaluate.EmulationForce} {
		t.Run(string(mode), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/v1/chat/completions" || r.Header.Get("X-Chat") != "test" {
					t.Errorf("wrong request %s", r.URL)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["model"] != "Qwen/example" || body["max_tokens"] != float64(128) || body["reasoning_effort"] != "none" {
					t.Errorf("parameters=%#v", body)
				}
				for _, key := range []string{"tools", "tool_choice", "response_format"} {
					if _, ok := body[key]; ok {
						t.Errorf("unexpected %s", key)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"model": "served-qwen", "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": emulatedJSON, "reasoning_content": "private thought"}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 100, "completion_tokens": 10, "total_tokens": 110, "prompt_tokens_details": map[string]any{"cached_tokens": 20}}})
			}))
			defer server.Close()
			cached := 0.5
			c := New(Config{Provider: "wrong", OpenAIAPIKey: "key", OpenAIAPIBase: server.URL + "/v1", OpenAIModel: "wrong-model", EvaluateProvider: "openai", EvaluateModel: "Qwen/example", EvaluateEmulationMode: mode, EvaluateEmulationOptions: &evaluate.EmulationOptions{ReasoningEffort: evalPtr(chat.ReasoningEffortHigh), MaxTokens: evalPtr(128)}, ChatHeaders: map[string]string{"X-Chat": "test"}, EvaluateHTTPClient: &http.Client{Transport: evalTransport(func(*http.Request) (*http.Response, error) {
				t.Error("used native HTTP client for Chat")
				return nil, errors.New("unexpected native call")
			})}, Pricing: &PricingCatalog{Chat: []ChatPricingRule{{InferenceProvider: "self-hosted", Model: "served-qwen", InputUSDPerMillion: 1, OutputUSDPerMillion: 2, CachedInputUSDPerMillion: &cached}}, Evaluate: []EvaluationPricingRule{{InferenceProvider: "openai", Model: "served-qwen", InputUSDPerMillion: 999, OutputUSDPerMillion: 999}}}})
			r := emulationRequest()
			r.Provider = ""
			r.Model = ""
			r.EmulationMode = ""
			r.InferenceProvider = "self-hosted"
			r.EmulationOptions = &evaluate.EmulationOptions{ReasoningEffort: evalPtr(chat.ReasoningEffortNone)}
			before, _ := json.Marshal(r)
			out, err := c.Evaluate(context.Background(), r)
			if mode == evaluate.EmulationOff {
				if !errors.Is(err, evaluate.ErrUnsupported) || calls != 0 {
					t.Fatalf("err=%v calls=%d", err, calls)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if calls != 1 || !out.Emulated || out.Provider != "openai" || *out.Usage.InputTokens != 100 || *out.Usage.OutputTokens != 10 || out.Usage.Cost == nil || out.Usage.Cost.Total != 0.00011 {
				t.Fatalf("out=%#v usage=%#v calls=%d", out, out.Usage, calls)
			}
			if !strings.Contains(string(out.ProviderMetadata["openai"]), `"cached_input_tokens":20`) {
				t.Fatal(out.ProviderMetadata)
			}
			after, _ := json.Marshal(r)
			if string(before) != string(after) {
				t.Fatal("request mutated")
			}
			if *c.cfg.EvaluateEmulationOptions.ReasoningEffort != chat.ReasoningEffortHigh {
				t.Fatal("defaults mutated")
			}
		})
	}
}

func TestEvaluateEmulationScoreMustBeInteger(t *testing.T) {
	r := emulationRequest()
	for _, value := range []string{"0.00000000000000000001", "1.00000000000000000001", "1e-999", "0.5"} {
		text := strings.Replace(emulatedJSON, `"urgency":0`, `"urgency":`+value, 1)
		if out, err := parseEvaluateChatResponse(&r, &chat.Result{Text: text}); out == nil || len(out.Answers) != 0 || !errors.Is(err, evaluate.ErrInvalidResponse) {
			t.Fatalf("fractional score %s accepted: %v %v", value, out, err)
		}
	}
}

func TestEvaluateEmulationUnsetControls(t *testing.T) {
	r := emulationRequest()
	out, err := buildEvaluateChatRequest(&r)
	if err != nil {
		t.Fatal(err)
	}
	if out.Options.ReasoningEffort != nil || out.Options.MaxTokens != nil {
		t.Fatal("unset controls were synthesized")
	}
}

func TestEvaluateEmulationNoCorrectionRequests(t *testing.T) {
	for _, response := range []string{`{"choices":[{"message":{"content":"incomplete"},"finish_reason":"stop"}]}`, `{"choices":[{"message":{"content":"{\"refund\":false}"},"finish_reason":"length"}]}`} {
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, response)
		}))
		c := New(Config{OpenAIAPIKey: "key", OpenAIAPIBase: server.URL + "/v1"})
		out, err := c.Evaluate(context.Background(), emulationRequest())
		server.Close()
		if out == nil || len(out.Answers) != 0 || !errors.Is(err, evaluate.ErrInvalidResponse) || calls != 1 {
			t.Fatalf("out=%v err=%v calls=%d", out, err, calls)
		}
	}
}

func TestEvaluateEmulationRejectsIgnoredControls(t *testing.T) {
	for _, provider := range []string{"openai_codex", "azure", "cloudflare"} {
		r := emulationRequest()
		r.Provider = provider
		r.EmulationOptions = &evaluate.EmulationOptions{}
		if provider == "openai_codex" {
			r.EmulationOptions.MaxTokens = evalPtr(128)
		} else {
			r.EmulationOptions.ReasoningEffort = evalPtr(chat.ReasoningEffortLow)
		}
		if _, err := New(Config{}).Evaluate(context.Background(), r); !errors.Is(err, evaluate.ErrUnsupported) {
			t.Fatalf("provider=%s err=%v", provider, err)
		}
	}
	r := emulationRequest()
	r.EmulationOptions = &evaluate.EmulationOptions{MaxTokens: evalPtr(-1)}
	if _, err := New(Config{}).Evaluate(context.Background(), r); !errors.Is(err, evaluate.ErrInvalidRequest) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New(Config{}).Evaluate(ctx, emulationRequest()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestEvaluateAzureUsesRequestedDeployment(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if !strings.Contains(r.URL.Path, "requested-deployment") {
			t.Errorf("wrong deployment: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": emulatedJSON}, "finish_reason": "stop"}}})
	}))
	defer server.Close()
	c := New(Config{AzureOpenAIAPIKey: "key", AzureOpenAIEndpoint: server.URL, AzureOpenAIModel: "chat-deployment"})
	r := emulationRequest()
	r.Provider = "azure"
	r.Model = "requested-deployment"
	if _, err := c.Evaluate(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || c.cfg.AzureOpenAIModel != "chat-deployment" {
		t.Fatal("deployment/default changed")
	}
}

func TestEvaluateEmulationPreservesUsageOnError(t *testing.T) {
	for _, tc := range []struct{ name, content, reason string }{
		{"invalid JSON", "incomplete", "stop"},
		{"truncated", emulatedJSON, "length"},
		{"refused", "", "refusal"},
		{"invalid answer", strings.Replace(emulatedJSON, "billing", "unknown", 1), "stop"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{
					"model":   "served-model",
					"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": tc.content}, "finish_reason": tc.reason}},
					"usage":   map[string]any{"prompt_tokens": 100, "completion_tokens": 10, "total_tokens": 110, "prompt_tokens_details": map[string]any{"cached_tokens": 20}},
				})
			}))
			defer server.Close()
			c := New(Config{OpenAIAPIKey: "key", OpenAIAPIBase: server.URL + "/v1", Pricing: &PricingCatalog{Chat: []ChatPricingRule{{InferenceProvider: "openai", Model: "served-model", InputUSDPerMillion: 1, OutputUSDPerMillion: 2, CachedInputUSDPerMillion: evalPtr(0.5)}}}})
			req := emulationRequest()
			req.InferenceProvider = "openai"
			out, err := c.Evaluate(context.Background(), req)
			if !errors.Is(err, evaluate.ErrInvalidResponse) || out == nil || calls != 1 {
				t.Fatalf("out=%+v err=%v calls=%d", out, err, calls)
			}
			if out.Provider != "openai" || out.Model != "served-model" || !out.Emulated || len(out.Answers) != 0 || string(out.Raw) != tc.content {
				t.Fatalf("invalid partial result: %+v", out)
			}
			if out.Usage == nil || out.Usage.InputTokens == nil || *out.Usage.InputTokens != 100 || out.Usage.OutputTokens == nil || *out.Usage.OutputTokens != 10 || out.Usage.TotalTokens == nil || *out.Usage.TotalTokens != 110 || out.Usage.Cost == nil || out.Usage.Cost.Total != 0.00011 {
				t.Fatalf("lost usage/cost: %+v", out.Usage)
			}
			if !strings.Contains(string(out.ProviderMetadata["openai"]), `"cached_input_tokens":20`) {
				t.Fatalf("lost cache metadata: %+v", out.ProviderMetadata)
			}
		})
	}
}
