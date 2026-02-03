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
)

type Config struct {
	AwsKey    string
	AwsSecret string
	AwsRegion string
	ModelArn  string
}

type Provider struct {
	client   bedrockruntimeiface.BedrockRuntimeAPI
	modelArn string
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
	if p.modelArn == "" {
		return nil, fmt.Errorf("bedrock model arn is required")
	}

	systemParts := make([]string, 0, 1)
	messages := make([]bedrockMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		switch m.Role {
		case chat.RoleSystem:
			if m.Content != "" {
				systemParts = append(systemParts, m.Content)
			}
		case chat.RoleUser, chat.RoleAssistant:
			if m.Content == "" {
				continue
			}
			messages = append(messages, bedrockMessage{
				Role: m.Role,
				Content: []bedrockMsgContent{
					{Type: "text", Text: m.Content},
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

	text := ""
	if len(out.Content) > 0 {
		text = out.Content[0].Text
	}

	result := &chat.Result{
		Text: text,
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
