// Package openaidecisions implements OpenAI's native Decisions API for Evaluate.
package openaidecisions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/quailyquaily/uniai/evaluate"
	"github.com/quailyquaily/uniai/internal/diag"
	"github.com/quailyquaily/uniai/internal/httputil"
)

const DefaultBaseURL = "https://api.openai.com/v1"

type Config struct {
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
	Debug      bool
}

type Provider struct{ cfg Config }

func New(cfg Config) (*Provider, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("%w: openai API key is required", evaluate.ErrInvalidRequest)
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("%w: openai base URL must be an HTTP(S) URL without credentials, query or fragment", evaluate.ErrInvalidRequest)
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &Provider{cfg: cfg}, nil
}

func (p *Provider) Evaluate(ctx context.Context, req *evaluate.Request) (*evaluate.Result, error) {
	if req == nil {
		return nil, fmt.Errorf("%w: request is nil", evaluate.ErrInvalidRequest)
	}
	r := *req
	if r.Provider == "" {
		r.Provider = "openai"
	}
	if err := evaluate.ValidateRequest(&r); err != nil {
		return nil, err
	}
	if r.Provider != "openai" || (r.EmulationMode != "" && r.EmulationMode != evaluate.EmulationOff) || r.EmulationOptions != nil || r.InferenceProvider != "" {
		return nil, fmt.Errorf("%w: openai Decisions accepts only native evaluate requests", evaluate.ErrUnsupported)
	}
	data, err := encodeRequest(&r)
	if err != nil {
		return nil, fmt.Errorf("%w: encode openai Decisions request: %w", evaluate.ErrInvalidRequest, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.BaseURL+"/decisions", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	diag.LogText(p.cfg.Debug, nil, "openai.evaluate.request", strings.ReplaceAll(string(data), p.cfg.APIKey, "[redacted]"))
	client := p.cfg.HTTPClient
	if client == nil {
		client = httputil.ClientForContext(ctx)
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai evaluate: %w", err)
	}
	defer resp.Body.Close()
	body, err := httputil.ReadBody(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("openai evaluate: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &evaluate.APIError{Provider: "openai", StatusCode: resp.StatusCode, Body: body, RetryAfter: resp.Header.Get("Retry-After")}
	}
	diag.LogText(p.cfg.Debug, nil, "openai.evaluate.response", strings.ReplaceAll(string(body), p.cfg.APIKey, "[redacted]"))
	out, err := decodeResponse(&r, body)
	if err != nil {
		return out, fmt.Errorf("%w: openai Decisions: %v", evaluate.ErrInvalidResponse, err)
	}
	return out, nil
}

// Decisions accepts text instructions and evidence. Keep strings unchanged and
// encode structured Evaluate content as JSON text, never as image messages.
func contentText(value any) (*string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if string(data) == "null" {
		return nil, nil
	}
	var s string
	if len(data) > 0 && data[0] == '"' {
		err = json.Unmarshal(data, &s)
		return &s, err
	}
	s = string(data)
	return &s, nil
}

func encodeRequest(req *evaluate.Request) ([]byte, error) {
	input, err := contentText(req.State)
	if err != nil {
		return nil, err
	}
	questions := make([]map[string]any, 0, len(req.Questions))
	for _, id := range slices.Sorted(maps.Keys(req.Questions)) {
		q := req.Questions[id]
		instructionsText, err := contentText(q.Instructions)
		if err != nil {
			return nil, err
		}
		instructions := *instructionsText
		question := map[string]any{"name": id}
		switch q.Kind {
		case evaluate.Boolean:
			question["type"] = "predicate"
			for _, criterion := range []struct {
				label string
				value any
			}{{"True", q.TrueDescription}, {"False", q.FalseDescription}} {
				text, err := contentText(criterion.value)
				if err != nil {
					return nil, err
				}
				if text != nil {
					instructions += "\n" + criterion.label + " criteria: " + *text
				}
			}
		case evaluate.Choice:
			question["type"] = "choice"
			choices := make([]map[string]string, 0, len(q.Options))
			for _, value := range slices.Sorted(maps.Keys(q.Options)) {
				text, err := contentText(q.Options[value])
				if err != nil {
					return nil, err
				}
				choice := map[string]string{"value": value}
				if text != nil {
					choice["description"] = *text
				}
				choices = append(choices, choice)
			}
			question["choices"] = choices
		case evaluate.Score:
			question["type"] = "score"
			levels := make([]map[string]string, 0, len(q.Levels))
			for i, level := range q.Levels {
				text, err := contentText(level)
				if err != nil {
					return nil, err
				}
				levels = append(levels, map[string]string{"label": strconv.Itoa(i), "description": *text})
			}
			question["levels"] = levels
		}
		question["instructions"] = instructions
		questions = append(questions, question)
	}
	return json.Marshal(map[string]any{"model": req.Model, "input": input, "questions": questions})
}

type wireAnswer struct {
	Name          *string  `json:"name"`
	Type          string   `json:"type"`
	Probability   *float64 `json:"probability"`
	Choice        *string  `json:"choice"`
	Score         *float64 `json:"score"`
	Confidence    *float64 `json:"confidence"`
	Probabilities []struct {
		Value       json.RawMessage `json:"value"`
		Label       *string         `json:"label"`
		Probability *float64        `json:"probability"`
	} `json:"probabilities"`
}

func decodeResponse(req *evaluate.Request, body []byte) (*evaluate.Result, error) {
	var wire struct {
		Model   string          `json:"model"`
		Answers json.RawMessage `json:"answers"`
		Usage   *evaluate.Usage `json:"usage"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, err
	}
	if wire.Usage == nil {
		return nil, fmt.Errorf("usage is required")
	}
	// Ignore any upstream cost extension; pricing is computed by the root client.
	wire.Usage.Cost = nil
	for name, count := range map[string]*int{"input_tokens": wire.Usage.InputTokens, "output_tokens": wire.Usage.OutputTokens, "total_tokens": wire.Usage.TotalTokens} {
		if count != nil && *count < 0 {
			return nil, fmt.Errorf("usage.%s must be nonnegative", name)
		}
	}
	out := &evaluate.Result{Provider: "openai", Model: wire.Model, Raw: append(json.RawMessage(nil), body...), Usage: wire.Usage}
	if u := out.Usage; u.TotalTokens == nil && u.InputTokens != nil && u.OutputTokens != nil {
		if *u.InputTokens > int(^uint(0)>>1)-*u.OutputTokens {
			return out, fmt.Errorf("usage total_tokens overflows int")
		}
		total := *u.InputTokens + *u.OutputTokens
		u.TotalTokens = &total
	}
	if strings.TrimSpace(wire.Model) == "" {
		return out, fmt.Errorf("model is required")
	}
	var wireAnswers []wireAnswer
	if err := json.Unmarshal(wire.Answers, &wireAnswers); err != nil {
		return out, err
	}
	answers := make(map[string]evaluate.Answer, len(wireAnswers))
	metadata := map[string]any{}
	for _, a := range wireAnswers {
		if a.Name == nil {
			return out, fmt.Errorf("answer name is required")
		}
		id := *a.Name
		if _, ok := req.Questions[id]; !ok {
			return out, fmt.Errorf("answers[%q]: unknown question", id)
		}
		if _, ok := answers[id]; ok {
			return out, fmt.Errorf("answers[%q]: duplicate answer", id)
		}
		answer := evaluate.Answer{ProbabilityTrue: a.Probability, ScoreValue: a.Score}
		if a.Choice != nil {
			answer.Selected = *a.Choice
		}
		switch a.Type {
		case "predicate":
			answer.Kind = evaluate.Boolean
			if a.Choice != nil || a.Confidence != nil {
				return out, fmt.Errorf("answers[%q]: invalid predicate fields", id)
			}
		case "choice":
			answer.Kind = evaluate.Choice
		case "score":
			answer.Kind = evaluate.Score
			if a.Choice != nil {
				return out, fmt.Errorf("answers[%q]: invalid score fields", id)
			}
		case "refusal":
			return out, fmt.Errorf("answers[%q]: model refused to answer", id)
		default:
			return out, fmt.Errorf("answers[%q]: invalid type", id)
		}
		if a.Type == "choice" || a.Type == "score" {
			if a.Confidence == nil || *a.Confidence < 0 || *a.Confidence > 1 || a.Probabilities == nil {
				return out, fmt.Errorf("answers[%q]: probabilities and confidence in [0,1] are required", id)
			}
			metadata[id] = map[string]any{"confidence": *a.Confidence}
		}
		if a.Probabilities != nil {
			answer.Probabilities = make(map[string]float64, len(a.Probabilities))
			sum := 0.0
			for _, entry := range a.Probabilities {
				if entry.Probability == nil {
					return out, fmt.Errorf("answers[%q]: probability is required", id)
				}
				var key string
				if a.Type == "score" {
					var index *int
					if err := json.Unmarshal(entry.Value, &index); err != nil || index == nil || entry.Label == nil {
						return out, fmt.Errorf("answers[%q]: invalid score level", id)
					}
					key = strconv.Itoa(*index)
				} else if err := json.Unmarshal(entry.Value, &key); err != nil || key == "" {
					return out, fmt.Errorf("answers[%q]: invalid choice value", id)
				}
				if _, exists := answer.Probabilities[key]; exists {
					return out, fmt.Errorf("answers[%q]: duplicate probability value", id)
				}
				answer.Probabilities[key] = *entry.Probability
				sum += *entry.Probability
			}
			if math.Abs(sum-1) > 1e-6 {
				return out, fmt.Errorf("answers[%q]: probabilities must sum to 1", id)
			}
		}
		answers[id] = answer
	}
	if len(metadata) > 0 {
		data, err := json.Marshal(map[string]any{"answers": metadata})
		if err != nil {
			return out, err
		}
		out.ProviderMetadata = map[string]json.RawMessage{"openai": data}
	}
	out.Answers = answers
	if err := evaluate.ValidateResult(req, out); err != nil {
		out.Answers = nil
		return out, err
	}
	return out, nil
}
