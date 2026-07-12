package bedrock

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/lyricat/goutils/structs"
	"github.com/quailyquaily/uniai/chat"
)

func TestToBedrockContentMapsCacheControl(t *testing.T) {
	content, err := toBedrockContent(chat.UserParts(
		chat.WithPartCacheControl(chat.TextPart("prefix"), chat.CacheTTL5m()),
		chat.TextPart("suffix"),
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(content))
	}
	if content[0].CacheControl == nil || content[0].CacheControl.TTL != "5m" {
		t.Fatalf("unexpected cache control: %#v", content[0].CacheControl)
	}
	if content[1].CacheControl != nil {
		t.Fatalf("expected second content block to have no cache control")
	}
}

func TestValidateBedrockCacheControl(t *testing.T) {
	req := &chat.Request{
		Messages: []chat.Message{
			chat.UserParts(chat.WithPartCacheControl(chat.TextPart("prefix"), chat.CacheTTL5m())),
		},
	}
	if err := validateBedrockCacheControl(req, "anthropic.claude-sonnet-4-20250514-v1:0"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := validateBedrockCacheControl(req, "amazon.nova-pro-v1:0"); err == nil {
		t.Fatalf("expected error for non-anthropic model")
	}

	systemReq := &chat.Request{
		Messages: []chat.Message{
			chat.SystemParts(chat.WithPartCacheControl(chat.TextPart("system"), chat.CacheTTL5m())),
			chat.User("hello"),
		},
	}
	if err := validateBedrockCacheControl(systemReq, "anthropic.claude-sonnet-4-20250514-v1:0"); err == nil {
		t.Fatalf("expected error for system cache control")
	}

	toolReq := &chat.Request{
		Messages: []chat.Message{
			chat.User("hello"),
		},
		Tools: []chat.Tool{
			chat.WithToolCacheControl(chat.FunctionTool("lookup", "desc", []byte(`{"type":"object"}`)), chat.CacheTTL5m()),
		},
	}
	if err := validateBedrockCacheControl(toolReq, "anthropic.claude-sonnet-4-20250514-v1:0"); err == nil {
		t.Fatalf("expected error for tool cache control")
	}
}

func TestToBedrockContentRejectsEmptyCachedTextPart(t *testing.T) {
	_, err := toBedrockContent(chat.UserParts(
		chat.WithPartCacheControl(chat.TextPart(" "), chat.CacheTTL5m()),
	))
	if err == nil {
		t.Fatalf("expected error")
	}
	if got := err.Error(); !strings.Contains(got, "non-empty text part") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBedrockOpus47ModelOverlayDropsTopK(t *testing.T) {
	payload := map[string]any{}
	applyBedrockOptions(payload, structs.JSONMap{"top_k": 5})
	applyBedrockModelOverlay(payload, "anthropic.claude-opus-4.7-v1:0")
	if _, ok := payload["top_k"]; ok {
		t.Fatalf("expected Opus 4.7 top_k to be omitted, got %#v", payload)
	}
}

func TestBedrockModelOverlayKeepsTopKForOpus46(t *testing.T) {
	payload := map[string]any{}
	applyBedrockOptions(payload, structs.JSONMap{"top_k": 5})
	applyBedrockModelOverlay(payload, "anthropic.claude-opus-4-6-v1:0")
	if payload["top_k"] != 5 {
		t.Fatalf("expected Opus 4.6 top_k to be preserved, got %#v", payload)
	}
}

func TestParseBedrockUsageReadsCacheMetrics(t *testing.T) {
	usage := parseBedrockUsage(bedrockUsage{
		InputTokens:              100,
		OutputTokens:             12,
		CacheReadInputTokens:     80,
		CacheCreationInputTokens: 40,
		CacheCreation: map[string]int{
			"ephemeral_5m_input_tokens": 40,
		},
	})
	if usage.InputTokens != 100 || usage.OutputTokens != 12 || usage.TotalTokens != 112 {
		t.Fatalf("unexpected usage: %#v", usage)
	}
	if usage.Cache.CachedInputTokens != 80 || usage.Cache.CacheCreationInputTokens != 40 {
		t.Fatalf("unexpected cache usage: %#v", usage.Cache)
	}
	if usage.Cache.Details["ephemeral_5m_input_tokens"] != 40 {
		t.Fatalf("unexpected cache details: %#v", usage.Cache.Details)
	}
}

func TestChatInvokesBedrockRuntimeV2Client(t *testing.T) {
	fake := &fakeBedrockRuntimeClient{
		invokeModelOutput: &bedrockruntime.InvokeModelOutput{
			Body: []byte(`{
				"content": [{"type": "text", "text": "hello"}],
				"usage": {"input_tokens": 3, "output_tokens": 2}
			}`),
		},
	}
	p := &Provider{
		client:   fake,
		modelArn: "anthropic.claude-sonnet-4-20250514-v1:0",
		headers:  map[string]string{"X-Test": "yes"},
	}

	result, err := p.Chat(context.Background(), &chat.Request{
		Messages: []chat.Message{chat.User("hi")},
	})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if result.Text != "hello" {
		t.Fatalf("unexpected text: %q", result.Text)
	}
	if result.Usage.InputTokens != 3 || result.Usage.OutputTokens != 2 || result.Usage.TotalTokens != 5 {
		t.Fatalf("unexpected usage: %#v", result.Usage)
	}
	if fake.invokeModelInput == nil {
		t.Fatalf("expected InvokeModel input")
	}
	if fake.invokeModelInput.ModelId == nil || *fake.invokeModelInput.ModelId != p.modelArn {
		t.Fatalf("unexpected model id: %#v", fake.invokeModelInput.ModelId)
	}
	if fake.invokeModelInput.ContentType == nil || *fake.invokeModelInput.ContentType != "application/json" {
		t.Fatalf("unexpected content type: %#v", fake.invokeModelInput.ContentType)
	}
	if fake.invokeModelOptFns == 0 {
		t.Fatalf("expected request options for custom headers")
	}

	var payload map[string]any
	if err := json.Unmarshal(fake.invokeModelInput.Body, &payload); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	if payload["anthropic_version"] != "bedrock-2023-05-31" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestChatStreamConsumesBedrockRuntimeV2Stream(t *testing.T) {
	stream := newFakeBedrockResponseStream(
		&types.ResponseStreamMemberChunk{Value: types.PayloadPart{Bytes: []byte(`{
			"type": "message_start",
			"message": {"model": "claude-test", "usage": {"input_tokens": 3}}
		}`)}},
		&types.ResponseStreamMemberChunk{Value: types.PayloadPart{Bytes: []byte(`{
			"type": "content_block_delta",
			"delta": {"type": "text_delta", "text": "hel"}
		}`)}},
		&types.ResponseStreamMemberChunk{Value: types.PayloadPart{Bytes: []byte(`{
			"type": "content_block_delta",
			"delta": {"type": "text_delta", "text": "lo"}
		}`)}},
		&types.ResponseStreamMemberChunk{Value: types.PayloadPart{Bytes: []byte(`{
			"type": "message_delta",
			"usage": {"output_tokens": 2}
		}`)}},
	)
	fake := &fakeBedrockRuntimeClient{stream: stream}
	p := &Provider{
		client:   fake,
		modelArn: "anthropic.claude-sonnet-4-20250514-v1:0",
	}

	var events []chat.StreamEvent
	result, err := p.Chat(context.Background(), &chat.Request{
		Messages: []chat.Message{chat.User("hi")},
		Options: chat.Options{
			OnStream: func(ev chat.StreamEvent) error {
				events = append(events, ev)
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("chat stream: %v", err)
	}
	if result.Text != "hello" || result.Model != "claude-test" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if result.Usage.InputTokens != 3 || result.Usage.OutputTokens != 2 || result.Usage.TotalTokens != 5 {
		t.Fatalf("unexpected usage: %#v", result.Usage)
	}
	if len(events) != 3 || events[0].Delta != "hel" || events[1].Delta != "lo" || !events[2].Done {
		t.Fatalf("unexpected stream events: %#v", events)
	}
	if !stream.closed {
		t.Fatalf("expected stream to be closed")
	}
	if fake.streamInput == nil || fake.streamInput.ModelId == nil || *fake.streamInput.ModelId != p.modelArn {
		t.Fatalf("unexpected stream input: %#v", fake.streamInput)
	}
}

type fakeBedrockRuntimeClient struct {
	invokeModelInput  *bedrockruntime.InvokeModelInput
	invokeModelOptFns int
	invokeModelOutput *bedrockruntime.InvokeModelOutput
	invokeModelErr    error

	streamInput  *bedrockruntime.InvokeModelWithResponseStreamInput
	streamOptFns int
	stream       bedrockResponseStream
	streamErr    error
}

func (c *fakeBedrockRuntimeClient) InvokeModel(ctx context.Context, input *bedrockruntime.InvokeModelInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.InvokeModelOutput, error) {
	c.invokeModelInput = input
	c.invokeModelOptFns = len(optFns)
	if c.invokeModelOutput == nil {
		c.invokeModelOutput = &bedrockruntime.InvokeModelOutput{}
	}
	return c.invokeModelOutput, c.invokeModelErr
}

func (c *fakeBedrockRuntimeClient) InvokeModelWithResponseStream(ctx context.Context, input *bedrockruntime.InvokeModelWithResponseStreamInput, optFns ...func(*bedrockruntime.Options)) (bedrockResponseStream, error) {
	c.streamInput = input
	c.streamOptFns = len(optFns)
	return c.stream, c.streamErr
}

type fakeBedrockResponseStream struct {
	events chan types.ResponseStream
	err    error
	closed bool
}

func newFakeBedrockResponseStream(events ...types.ResponseStream) *fakeBedrockResponseStream {
	ch := make(chan types.ResponseStream, len(events))
	for _, event := range events {
		ch <- event
	}
	close(ch)
	return &fakeBedrockResponseStream{events: ch}
}

func (s *fakeBedrockResponseStream) Events() <-chan types.ResponseStream {
	return s.events
}

func (s *fakeBedrockResponseStream) Close() error {
	s.closed = true
	return nil
}

func (s *fakeBedrockResponseStream) Err() error {
	return s.err
}
