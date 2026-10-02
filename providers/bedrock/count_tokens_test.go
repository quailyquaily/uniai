package bedrock

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/quailyquaily/uniai/chat"
)

type countingBedrockClient struct {
	fakeBedrockRuntimeClient
	input  *bedrockruntime.CountTokensInput
	output *bedrockruntime.CountTokensOutput
	err    error
}

func (c *countingBedrockClient) CountTokens(ctx context.Context, in *bedrockruntime.CountTokensInput, opts ...func(*bedrockruntime.Options)) (*bedrockruntime.CountTokensOutput, error) {
	c.input = in
	return c.output, c.err
}

// Existing inference-only fakes must never receive counting calls.
func (c *fakeBedrockRuntimeClient) CountTokens(context.Context, *bedrockruntime.CountTokensInput, ...func(*bedrockruntime.Options)) (*bedrockruntime.CountTokensOutput, error) {
	return nil, errors.New("unexpected CountTokens call")
}

func TestCountTokensUsesInferencePayload(t *testing.T) {
	fake := &countingBedrockClient{output: &bedrockruntime.CountTokensOutput{InputTokens: aws.Int32(42)}}
	fake.invokeModelOutput = &bedrockruntime.InvokeModelOutput{Body: []byte(`{"content":[],"usage":{},"stop_reason":"end_turn"}`)}
	p := &Provider{client: fake, modelArn: "anthropic.claude-sonnet-4-6-v1:0"}
	req := &chat.Request{Messages: []chat.Message{chat.System("system"), chat.UserParts(chat.WithPartCacheControl(chat.TextPart("prefix"), chat.CacheTTL5m()), chat.TextPart("tail"))}}
	if _, err := p.Chat(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	got, err := p.CountTokens(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if got.InputTokens != 42 || got.Model != p.modelArn {
		t.Fatalf("unexpected count: %+v", got)
	}
	input, ok := fake.input.Input.(*types.CountTokensInputMemberInvokeModel)
	if !ok {
		t.Fatal("count did not use InvokeModel body")
	}
	if !bytes.Equal(input.Value.Body, fake.invokeModelInput.Body) || *fake.input.ModelId != *fake.invokeModelInput.ModelId {
		t.Fatalf("count payload differs from inference payload")
	}
}
func TestCountTokensPartialAndFailures(t *testing.T) {
	for _, tc := range []struct {
		name        string
		output      *bedrockruntime.CountTokensOutput
		err         error
		unsupported bool
	}{
		{"valid empty", &bedrockruntime.CountTokensOutput{InputTokens: aws.Int32(0)}, nil, false},
		{"missing count", &bedrockruntime.CountTokensOutput{}, nil, false},
		{"negative", &bedrockruntime.CountTokensOutput{InputTokens: aws.Int32(-1)}, nil, false},
		{"not supported", nil, &types.ValidationException{Message: aws.String("This model does not support counting tokens.")}, true},
		{"invalid input", nil, &types.ValidationException{Message: aws.String("messages: invalid content")}, false},
		{"access denied", nil, &types.AccessDeniedException{Message: aws.String("denied")}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &countingBedrockClient{output: tc.output, err: tc.err}
			p := &Provider{client: fake, modelArn: "anthropic.claude-sonnet-4-6-v1:0"}
			got, err := p.CountTokens(context.Background(), &chat.Request{})
			if tc.name == "valid empty" {
				if err != nil || got.InputTokens != 0 {
					t.Fatalf("%+v %v", got, err)
				}
			} else if err == nil || got != nil {
				t.Fatalf("%+v %v", got, err)
			}
			if errors.Is(err, chat.ErrTokenCountUnsupported) != tc.unsupported {
				t.Fatalf("incorrect error classification: %v", err)
			}
			if tc.err != nil && !errors.Is(err, tc.err) {
				t.Fatalf("lost upstream error: %v", err)
			}
			var payload map[string]any
			if err := json.Unmarshal(fake.input.Input.(*types.CountTokensInputMemberInvokeModel).Value.Body, &payload); err != nil {
				t.Fatal(err)
			}
			if len(payload["messages"].([]any)) != 0 {
				t.Fatal("partial request was padded")
			}
			if fake.invokeModelInput != nil || fake.streamInput != nil {
				t.Fatal("counting invoked inference")
			}
		})
	}
}
