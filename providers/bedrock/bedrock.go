package bedrock

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/bedrockruntime"
	"github.com/aws/aws-sdk-go/service/bedrockruntime/bedrockruntimeiface"
	"github.com/lyricat/goutils/structs"
	"github.com/quailyquaily/uniai/chat"
	"github.com/quailyquaily/uniai/internal/diag"
)

type Config struct {
	AwsKey    string
	AwsSecret string
	AwsRegion string
	ModelArn  string
	Debug     bool
}

type Provider struct {
	client   bedrockruntimeiface.BedrockRuntimeAPI
	modelArn string
	debug    bool
}

func New(cfg Config) *Provider {
	region := cfg.AwsRegion
	if region == "" {
		region = "us-east-1"
	}
	sess := session.Must(session.NewSession(&aws.Config{
		Region:      aws.String(region),
		Credentials: credentials.NewStaticCredentials(cfg.AwsKey, cfg.AwsSecret, ""),
	}))
	return &Provider{
		client:   bedrockruntime.New(sess),
		modelArn: cfg.ModelArn,
		debug:    cfg.Debug,
	}
}

type bedrockMessage struct {
	Role    string              `json:"role"`
	Content []bedrockMsgContent `json:"content"`
}

type bedrockMsgContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type bedrockResponse struct {
	Content []bedrockMsgContent `json:"content"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func (p *Provider) Chat(ctx context.Context, req *chat.Request) (*chat.Result, error) {
	debugFn := req.Options.DebugFn
	if p.modelArn == "" {
		return nil, fmt.Errorf("bedrock model arn is required")
	}

	systemParts := make([]string, 0, 1)
	messages := make([]bedrockMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		text, err := chat.MessageText(m)
		if err != nil {
			return nil, fmt.Errorf("bedrock provider model %q: role %q: %w", p.modelArn, m.Role, err)
		}
		switch m.Role {
		case chat.RoleSystem:
			if text != "" {
				systemParts = append(systemParts, text)
			}
		case chat.RoleUser, chat.RoleAssistant:
			if text == "" {
				continue
			}
			messages = append(messages, bedrockMessage{
				Role: m.Role,
				Content: []bedrockMsgContent{
					{Type: "text", Text: text},
				},
			})
		default:
			return nil, fmt.Errorf("bedrock provider does not support role %q", m.Role)
		}
	}
	if len(messages) == 0 {
		return nil, fmt.Errorf("at least one user or assistant message is required")
	}

	maxTokens := 10000
	if req.Options.MaxTokens != nil {
		maxTokens = *req.Options.MaxTokens
	}

	payload := map[string]any{
		"anthropic_version": "bedrock-2023-05-31",
		"max_tokens":        maxTokens,
		"messages":          messages,
	}
	if len(systemParts) > 0 {
		payload["system"] = strings.Join(systemParts, "\n")
	}
	applyBedrockOptions(payload, req.Options.Bedrock)

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	diag.LogText(p.debug, debugFn, "bedrock.chat.request", string(body))

	if req.Options.OnStream != nil {
		return p.chatStream(ctx, body, req.Options.OnStream, req.Tools)
	}

	resp, err := p.client.InvokeModelWithContext(ctx, &bedrockruntime.InvokeModelInput{
		ModelId:     aws.String(p.modelArn),
		Body:        body,
		Accept:      aws.String("application/json"),
		ContentType: aws.String("application/json"),
	})
	if err != nil {
		return nil, err
	}

	var out bedrockResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		return nil, err
	}
	diag.LogText(p.debug, debugFn, "bedrock.chat.response", string(resp.Body))

	var textParts []string
	for _, c := range out.Content {
		if c.Type == "text" && c.Text != "" {
			textParts = append(textParts, c.Text)
		}
	}
	text := strings.Join(textParts, "")

	result := &chat.Result{
		Text: text,
		Parts: func() []chat.Part {
			if text == "" {
				return nil
			}
			return []chat.Part{chat.TextPart(text)}
		}(),
		Usage: chat.Usage{
			InputTokens:  out.Usage.InputTokens,
			OutputTokens: out.Usage.OutputTokens,
			TotalTokens:  out.Usage.InputTokens + out.Usage.OutputTokens,
		},
		Raw: out,
	}
	if len(req.Tools) > 0 {
		result.Warnings = append(result.Warnings, "tools not supported for bedrock provider yet")
	}
	return result, nil
}

// bedrockStreamEvent represents a single event from the Bedrock streaming response.
// Each PayloadPart.Bytes contains a JSON object with a "type" field.
type bedrockStreamEvent struct {
	Type         string `json:"type"`
	Index        int    `json:"index,omitempty"`
	ContentBlock *struct {
		Type string `json:"type"`
	} `json:"content_block,omitempty"`
	Delta *struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	} `json:"delta,omitempty"`
	Message *struct {
		Model string `json:"model,omitempty"`
		Usage *struct {
			InputTokens int `json:"input_tokens"`
		} `json:"usage,omitempty"`
	} `json:"message,omitempty"`
	Usage *struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"usage,omitempty"`
}

