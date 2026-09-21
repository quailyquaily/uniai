// Package typesafe implements TypeSafe AI's native System One API.
package typesafe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/quailyquaily/uniai/evaluate"
	"github.com/quailyquaily/uniai/internal/diag"
	"github.com/quailyquaily/uniai/internal/httputil"
)

const DefaultBaseURL = "https://api.typesafe.ai/v1"

type Config struct {
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
	Debug      bool
}

type Provider struct{ cfg Config }

func New(cfg Config) (*Provider, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("%w: typesafe API key is required", evaluate.ErrInvalidRequest)
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("%w: typesafe base URL must be an HTTP(S) URL without credentials, query or fragment", evaluate.ErrInvalidRequest)
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
		r.Provider = "typesafe"
	}
	if err := evaluate.ValidateRequest(&r); err != nil {
		return nil, err
	}
	if r.Provider != "typesafe" || (r.EmulationMode != "" && r.EmulationMode != evaluate.EmulationOff) || r.EmulationOptions != nil || r.InferenceProvider != "" {
		return nil, fmt.Errorf("%w: typesafe accepts only native evaluate requests", evaluate.ErrUnsupported)
	}
	questions := make(map[string]any, len(r.Questions))
	for id, q := range r.Questions {
		question := map[string]any{"instructions": q.Instructions}
		switch q.Kind {
		case evaluate.Boolean:
			question["type"] = "noul"
			criteria := map[string]json.RawMessage{}
			for key, value := range map[string]any{"true": q.TrueDescription, "false": q.FalseDescription} {
				data, err := json.Marshal(value)
				if err != nil {
					return nil, fmt.Errorf("%w: questions[%q].criteria: %w", evaluate.ErrInvalidRequest, id, err)
				}
				if string(data) != "null" {
					criteria[key] = data
				}
			}
			if len(criteria) > 0 {
				question["criteria"] = criteria
			}
		case evaluate.Choice:
			if len(q.Options) > 255 {
				return nil, fmt.Errorf("%w: questions[%q].options exceeds 255 choices", evaluate.ErrInvalidRequest, id)
			}
			question["type"] = "choice"
			question["criteria"] = q.Options
		case evaluate.Score:
			if len(q.Levels) > 10 {
				return nil, fmt.Errorf("%w: questions[%q].levels exceeds 10 levels", evaluate.ErrInvalidRequest, id)
			}
			question["type"] = "score"
			question["criteria"] = q.Levels
		}
		questions[id] = question
	}
	data, err := json.Marshal(map[string]any{"model": r.Model, "state": r.State, "questions": questions})
	if err != nil {
		return nil, fmt.Errorf("%w: encode typesafe request: %w", evaluate.ErrInvalidRequest, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.BaseURL+"/systemone", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	diag.LogText(p.cfg.Debug, nil, "typesafe.evaluate.request", strings.ReplaceAll(string(data), p.cfg.APIKey, "[redacted]"))
	client := p.cfg.HTTPClient
	if client == nil {
		client = httputil.ClientForContext(ctx)
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("typesafe evaluate: %w", err)
	}
	defer resp.Body.Close()
	body, err := httputil.ReadBody(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("typesafe evaluate: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &evaluate.APIError{Provider: "typesafe", StatusCode: resp.StatusCode, Body: body, RetryAfter: resp.Header.Get("Retry-After")}
	}
	diag.LogText(p.cfg.Debug, nil, "typesafe.evaluate.response", strings.ReplaceAll(string(body), p.cfg.APIKey, "[redacted]"))
	out, err := decodeResponse(&r, body)
	if err != nil {
		return nil, fmt.Errorf("%w: typesafe: %v", evaluate.ErrInvalidResponse, err)
	}
	return out, nil
}

type wireAnswer struct {
	Type          string                     `json:"type"`
	Noul          *float64                   `json:"noul"`
	Choice        string                     `json:"choice"`
	Score         *float64                   `json:"score"`
	Probabilities map[string]*float64        `json:"probabilities"`
	Confidence    *float64                   `json:"confidence"`
	Legend        map[string]json.RawMessage `json:"legend"`
}

func decodeResponse(req *evaluate.Request, body []byte) (*evaluate.Result, error) {
	var wire struct {
		Model   string                `json:"model"`
		Answers map[string]wireAnswer `json:"answers"`
		Usage   *struct {
			InputTokens  *int `json:"input_tokens"`
			OutputTokens *int `json:"output_tokens"`
			TotalTokens  *int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, err
	}
	if strings.TrimSpace(wire.Model) == "" || wire.Usage == nil {
		return nil, fmt.Errorf("model and usage are required")
	}
	out := &evaluate.Result{Provider: "typesafe", Model: wire.Model, Answers: make(map[string]evaluate.Answer, len(wire.Answers)), Raw: append(json.RawMessage(nil), body...), Usage: &evaluate.Usage{InputTokens: wire.Usage.InputTokens, OutputTokens: wire.Usage.OutputTokens, TotalTokens: wire.Usage.TotalTokens}}
	metadata := map[string]any{}
	for id, a := range wire.Answers {
		q, ok := req.Questions[id]
		if !ok {
			return nil, fmt.Errorf("answers[%q]: unknown question", id)
		}
		answer := evaluate.Answer{ProbabilityTrue: a.Noul, Selected: a.Choice, ScoreValue: a.Score}
		switch a.Type {
		case "noul":
			answer.Kind = evaluate.Boolean
		case "choice":
			answer.Kind = evaluate.Choice
		case "score":
			answer.Kind = evaluate.Score
		default:
			return nil, fmt.Errorf("answers[%q]: invalid type", id)
		}
		if a.Probabilities != nil {
			answer.Probabilities = make(map[string]float64, len(a.Probabilities))
			sum := 0.0
			for key, value := range a.Probabilities {
				if value == nil {
					return nil, fmt.Errorf("answers[%q].probabilities[%q]: null value", id, key)
				}
				answer.Probabilities[key] = *value
				sum += *value
			}
			// TypeSafe rounds probabilities to two decimal places. Preserve the
			// values, allowing at most half a unit of rounding per candidate.
			if math.Abs(sum-1) > float64(len(a.Probabilities))*0.005+1e-12 {
				return nil, fmt.Errorf("answers[%q].probabilities: invalid sum", id)
			}
		}
		if answer.Kind == evaluate.Choice || answer.Kind == evaluate.Score {
			if a.Probabilities == nil || a.Confidence == nil {
				return nil, fmt.Errorf("answers[%q]: probabilities and confidence are required", id)
			}
		}
		meta := map[string]any{}
		if a.Confidence != nil {
			if math.IsNaN(*a.Confidence) || *a.Confidence < 0 || *a.Confidence > 1 {
				return nil, fmt.Errorf("answers[%q].confidence is outside [0,1]", id)
			}
			meta["confidence"] = *a.Confidence
		}
		if answer.Kind == evaluate.Score {
			if len(a.Legend) != len(q.Levels) {
				return nil, fmt.Errorf("answers[%q].legend: incorrect levels", id)
			}
			for i := range q.Levels {
				value, ok := a.Legend[strconv.Itoa(i)]
				if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
					return nil, fmt.Errorf("answers[%q].legend: missing level %d", id, i)
				}
			}
		}
		if a.Legend != nil {
			meta["legend"] = a.Legend
		}
		if len(meta) > 0 {
			metadata[id] = meta
		}
		out.Answers[id] = answer
	}
	if err := evaluate.ValidateResult(req, out); err != nil {
		return nil, err
	}
	usage := out.Usage
	if usage.TotalTokens == nil && usage.InputTokens != nil && usage.OutputTokens != nil {
		if *usage.InputTokens > int(^uint(0)>>1)-*usage.OutputTokens {
			return nil, fmt.Errorf("usage total_tokens overflows int")
		}
		total := *usage.InputTokens + *usage.OutputTokens
		usage.TotalTokens = &total
	}
	if len(metadata) > 0 {
		data, err := json.Marshal(map[string]any{"answers": metadata})
		if err != nil {
			return nil, err
		}
		out.ProviderMetadata = map[string]json.RawMessage{"typesafe": data}
	}
	return out, nil
}
