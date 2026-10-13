package openaidecisions

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/quailyquaily/uniai/evaluate"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type observedBody struct {
	io.Reader
	closed bool
}

func (b *observedBody) Close() error { b.closed = true; return nil }

const validResponse = `{"model":"gpt-6-luna","answers":[{"name":"refund","type":"predicate","probability":0},{"name":"team","type":"choice","choice":"billing","confidence":0.8,"probabilities":[{"value":"billing","probability":0.9},{"value":"other","probability":0.1}]},{"name":"urgency","type":"score","score":0.4,"confidence":0.2,"probabilities":[{"value":0,"label":"0","probability":0.6},{"value":1,"label":"1","probability":0.4}]}],"usage":{"input_tokens":10,"output_tokens":0}}`

func request() *evaluate.Request {
	return &evaluate.Request{Provider: "openai", Model: "gpt-6-luna", State: map[string]any{"message": "refund"}, Questions: map[string]evaluate.Question{
		"refund":  {Kind: evaluate.Boolean, Instructions: "Refund?", TrueDescription: map[string]any{"meaning": "yes"}, FalseDescription: "no"},
		"team":    {Kind: evaluate.Choice, Instructions: map[string]any{"task": "Team?"}, Options: map[string]any{"billing": nil, "other": "null"}},
		"urgency": {Kind: evaluate.Score, Instructions: "Urgency?", Levels: []any{"low", map[string]any{"impact": "high"}}},
	}}
}

