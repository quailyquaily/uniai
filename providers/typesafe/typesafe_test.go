package typesafe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/quailyquaily/uniai/evaluate"
	"github.com/quailyquaily/uniai/internal/httputil"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type observedBody struct {
	io.Reader
	closed bool
}

func (b *observedBody) Close() error { b.closed = true; return nil }

const validResponse = `{"model":"jev-1.13.0","answers":{"refund":{"type":"noul","noul":0},"team":{"type":"choice","choice":"billing","probabilities":{"billing":0.67,"other":0.33},"confidence":0.34},"urgency":{"type":"score","score":0.4,"probabilities":{"0":0.6,"1":0.4},"confidence":0.2,"legend":{"0":"low","1":"high"}}},"usage":{"input_tokens":10,"output_tokens":0}}`

func request() *evaluate.Request {
	return &evaluate.Request{Model: "jev-latest", State: map[string]any{"message": "refund"}, Questions: map[string]evaluate.Question{
		"refund":  {Kind: evaluate.Boolean, Instructions: "Refund?", TrueDescription: map[string]any{"meaning": "yes"}},
		"team":    {Kind: evaluate.Choice, Instructions: "Team?", Options: map[string]any{"billing": nil, "other": "Other"}},
		"urgency": {Kind: evaluate.Score, Instructions: "Urgency?", Levels: []any{"low", "high"}},
	}}
}

func TestEvaluateHTTPMapping(t *testing.T) {
	calls := 0
	body := &observedBody{Reader: strings.NewReader(validResponse)}
	client := &http.Client{Timeout: 7 * time.Second, Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://service.example/v1/systemone" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Fatal("content type")
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["model"] != "jev-latest" || payload["state"].(map[string]any)["message"] != "refund" {
			t.Fatalf("payload: %#v", payload)
		}
		questions := payload["questions"].(map[string]any)
		if len(questions) != 3 || questions["refund"].(map[string]any)["type"] != "noul" {
			t.Fatal(questions)
		}
		if questions["refund"].(map[string]any)["criteria"].(map[string]any)["true"].(map[string]any)["meaning"] != "yes" {
			t.Fatal(questions)
		}
		if v, ok := questions["team"].(map[string]any)["criteria"].(map[string]any)["billing"]; !ok || v != nil {
			t.Fatal(questions)
		}
		return &http.Response{StatusCode: 200, Body: body, Header: make(http.Header)}, nil
	})}
	p, err := New(Config{APIKey: "test-key", BaseURL: "https://service.example/v1/", HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	req := request()
	before, _ := json.Marshal(req)
	out, err := p.Evaluate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !body.closed || client.Timeout != 7*time.Second {
		t.Fatal("request lifecycle changed")
	}
	after, _ := json.Marshal(req)
	if !bytes.Equal(before, after) {
		t.Fatal("request mutated")
	}
	if out.Provider != "typesafe" || out.Model != "jev-1.13.0" || out.Emulated || string(out.Raw) != validResponse {
		t.Fatalf("result: %#v", out)
	}
	if *out.Answers["refund"].ProbabilityTrue != 0 || out.Answers["refund"].BooleanValue != nil || *out.Answers["urgency"].ScoreValue != 0.4 {
		t.Fatal(out.Answers)
	}
	if out.Usage.Cost != nil || *out.Usage.TotalTokens != 10 || *out.Usage.OutputTokens != 0 {
		t.Fatal(out.Usage)
	}
	if !strings.Contains(string(out.ProviderMetadata["typesafe"]), `"confidence":0.34`) {
		t.Fatal(out.ProviderMetadata)
	}
}

func TestEvaluateRejectsInvalidResponses(t *testing.T) {
	cases := map[string]string{
		"invalid JSON": "{", "null": "null", "trailing": validResponse + `{}`,
		"missing model":        strings.Replace(validResponse, `"model":"jev-1.13.0",`, "", 1),
		"missing usage":        strings.Replace(validResponse, `,"usage":{"input_tokens":10,"output_tokens":0}`, "", 1),
		"null probability":     strings.Replace(validResponse, `"billing":0.67`, `"billing":null`, 1),
		"missing distribution": strings.Replace(validResponse, `"probabilities":{"billing":0.67,"other":0.33},`, "", 1),
		"sum":                  strings.Replace(validResponse, `"other":0.33`, `"other":0.9`, 1),
		"confidence":           strings.Replace(validResponse, `"confidence":0.34`, `"confidence":2`, 1),
		"missing confidence":   strings.Replace(validResponse, `,"confidence":0.34`, "", 1),
		"missing noul":         strings.Replace(validResponse, `,"noul":0`, "", 1),
		"unknown answer":       strings.Replace(validResponse, `"refund":{"type"`, `"other_refund":{"type"`, 1),
		"type":                 strings.Replace(validResponse, `"type":"noul"`, `"type":"choice"`, 1),
		"legend":               strings.Replace(validResponse, `"legend":{"0":"low","1":"high"}`, `"legend":{"0":"low"}`, 1),
		"negative usage":       strings.Replace(validResponse, `"input_tokens":10`, `"input_tokens":-1`, 1),
		"foreign value":        strings.Replace(validResponse, `"noul":0`, `"noul":0,"score":0`, 1),
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			calls := 0
			body := &observedBody{Reader: strings.NewReader(payload)}
			p, _ := New(Config{APIKey: "key", HTTPClient: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 200, Body: body}, nil
			})}})
			out, err := p.Evaluate(context.Background(), request())
			if (out != nil && len(out.Answers) != 0) || !errors.Is(err, evaluate.ErrInvalidResponse) || calls != 1 || !body.closed {
				t.Fatalf("out=%#v err=%v calls=%d", out, err, calls)
			}
		})
	}
}