func (p *Provider) chatStream(ctx context.Context, body []byte, onStream chat.OnStreamFunc, tools []chat.Tool) (*chat.Result, error) {
	resp, err := p.client.InvokeModelWithResponseStreamWithContext(ctx, &bedrockruntime.InvokeModelWithResponseStreamInput{
		ModelId:     aws.String(p.modelArn),
		Body:        body,
		Accept:      aws.String("application/json"),
		ContentType: aws.String("application/json"),
	})
	if err != nil {
		return nil, err
	}
	stream := resp.GetStream()
	defer stream.Close()

	var (
		textParts    []string
		model        string
		inputTokens  int
		outputTokens int
	)

	for event := range stream.Events() {
		chunk, ok := event.(*bedrockruntime.PayloadPart)
		if !ok || len(chunk.Bytes) == 0 {
			continue
		}

		var ev bedrockStreamEvent
		if err := json.Unmarshal(chunk.Bytes, &ev); err != nil {
			continue
		}

		switch ev.Type {
		case "message_start":
			if ev.Message != nil {
				model = ev.Message.Model
				if ev.Message.Usage != nil {
					inputTokens = ev.Message.Usage.InputTokens
				}
			}
		case "content_block_delta":
			if ev.Delta != nil && ev.Delta.Type == "text_delta" && ev.Delta.Text != "" {
				textParts = append(textParts, ev.Delta.Text)
				if err := onStream(chat.StreamEvent{
					Delta: ev.Delta.Text,
				}); err != nil {
					return nil, err
				}
			}
		case "message_delta":
			if ev.Usage != nil {
				outputTokens = ev.Usage.OutputTokens
			}
		}
	}

	if err := stream.Err(); err != nil {
		return nil, err
	}

	totalTokens := inputTokens + outputTokens
	if err := onStream(chat.StreamEvent{
		Done: true,
		Usage: &chat.Usage{
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
			TotalTokens:  totalTokens,
		},
	}); err != nil {
		return nil, err
	}

	result := &chat.Result{
		Text:  strings.Join(textParts, ""),
		Model: model,
		Parts: func() []chat.Part {
			text := strings.Join(textParts, "")
			if text == "" {
				return nil
			}
			return []chat.Part{chat.TextPart(text)}
		}(),
		Usage: chat.Usage{
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
			TotalTokens:  totalTokens,
		},
	}
	if len(tools) > 0 {
		result.Warnings = append(result.Warnings, "tools not supported for bedrock provider yet")
	}
	return result, nil
}

func applyBedrockOptions(payload map[string]any, opts structs.JSONMap) {
	if payload == nil || len(opts) == 0 {
		return
	}
	opt := &opts
	if opt.HasKey("top_k") {
		if top := int(opt.GetInt64("top_k")); top > 0 {
			payload["top_k"] = top
		}
	}
}
