package cloudflare

import "testing"

func TestChatPreservesCompletionStateForEvaluate(t *testing.T) {
	for _, tc := range []struct{ payload, want string }{
		{`{"response":"{}","finish_reason":"length"}`, "length"},
		{`{"choices":[{"message":{"content":"{}"},"finish_reason":"length"}]}`, "length"},
		{`{"choices":[{"message":{"content":"{}","refusal":"refused"},"finish_reason":"stop"}]}`, "content_filter"},
		{`{"output_text":"{}","status":"incomplete"}`, "incomplete"},
		{`{"output_text":"{}","status":"failed"}`, "failed"},
		{`{"output_text":"{}","status":"completed"}`, "stop"},
		{`{"output":[{"type":"message","content":[{"type":"output_text","text":"{}"},{"type":"refusal","refusal":"refused"}]}],"status":"completed"}`, "content_filter"},
		{`{"response":"{}"}`, ""},
	} {
		out := toChatResult([]byte(tc.payload), "model")
		if out.Text != "{}" || out.FinishReason != tc.want {
			t.Fatalf("payload=%s result=%+v want=%s", tc.payload, out, tc.want)
		}
	}
}
