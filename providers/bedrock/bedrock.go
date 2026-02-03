package bedrock

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/bedrockruntime"
	"github.com/aws/aws-sdk-go/service/bedrockruntime/bedrockruntimeiface"
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

	messages := make([]bedrockMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		messages = append(messages, bedrockMessage{
			Role: m.Role,
			Content: []bedrockMsgContent{
				{Type: "text", Text: m.Content},
			},
		})
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
