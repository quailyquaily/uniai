package anthropic

import "testing"

func TestUsageIncludesAllInputTokens(t *testing.T) {
	for _, tc := range []struct {
		name               string
		input, read, write int
	}{
		{"uncached", 50, 0, 0}, {"cached", 50, 10000, 200}, {"fully cached", 0, 10000, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := usageFromAnthropicUsage(anthropicUsage{InputTokens: tc.input, OutputTokens: 25, CacheReadInputTokens: tc.read, CacheCreationInputTokens: tc.write, CacheCreation: map[string]int{"ephemeral_5m_input_tokens": tc.write}})
			want := tc.input + tc.read + tc.write
			if u.InputTokens != want || u.TotalTokens != want+25 {
				t.Fatalf("usage=%+v; want input=%d total=%d", u, want, want+25)
			}
		})
	}
}
