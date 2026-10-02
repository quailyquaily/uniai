package chat

import "testing"

func TestBuildTokenCountRequest(t *testing.T) {
	for _, opts := range [][]Option{nil, {WithTools([]Tool{FunctionTool("tool", "desc", []byte(`{"type":"object"}`))})}, {WithMessages(System("system"))}} {
		if _, err := BuildTokenCountRequest(opts...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := BuildRequest(); err == nil {
		t.Fatal("Chat accepted empty messages")
	}
	invalid := WithTools([]Tool{WithToolCacheControl(FunctionTool("tool", "desc", nil), CacheControl{TTL: "bad"})})
	if _, err := BuildTokenCountRequest(invalid); err == nil {
		t.Fatal("count skipped cache validation")
	}
}
