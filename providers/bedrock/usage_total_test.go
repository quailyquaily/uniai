package bedrock

import (
	"context"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/quailyquaily/uniai/chat"
	"testing"
)

func TestUsageIncludesAllInputTokens(t *testing.T) {
	for _, tc := range []struct {
		name               string
		input, read, write int
	}{
		{"uncached", 50, 0, 0}, {"cached", 50, 10000, 200}, {"fully cached", 0, 10000, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := parseBedrockUsage(bedrockUsage{InputTokens: tc.input, OutputTokens: 25, CacheReadInputTokens: tc.read, CacheCreationInputTokens: tc.write, CacheCreation: map[string]int{"ephemeral_5m_input_tokens": tc.write}})
			want := tc.input + tc.read + tc.write
			if u.InputTokens != want || u.TotalTokens != want+25 {
				t.Fatalf("usage=%+v; want input=%d total=%d", u, want, want+25)
			}
		})
	}
}

func TestStreamUsageIncludesCacheOnce(t *testing.T) {
	stream := newFakeBedrockResponseStream(
		&types.ResponseStreamMemberChunk{Value: types.PayloadPart{Bytes: []byte(`{"type":"message_start","message":{"usage":{"input_tokens":50,"cache_read_input_tokens":10000,"cache_creation_input_tokens":200}}}`)}},
		&types.ResponseStreamMemberChunk{Value: types.PayloadPart{Bytes: []byte(`{"type":"message_delta","usage":{"input_tokens":50,"cache_read_input_tokens":10000,"cache_creation_input_tokens":200,"output_tokens":10}}`)}},
		&types.ResponseStreamMemberChunk{Value: types.PayloadPart{Bytes: []byte(`{"type":"message_delta","usage":{"output_tokens":25}}`)}},
	)
	p := &Provider{client: &fakeBedrockRuntimeClient{stream: stream}, modelArn: "anthropic.claude-sonnet-4-6-v1:0"}
	var final chat.Usage
	out, err := p.Chat(context.Background(), &chat.Request{Messages: []chat.Message{chat.User("hi")}, Options: chat.Options{OnStream: func(e chat.StreamEvent) error {
		if e.Done {
			final = *e.Usage
		}
		return nil
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if final.InputTokens != 10250 || final.TotalTokens != 10275 || out.Usage.InputTokens != 10250 {
		t.Fatalf("incorrect usage: %+v / %+v", final, out.Usage)
	}
}
func TestUsageCacheWriteAlias(t *testing.T) {
	u := parseBedrockUsage(bedrockUsage{InputTokens: 50, CacheReadInputTokens: 10000, CacheWriteInputTokens: 200})
	if u.InputTokens != 10250 {
		t.Fatalf("incorrect usage: %+v", u)
	}
	u = parseBedrockUsage(bedrockUsage{InputTokens: 50, CacheReadInputTokens: 10000, CacheWriteInputTokens: 200, CacheCreationInputTokens: 200})
	if u.InputTokens != 10250 {
		t.Fatalf("counted alias twice: %+v", u)
	}
}
