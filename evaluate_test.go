package uniai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/quailyquaily/uniai/chat"
	"github.com/quailyquaily/uniai/evaluate"
)

func evalPtr[T any](value T) *T { return &value }

type evalTransport func(*http.Request) (*http.Response, error)

func (f evalTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func evalRequest() evaluate.Request {
	return evaluate.Request{State: map[string]any{"text": "refund"}, Questions: map[string]evaluate.Question{"refund": {Kind: evaluate.Boolean, Instructions: "Refund requested?"}}}
}

const evalNativeResponse = `{"model":"jev-1.13.0","answers":{"refund":{"type":"noul","noul":0.7}},"usage":{"input_tokens":100,"output_tokens":1}}`

func TestEvaluateNativeDefaultsAndOverrides(t *testing.T) {
	calls := 0
	c := New(Config{Provider: "openai", OpenAIModel: "wrong-chat-model", TypeSafeAPIKey: "key", TypeSafeAPIBase: "https://example.test/v1", EvaluateModel: "jev-latest", EvaluateEmulationMode: evaluate.EmulationForce, EvaluateEmulationOptions: &evaluate.EmulationOptions{MaxTokens: evalPtr(64)}, EvaluateHTTPClient: &http.Client{Transport: evalTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		var payload struct{ Model string }
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.Model != "jev-request" || r.URL.Path != "/v1/systemone" {
			t.Fatalf("wrong target: %s %#v", r.URL, payload)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(evalNativeResponse))}, nil
	})}})
	req := evalRequest()
	req.Model = "jev-request"
	req.EmulationMode = evaluate.EmulationOff
	before, _ := json.Marshal(req)
	out, err := c.Evaluate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if out.Provider != "typesafe" || out.Emulated || calls != 1 {
		t.Fatal(out)
	}
	after, _ := json.Marshal(req)
	if string(before) != string(after) {
		t.Fatal("request mutated")
	}
	if view := c.GetConfig(); view.Provider != "openai" || view.Model != "wrong-chat-model" {
		t.Fatal("chat configuration changed")
	}
}

func TestEvaluateNativeFailuresDoNotFallback(t *testing.T) {
	for _, status := range []int{401, 429, 200} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			c := New(Config{TypeSafeAPIKey: "key", EvaluateModel: "jev-latest", EvaluateEmulationMode: evaluate.EmulationFallback, EvaluateHTTPClient: &http.Client{Transport: evalTransport(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
			})}})
			out, err := c.Evaluate(context.Background(), evalRequest())
			if out != nil || err == nil || calls != 1 {
				t.Fatalf("out=%v err=%v calls=%d", out, err, calls)
			}
		})
	}
}

func TestEvaluatePreflight(t *testing.T) {
	c := New(Config{TypeSafeAPIKey: "key", OpenAIModel: "not-evaluate-default"})
	if _, err := c.Evaluate(context.Background(), evalRequest()); !errors.Is(err, evaluate.ErrInvalidRequest) {
		t.Fatal(err)
	}
	c = New(Config{EvaluateModel: "jev-latest"})
	if _, err := c.Evaluate(context.Background(), evalRequest()); !errors.Is(err, evaluate.ErrInvalidRequest) {
		t.Fatal(err)
	}
	c = New(Config{TypeSafeAPIKey: "key", EvaluateModel: "jev-latest"})
	for _, mutate := range []func(*evaluate.Request){
		func(r *evaluate.Request) { r.Provider = "unknown" },
		func(r *evaluate.Request) { r.Provider = "openai"; r.EmulationMode = evaluate.EmulationOff },
		func(r *evaluate.Request) { r.EmulationMode = evaluate.EmulationForce },
		func(r *evaluate.Request) { r.EmulationOptions = &evaluate.EmulationOptions{} },
	} {
		req := evalRequest()
		mutate(&req)
		if out, err := c.Evaluate(context.Background(), req); out != nil || !errors.Is(err, evaluate.ErrUnsupported) {
			t.Fatalf("out=%v err=%v", out, err)
		}
	}
}

func TestEvaluateConfigCopiesEmulationOptions(t *testing.T) {
	options := &evaluate.EmulationOptions{MaxTokens: evalPtr(128), ReasoningEffort: evalPtr(chat.ReasoningEffortLow)}
	c := New(Config{EvaluateEmulationOptions: options})
	*options.MaxTokens = 1
	*options.ReasoningEffort = chat.ReasoningEffortHigh
	if *c.cfg.EvaluateEmulationOptions.MaxTokens != 128 || *c.cfg.EvaluateEmulationOptions.ReasoningEffort != chat.ReasoningEffortLow {
		t.Fatal("config pointers were aliased")
	}
}

func TestEvaluateNativePreservesUsageOnError(t *testing.T) {
	for _, partial := range []bool{false, true} {
		payload := strings.Replace(evalNativeResponse, `"noul":0.7`, `"noul":2`, 1)
		if partial {
			payload = strings.Replace(payload, `,"output_tokens":1`, "", 1)
		}
		c := New(Config{TypeSafeAPIKey: "key", EvaluateModel: "jev-latest", EvaluateHTTPClient: &http.Client{Transport: evalTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(payload))}, nil
		})}})
		out, err := c.Evaluate(context.Background(), evalRequest())
		if !errors.Is(err, evaluate.ErrInvalidResponse) || out == nil || len(out.Answers) != 0 || out.Model != "jev-1.13.0" {
			t.Fatalf("out=%+v err=%v", out, err)
		}
		if out.Usage == nil || out.Usage.InputTokens == nil || *out.Usage.InputTokens != 100 {
			t.Fatalf("lost usage: %+v", out.Usage)
		}
		if partial {
			if out.Usage.OutputTokens != nil || out.Usage.TotalTokens != nil || out.Usage.Cost != nil {
				t.Fatalf("invented usage/cost: %+v", out.Usage)
			}
		} else if out.Usage.TotalTokens == nil || *out.Usage.TotalTokens != 101 || out.Usage.Cost == nil || out.Usage.Cost.Total != 0.0000042 {
			t.Fatalf("lost total/cost: %+v", out.Usage)
		}
	}
}