func TestEvaluateRoundingAndPartialUsage(t *testing.T) {
	payload := strings.Replace(validResponse, `"billing":0.67,"other":0.33`, `"billing":0.67,"other":0.34`, 1)
	payload = strings.Replace(payload, `,"output_tokens":0`, "", 1)
	p, _ := New(Config{APIKey: "key", HTTPClient: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(payload))}, nil
	})}})
	out, err := p.Evaluate(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	if out.Answers["team"].Probabilities["other"] != 0.34 || out.Usage.OutputTokens != nil || out.Usage.TotalTokens != nil {
		t.Fatal(out)
	}
}

func TestEvaluatePreflightAndErrors(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("missing key accepted")
	}
	for _, base := range []string{"ftp://example.test", "not a URL", "https://user:password@example.test/v1", "https://example.test/v1?key=value"} {
		if _, err := New(Config{APIKey: "key", BaseURL: base}); err == nil {
			t.Fatalf("accepted %s", base)
		}
	}
	calls := 0
	p, _ := New(Config{APIKey: "key", HTTPClient: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { calls++; return nil, fmt.Errorf("unexpected call") })}})
	for _, change := range []func(*evaluate.Request){
		func(r *evaluate.Request) { r.Model = "" }, func(r *evaluate.Request) { r.Provider = "other" }, func(r *evaluate.Request) { r.EmulationMode = evaluate.EmulationForce }, func(r *evaluate.Request) { r.EmulationOptions = &evaluate.EmulationOptions{} }, func(r *evaluate.Request) { r.InferenceProvider = "gateway" },
		func(r *evaluate.Request) {
			q := r.Questions["urgency"]
			q.Levels = make([]any, 11)
			for i := range q.Levels {
				q.Levels[i] = "level"
			}
			r.Questions["urgency"] = q
		},
		func(r *evaluate.Request) {
			for i := 0; i < 256; i++ {
				r.Questions["team"].Options[fmt.Sprint(i)] = nil
			}
		},
	} {
		r := request()
		change(r)
		if out, err := p.Evaluate(context.Background(), r); out != nil || err == nil {
			t.Fatal("invalid request accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Evaluate(ctx, request()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("preflight made %d calls", calls)
	}

	var logs bytes.Buffer
	prior := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(prior)
	for _, status := range []int{401, 422, 429, 529} {
		body := &observedBody{Reader: strings.NewReader(`{"error":"sensitive response"}`)}
		p, _ := New(Config{APIKey: "secret-credential", Debug: true, HTTPClient: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": []string{"12"}}, Body: body}, nil
		})}})
		_, err := p.Evaluate(context.Background(), request())
		var apiErr *evaluate.APIError
		if !errors.As(err, &apiErr) || apiErr.StatusCode != status || apiErr.RetryAfter != "12" || !body.closed || strings.Contains(err.Error(), "sensitive") {
			t.Fatal(err)
		}
	}
	if calls != 4 || strings.Contains(logs.String(), "secret-credential") {
		t.Fatal("retry or credential leak")
	}
}

type repeatingReader struct{}

func (repeatingReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	return len(p), nil
}

func TestEvaluateBodyLimitAndCancellation(t *testing.T) {
	body := &observedBody{Reader: io.LimitReader(repeatingReader{}, httputil.MaxResponseBodySize+1)}
	p, _ := New(Config{APIKey: "key", HTTPClient: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { return &http.Response{StatusCode: 200, Body: body}, nil })}})
	if out, err := p.Evaluate(context.Background(), request()); out != nil || err == nil || !body.closed {
		t.Fatalf("out=%v err=%v closed=%v", out, err, body.closed)
	}
	ctx, cancel := context.WithCancel(context.Background())
	p, _ = New(Config{APIKey: "key", HTTPClient: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) { cancel(); return nil, r.Context().Err() })}})
	if _, err := p.Evaluate(ctx, request()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestEvaluatePreservesUsageOnInvalidAnswers(t *testing.T) {
	for _, payload := range []string{
		strings.Replace(validResponse, `"type":"noul"`, `"type":"unknown"`, 1),
		strings.Replace(validResponse, `"noul":0`, `"noul":2`, 1),
		strings.Replace(validResponse, `"noul":0`, `"noul":"invalid"`, 1),
		strings.Replace(validResponse, `"model":"jev-1.13.0"`, `"model":""`, 1),
	} {
		p, _ := New(Config{APIKey: "key", HTTPClient: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(payload))}, nil
		})}})
		out, err := p.Evaluate(context.Background(), request())
		if !errors.Is(err, evaluate.ErrInvalidResponse) || out == nil || len(out.Answers) != 0 || out.Provider != "typesafe" || string(out.Raw) != payload {
			t.Fatalf("out=%+v err=%v", out, err)
		}
		if out.Usage == nil || out.Usage.InputTokens == nil || *out.Usage.InputTokens != 10 || out.Usage.OutputTokens == nil || *out.Usage.OutputTokens != 0 || out.Usage.TotalTokens == nil || *out.Usage.TotalTokens != 10 {
			t.Fatalf("lost usage: %+v", out.Usage)
		}
	}
}
