package bedrock

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/quailyquaily/uniai/chat"
)

func TestChatPreservesCompletionStateForEvaluate(t *testing.T) {
	for _, tc := range []struct{ reason, kind, want string }{
		{"end_turn", "text", "stop"}, {"stop_sequence", "text", "stop"},
		{"max_tokens", "text", "length"}, {"refusal", "text", "content_filter"},
		{"tool_use", "text", "tool_calls"}, {"pause_turn", "text", "pause_turn"},
		{"", "text", ""}, {"", "tool_use", "tool_calls"},
	} {
		t.Run(tc.reason+tc.kind, func(t *testing.T) {
			fake := &fakeBedrockRuntimeClient{invokeModelOutput: &bedrockruntime.InvokeModelOutput{Body: []byte(fmt.Sprintf(`{"content":[{"type":%q,"text":"{}"}],"stop_reason":%q}`, tc.kind, tc.reason))}}
			p := &Provider{client: fake, modelArn: "anthropic.claude-sonnet-4-20250514-v1:0"}
			out, err := p.Chat(context.Background(), &chat.Request{Messages: []chat.Message{chat.User("judge")}})
			if err != nil || out.FinishReason != tc.want {
				t.Fatalf("result=%+v err=%v want=%s", out, err, tc.want)
			}
		})
	}
}
