package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/quailyquaily/uniai/chat"
)

type Config struct {
	APIKey       string
	DefaultModel string
}

type Provider struct {
	cfg Config
}

func New(cfg Config) *Provider {
	return &Provider{cfg: cfg}
}

type anthropicMessage struct {
	Role    string                 `json:"role"`
	Content []anthropicContentPart `json:"content,omitempty"`
}

type anthropicContentPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type anthropicRequest struct {
	Model         string             `json:"model"`
	System        string             `json:"system,omitempty"`
	Messages      []anthropicMessage `json:"messages"`
	MaxTokens     int                `json:"max_tokens"`
	Temperature   *float64           `json:"temperature,omitempty"`
	TopP          *float64           `json:"top_p,omitempty"`
	StopSequences []string           `json:"stop_sequences,omitempty"`
}

type anthropicResponse struct {
	Content []anthropicContentPart `json:"content"`
	Model   string                 `json:"model"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func (p *Provider) Chat(ctx context.Context, req *chat.Request) (*chat.Result, error) {
	if p.cfg.APIKey == "" {
		return nil, fmt.Errorf("anthropic api key is required")
	}
	model := req.Model
	if model == "" {
		model = p.cfg.DefaultModel
	}
	if model == "" {
		return nil, fmt.Errorf("model is required")
	}

	systemParts := make([]string, 0, 1)
	messages := make([]anthropicMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Role == chat.RoleSystem {
			if m.Content != "" {
				systemParts = append(systemParts, m.Content)
			}
			continue
		}
		messages = append(messages, anthropicMessage{
			Role: m.Role,
			Content: []anthropicContentPart{
				{Type: "text", Text: m.Content},
			},
		})
	}
	if len(messages) == 0 {
		return nil, fmt.Errorf("at least one non-system message is required")
	}

	maxTokens := 8192
	if req.Options.MaxTokens != nil {
		maxTokens = *req.Options.MaxTokens
	}

	body := anthropicRequest{
		Model:         model,
		System:        strings.Join(systemParts, "\n"),
		Messages:      messages,
		MaxTokens:     maxTokens,
		Temperature:   req.Options.Temperature,
		TopP:          req.Options.TopP,
		StopSequences: req.Options.Stop,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.cfg.APIKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("anthropic api error: status %d", resp.StatusCode)
	}

	var out anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}

	text := ""
	if len(out.Content) > 0 {
		text = out.Content[0].Text
	}

	result := &chat.Result{
		Text:  text,
		Model: out.Model,
		Usage: chat.Usage{
			InputTokens:  out.Usage.InputTokens,
			OutputTokens: out.Usage.OutputTokens,
			TotalTokens:  out.Usage.InputTokens + out.Usage.OutputTokens,
		},
		Raw: out,
	}

	if len(req.Tools) > 0 {
		result.Warnings = append(result.Warnings, "tools not supported for anthropic provider yet")
	}

	return result, nil
}
