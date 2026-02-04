package susanoo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/quailyquaily/uniai/chat"
	"github.com/quailyquaily/uniai/internal/diag"
	"github.com/quailyquaily/uniai/internal/httputil"
)

type Config struct {
	APIBase string
	APIKey  string
	Debug   bool
}

type Provider struct {
	cfg Config
}

func New(cfg Config) *Provider {
	return &Provider{cfg: cfg}
}

type taskRequest struct {
	Messages []chat.Message `json:"messages"`
	Params   map[string]any `json:"params"`
}

type taskResponse struct {
	Data struct {
		Code    int    `json:"code"`
		TraceID string `json:"trace_id"`
	} `json:"data"`
}

type taskResultResponse struct {
	Data struct {
		Result map[string]any `json:"result"`
		Status int            `json:"status"`
		Usage  struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
		CostTime int `json:"cost_time"`
	} `json:"data"`
}

func (p *Provider) Chat(ctx context.Context, req *chat.Request) (*chat.Result, error) {
	debugFn := req.Options.DebugFn
	if p.cfg.APIBase == "" || p.cfg.APIKey == "" {
		return nil, fmt.Errorf("susanoo api base and api key are required")
	}

	params := map[string]any{}
	if req.Model != "" {
		params["model"] = req.Model
	}
	if req.Options.Temperature != nil {
		params["temperature"] = *req.Options.Temperature
	}
	if req.Options.TopP != nil {
		params["top_p"] = *req.Options.TopP
	}
	if req.Options.MaxTokens != nil {
		params["max_tokens"] = *req.Options.MaxTokens
	}
	if len(req.Options.Stop) > 0 {
		params["stop"] = req.Options.Stop
	}
	if req.Options.PresencePenalty != nil {
		params["presence_penalty"] = *req.Options.PresencePenalty
	}
	if req.Options.FrequencyPenalty != nil {
		params["frequency_penalty"] = *req.Options.FrequencyPenalty
	}
	if req.Options.User != nil {
		params["user"] = *req.Options.User
	}
	if len(req.Tools) > 0 {
		params["tools"] = req.Tools
		if req.ToolChoice != nil {
			params["tool_choice"] = req.ToolChoice
		}
	}

	traceID, err := p.createTask(ctx, &taskRequest{
		Messages: req.Messages,
		Params:   params,
	}, debugFn)
	if err != nil {
		return nil, err
	}

	result, err := p.pollResult(ctx, traceID, debugFn)
	if err != nil {
		return nil, err
	}
	diag.LogJSON(p.cfg.Debug, debugFn, "susanoo.chat.response", result)

	text := ""
	if val, ok := result.Data.Result["response"]; ok {
		if s, ok := val.(string); ok {
			text = s
		}
	}

	return &chat.Result{
		Text: text,
		Usage: chat.Usage{
			InputTokens:  result.Data.Usage.InputTokens,
			OutputTokens: result.Data.Usage.OutputTokens,
			TotalTokens:  result.Data.Usage.InputTokens + result.Data.Usage.OutputTokens,
		},
		Raw: result,
	}, nil
}

func (p *Provider) createTask(ctx context.Context, task *taskRequest, debugFn func(string, string)) (string, error) {
	data, err := json.Marshal(task)
	if err != nil {
		return "", err
	}
	diag.LogText(p.cfg.Debug, debugFn, "susanoo.chat.request", string(data))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/tasks", p.cfg.APIBase), bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-SUSANOO-KEY", p.cfg.APIKey)

	resp, err := httputil.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respData, err := httputil.ReadBody(resp.Body)
	if err != nil {
		return "", err
	}
	diag.LogText(p.cfg.Debug, debugFn, "susanoo.chat.create_task.response", string(respData))
	var out taskResponse
	if err := json.Unmarshal(respData, &out); err != nil {
		return "", err
	}
	if out.Data.Code != 0 {
		return "", fmt.Errorf("susanoo create task error: %d", out.Data.Code)
	}
	return out.Data.TraceID, nil
}

const (
	pollInterval   = 3 * time.Second
	maxPollRetries = 100
)

func (p *Provider) pollResult(ctx context.Context, traceID string, debugFn func(string, string)) (*taskResultResponse, error) {
	for attempt := 0; attempt < maxPollRetries; attempt++ {
		result, err := p.fetchResult(ctx, traceID, debugFn)
		if err != nil {
			return nil, err
		}
		if result.Data.Status == 3 {
			return result, nil
		}
		if result.Data.Status == 4 {
			return nil, errors.New("susanoo task failed")
		}
		// Status 1 (pending), 2 (running), or unknown: wait and retry.
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("susanoo poll cancelled: %w", ctx.Err())
		case <-time.After(pollInterval):
		}
	}
	return nil, fmt.Errorf("susanoo poll exceeded max retries (%d)", maxPollRetries)
}

func (p *Provider) fetchResult(ctx context.Context, traceID string, debugFn func(string, string)) (*taskResultResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/tasks/result?trace_id=%s", p.cfg.APIBase, url.QueryEscape(traceID)), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-SUSANOO-KEY", p.cfg.APIKey)

	resp, err := httputil.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respData, err := httputil.ReadBody(resp.Body)
	if err != nil {
		return nil, err
	}
	diag.LogText(p.cfg.Debug, debugFn, "susanoo.chat.fetch_result.response", string(respData))
	var out taskResultResponse
	if err := json.Unmarshal(respData, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