func TestEvaluateMapping(t *testing.T) {
	body := &observedBody{Reader: strings.NewReader(validResponse)}
	calls := 0
	p, err := New(Config{APIKey: "test-key", BaseURL: "https://service.example/v1/", HTTPClient: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://service.example/v1/decisions" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL)
		}
		var payload struct {
			Model     string `json:"model"`
			Input     string `json:"input"`
			Questions []struct {
				Name, Type, Instructions string
				Choices                  []struct {
					Value       string
					Description *string
				}
				Levels []struct{ Label, Description string }
			} `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.Model != "gpt-6-luna" || payload.Input != `{"message":"refund"}` || len(payload.Questions) != 3 {
			t.Fatalf("payload: %+v", payload)
		}
		qs := payload.Questions
		if qs[0].Name != "refund" || qs[0].Type != "predicate" || qs[0].Instructions != "Refund?\nTrue criteria: {\"meaning\":\"yes\"}\nFalse criteria: no" {
			t.Fatal(qs[0])
		}
		if qs[1].Type != "choice" || qs[1].Instructions != `{"task":"Team?"}` || len(qs[1].Choices) != 2 || qs[1].Choices[0].Value != "billing" || qs[1].Choices[0].Description != nil || qs[1].Choices[1].Description == nil || *qs[1].Choices[1].Description != "null" {
			t.Fatal(qs[1])
		}
		if qs[2].Type != "score" || len(qs[2].Levels) != 2 || qs[2].Levels[0].Label != "0" || qs[2].Levels[0].Description != "low" || qs[2].Levels[1].Description != `{"impact":"high"}` {
			t.Fatal(qs[2])
		}
		return &http.Response{StatusCode: 200, Body: body}, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	req := request()
	before, _ := json.Marshal(req)
	out, err := p.Evaluate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(req)
	if string(before) != string(after) || calls != 1 || !body.closed {
		t.Fatal("request mutation or lifecycle error")
	}
	if out.Provider != "openai" || out.Emulated || out.Model != "gpt-6-luna" || string(out.Raw) != validResponse {
		t.Fatal(out)
	}
	if *out.Answers["refund"].ProbabilityTrue != 0 || out.Answers["refund"].BooleanValue != nil || out.Answers["team"].Selected != "billing" || out.Answers["team"].Probabilities["billing"] != 0.9 || *out.Answers["urgency"].ScoreValue != 0.4 || out.Answers["urgency"].Probabilities["1"] != 0.4 {
		t.Fatal(out.Answers)
	}
	if *out.Usage.OutputTokens != 0 || *out.Usage.TotalTokens != 10 || !strings.Contains(string(out.ProviderMetadata["openai"]), `"confidence":0.8`) {
		t.Fatal(out)
	}
}

func TestEvaluateTextAndStructuredState(t *testing.T) {
	for _, tc := range []struct {
		state any
		want  string
	}{
		{"hello", "hello"}, {json.RawMessage(`"hello"`), "hello"},
		{[]any{"hello", 1}, `["hello",1]`},
		{[]any{map[string]any{"role": "user", "content": "hello"}}, `[{"content":"hello","role":"user"}]`},
	} {
		req := request()
		req.State = tc.state
		body, err := encodeRequest(req)
		if err != nil {
			t.Fatal(err)
		}
		var wire struct{ Input string }
		if err := json.Unmarshal(body, &wire); err != nil || wire.Input != tc.want {
			t.Fatalf("input=%q err=%v", wire.Input, err)
		}
	}
}

func TestEvaluateRejectsInvalidAnswers(t *testing.T) {
	for _, tc := range []struct{ name, from, to string }{
		{"refusal", `"type":"predicate","probability":0`, `"type":"refusal"`},
		{"missing-name", `"name":"refund"`, `"name":null`},
		{"duplicate-answer", `"name":"team"`, `"name":"refund"`},
		{"unknown-answer", `"name":"refund"`, `"name":"unknown"`},
		{"missing-answer", `{"name":"refund","type":"predicate","probability":0},`, ``},
		{"wrong-kind", `"type":"predicate","probability":0`, `"type":"score","probability":0`},
		{"missing-probability", `"probability":0}`, `"probability":null}`},
		{"invalid-probability", `"probability":0}`, `"probability":2}`},
		{"invalid-choice", `"choice":"billing"`, `"choice":"unknown"`},
		{"boolean-choice", `"choice":"billing"`, `"choice":true`},
		{"invalid-confidence", `"confidence":0.8`, `"confidence":2`},
		{"missing-confidence", `"confidence":0.8`, `"confidence":null`},
		{"duplicate-option", `"value":"other"`, `"value":"billing"`},
		{"missing-option", `,{"value":"other","probability":0.1}`, ``},
		{"null-probability", `"probability":0.9`, `"probability":null`},
		{"invalid-sum", `"probability":0.9`, `"probability":0.7`},
		{"null-index", `"value":0`, `"value":null`},
		{"string-index", `"value":0`, `"value":"0"`},
		{"unknown-index", `"value":0`, `"value":2`},
		{"missing-label", `"label":"0"`, `"label":null`},
		{"invalid-score", `"score":0.4`, `"score":2`},
		{"missing-score", `"score":0.4`, `"score":null`},
		{"missing-model", `"model":"gpt-6-luna"`, `"model":""`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := strings.Replace(validResponse, tc.from, tc.to, 1)
			p, err := New(Config{APIKey: "key", HTTPClient: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(payload))}, nil
			})}})
			if err != nil {
				t.Fatal(err)
			}
			out, err := p.Evaluate(context.Background(), request())
			if !errors.Is(err, evaluate.ErrInvalidResponse) || out == nil || out.Answers != nil || string(out.Raw) != payload || out.Usage == nil || *out.Usage.InputTokens != 10 {
				t.Fatalf("out=%+v err=%v", out, err)
			}
		})
	}
}

func TestEvaluatePartialUsage(t *testing.T) {
	body := strings.Replace(validResponse, `,"output_tokens":0`, "", 1)
	out, err := decodeResponse(request(), []byte(body))
	if err != nil || out.Usage.OutputTokens != nil || out.Usage.TotalTokens != nil {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	for _, body := range []string{`{`, `null`, strings.Replace(validResponse, `"input_tokens":10`, `"input_tokens":-1`, 1)} {
		if _, err := decodeResponse(request(), []byte(body)); err == nil {
			t.Fatal("invalid usage accepted")
		}
	}
}

func TestEvaluatePreflightAndHTTPFailures(t *testing.T) {
	for _, cfg := range []Config{{}, {APIKey: " "}, {APIKey: "key", BaseURL: "file:///v1"}, {APIKey: "key", BaseURL: "https://user:pass@example.com/v1"}, {APIKey: "key", BaseURL: "https://example.com/v1?q=1"}} {
		if _, err := New(cfg); !errors.Is(err, evaluate.ErrInvalidRequest) {
			t.Fatal(err)
		}
	}
	calls := 0
	body := &observedBody{Reader: strings.NewReader(`{"error":"private"}`)}
	p, err := New(Config{APIKey: "key", HTTPClient: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{"3"}}, Body: body}, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*evaluate.Request){
		func(r *evaluate.Request) { r.Provider = "other" },
		func(r *evaluate.Request) { r.EmulationMode = evaluate.EmulationForce },
		func(r *evaluate.Request) { r.EmulationOptions = &evaluate.EmulationOptions{} },
		func(r *evaluate.Request) { r.InferenceProvider = "gateway" },
	} {
		r := request()
		mutate(r)
		if _, err := p.Evaluate(context.Background(), r); !errors.Is(err, evaluate.ErrUnsupported) {
			t.Fatal(err)
		}
	}
	if _, err := p.Evaluate(context.Background(), nil); !errors.Is(err, evaluate.ErrInvalidRequest) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Evaluate(ctx, request()); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
	_, err = p.Evaluate(context.Background(), request())
	var apiErr *evaluate.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 429 || apiErr.Provider != "openai" || apiErr.RetryAfter != "3" || string(apiErr.Body) != `{"error":"private"}` || strings.Contains(err.Error(), "private") || calls != 1 || !body.closed {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}
