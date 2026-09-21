package evaluate

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
)

func ptr[T any](v T) *T { return &v }

func sampleRequest() *Request {
	return &Request{Provider: "test", Model: "decision", State: map[string]any{"text": "refund", "count": 0}, Questions: map[string]Question{
		"refund":  {Kind: Boolean, Instructions: "Refund requested?"},
		"team":    {Kind: Choice, Instructions: map[string]any{"question": "Which team?"}, Options: map[string]any{"billing": nil, "support": "Other"}},
		"urgency": {Kind: Score, Instructions: "Urgency?", Levels: []any{"low", "high"}},
	}}
}

func sampleResult() *Result {
	return &Result{Answers: map[string]Answer{
		"refund":  {Kind: Boolean, BooleanValue: ptr(false)},
		"team":    {Kind: Choice, Selected: "billing"},
		"urgency": {Kind: Score, ScoreValue: ptr(0.0)},
	}}
}

func TestValidateRequest(t *testing.T) {
	for _, state := range []any{"", map[string]any{}, []any{}, json.RawMessage(`{"nested":[false,0,null]}`), struct{ Text string }{"hello"}} {
		r := sampleRequest()
		r.State = state
		before, _ := json.Marshal(r)
		if err := ValidateRequest(r); err != nil {
			t.Fatal(err)
		}
		after, _ := json.Marshal(r)
		if string(before) != string(after) {
			t.Fatal("request mutated")
		}
	}
	cases := []struct {
		name, path string
		change     func(*Request)
	}{
		{"provider", "provider", func(r *Request) { r.Provider = " " }},
		{"model", "model", func(r *Request) { r.Model = "" }},
		{"mode", "emulation_mode", func(r *Request) { r.EmulationMode = "auto" }},
		{"tokens", "max_tokens", func(r *Request) { r.EmulationOptions = &EmulationOptions{MaxTokens: ptr(0)} }},
		{"state null", "state", func(r *Request) { r.State = json.RawMessage(`null`) }},
		{"state scalar", "state", func(r *Request) { r.State = 1 }},
		{"state invalid", "state", func(r *Request) { r.State = math.NaN() }},
		{"state function", "state", func(r *Request) { r.State = func() {} }},
		{"questions", "questions", func(r *Request) { r.Questions = nil }},
		{"question id", "questions", func(r *Request) { r.Questions[""] = r.Questions["refund"] }},
		{"kind", "refund", func(r *Request) { q := r.Questions["refund"]; q.Kind = "other"; r.Questions["refund"] = q }},
		{"instructions", "refund", func(r *Request) { q := r.Questions["refund"]; q.Instructions = " \n"; r.Questions["refund"] = q }},
		{"cross fields", "refund", func(r *Request) { q := r.Questions["refund"]; q.Options = map[string]any{}; r.Questions["refund"] = q }},
		{"empty options", "team", func(r *Request) { q := r.Questions["team"]; q.Options = nil; r.Questions["team"] = q }},
		{"empty option id", "team", func(r *Request) { r.Questions["team"].Options[""] = nil }},
		{"description scalar", "team", func(r *Request) { r.Questions["team"].Options["billing"] = false }},
		{"score levels", "urgency", func(r *Request) { q := r.Questions["urgency"]; q.Levels = []any{"low"}; r.Questions["urgency"] = q }},
		{"score null", "urgency", func(r *Request) { r.Questions["urgency"].Levels[0] = json.RawMessage(`null`) }},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			r := sampleRequest()
			tt.change(r)
			err := ValidateRequest(r)
			if !errors.Is(err, ErrInvalidRequest) || !strings.Contains(err.Error(), tt.path) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	if !errors.Is(ValidateRequest(nil), ErrInvalidRequest) {
		t.Fatal("nil request accepted")
	}
	cycle := map[string]any{}
	cycle["self"] = cycle
	r := sampleRequest()
	r.State = cycle
	if !errors.Is(ValidateRequest(r), ErrInvalidRequest) {
		t.Fatal("cyclic state accepted")
	}
}

func TestValidateResult(t *testing.T) {
	r := sampleRequest()
	for _, boolean := range []Answer{{Kind: Boolean, BooleanValue: ptr(false)}, {Kind: Boolean, ProbabilityTrue: ptr(0.0)}, {Kind: Boolean, BooleanValue: ptr(true), ProbabilityTrue: ptr(0.2)}} {
		out := sampleResult()
		out.Answers["refund"] = boolean
		if err := ValidateResult(r, out); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name   string
		change func(*Result)
	}{
		{"missing answer", func(o *Result) { delete(o.Answers, "team") }},
		{"unknown answer", func(o *Result) { o.Answers["extra"] = o.Answers["refund"] }},
		{"kind mismatch", func(o *Result) { o.Answers["refund"] = Answer{Kind: Score, ScoreValue: ptr(0.0)} }},
		{"missing bool", func(o *Result) { o.Answers["refund"] = Answer{Kind: Boolean} }},
		{"foreign value", func(o *Result) {
			o.Answers["refund"] = Answer{Kind: Boolean, BooleanValue: ptr(false), ScoreValue: ptr(0.0)}
		}},
		{"probability range", func(o *Result) { o.Answers["refund"] = Answer{Kind: Boolean, ProbabilityTrue: ptr(1.1)} }},
		{"probability nan", func(o *Result) { o.Answers["refund"] = Answer{Kind: Boolean, ProbabilityTrue: ptr(math.NaN())} }},
		{"invalid selection", func(o *Result) { o.Answers["team"] = Answer{Kind: Choice, Selected: "other"} }},
		{"empty distribution", func(o *Result) {
			o.Answers["team"] = Answer{Kind: Choice, Selected: "billing", Probabilities: map[string]float64{}}
		}},
		{"wrong keys", func(o *Result) {
			o.Answers["team"] = Answer{Kind: Choice, Selected: "billing", Probabilities: map[string]float64{"billing": 1, "other": 0}}
		}},
		{"invalid distribution", func(o *Result) {
			o.Answers["team"] = Answer{Kind: Choice, Selected: "billing", Probabilities: map[string]float64{"billing": math.Inf(1), "support": 0}}
		}},
		{"score range", func(o *Result) { o.Answers["urgency"] = Answer{Kind: Score, ScoreValue: ptr(2.0)} }},
		{"score missing", func(o *Result) { o.Answers["urgency"] = Answer{Kind: Score} }},
		{"usage negative", func(o *Result) { o.Usage = &Usage{InputTokens: ptr(-1)} }},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			out := sampleResult()
			tt.change(out)
			if err := ValidateResult(r, out); !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	if !errors.Is(ValidateResult(r, nil), ErrInvalidResponse) {
		t.Fatal("nil result accepted")
	}
	out := sampleResult()
	out.Answers["urgency"] = Answer{Kind: Score, ScoreValue: ptr(0.4), Probabilities: map[string]float64{"0": 0.6, "1": 0.4}}
	if err := ValidateResult(r, out); err != nil {
		t.Fatal(err)
	}
}

func TestResultJSONPreservesZeroAndOmitsRaw(t *testing.T) {
	out := sampleResult()
	out.Raw = json.RawMessage(`{"private":true}`)
	out.Usage = &Usage{InputTokens: ptr(0)}
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"boolean_value":false`, `"score_value":0`, `"input_tokens":0`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("missing %s in %s", want, data)
		}
	}
	if strings.Contains(string(data), "private") || strings.Contains(string(data), "probability_true") || strings.Contains(string(data), "output_tokens") {
		t.Fatalf("unexpected fields: %s", data)
	}
	var decoded Result
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	out.Raw = nil
	if !reflect.DeepEqual(out, &decoded) {
		t.Fatalf("round trip: %#v", decoded)
	}
}

func TestAPIErrorDoesNotPrintBody(t *testing.T) {
	err := &APIError{Provider: "typesafe", StatusCode: 429, Body: []byte("private response"), RetryAfter: "5"}
	if strings.Contains(err.Error(), "private") || !strings.Contains(err.Error(), "429") {
		t.Fatal(err)
	}
}
