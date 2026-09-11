package claudecode

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestPreparePreservesCallerSemantics(t *testing.T) {
	input := []byte(`{"model":"claude-sonnet-4-6","system":[{"type":"text","text":"Keep these rules.","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"tool_1","name":"my_tool","input":{"n":9007199254740993}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool_1","content":"ok"}]}],"tools":[{"name":"my_tool","input_schema":{"type":"object"}}],"tool_choice":{"type":"tool","name":"my_tool"},"metadata":{"user_id":"caller","other":"keep"}}`)
	before := string(input)
	p := Profile{SessionID: "00000000-0000-4000-8000-000000000001"}
	data, session, err := p.Prepare(input, "account")
	if err != nil {
		t.Fatal(err)
	}
	if string(input) != before || session != p.SessionID {
		t.Fatal("input mutated or session lost")
	}
	var original, actual map[string]json.RawMessage
	json.Unmarshal(input, &original)
	json.Unmarshal(data, &actual)
	for _, key := range []string{"messages", "tools", "tool_choice", "model"} {
		var a, b any
		json.Unmarshal(original[key], &a)
		json.Unmarshal(actual[key], &b)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("%s changed", key)
		}
	}
	if !strings.Contains(string(data), "9007199254740993") {
		t.Fatal("integer precision lost")
	}
	var system []map[string]any
	json.Unmarshal(actual["system"], &system)
	if len(system) != 2 || system[0]["text"] != Identity || system[1]["text"] != "Keep these rules." || system[1]["cache_control"] == nil {
		t.Fatalf("system = %#v", system)
	}
	var metadata map[string]string
	json.Unmarshal(actual["metadata"], &metadata)
	var identity map[string]string
	if err := json.Unmarshal([]byte(metadata["user_id"]), &identity); err != nil {
		t.Fatal(err)
	}
	if identity["account_uuid"] != "account" || identity["session_id"] != session || len(identity["device_id"]) != 64 || metadata["other"] != "keep" {
		t.Fatalf("metadata = %#v", metadata)
	}
	again, _, err := p.Prepare(data, "account")
	if err != nil || string(again) != string(data) {
		t.Fatalf("not idempotent: %v", err)
	}
}

func TestProfileHeadersProtectCredentialsAndMergeBetas(t *testing.T) {
	header := http.Header{"authorization": {"wrong"}, "X-Api-Key": {"wrong"}, "Proxy-Authorization": {"wrong"}, "Anthropic-Beta": {"custom-beta,oauth-2025-04-20"}, "User-Agent": {"wrong"}, "X-Trace": {"keep"}}
	Profile{}.ApplyHeaders(header, "access", "session")
	if header.Get("Authorization") != "Bearer access" || header.Get("X-Api-Key") != "" || header.Get("Proxy-Authorization") != "" || header.Get("X-Trace") != "keep" {
		t.Fatal("incorrect header protection")
	}
	if header.Get("X-App") != "cli" || header.Get("X-Claude-Code-Session-Id") != "session" || !strings.HasPrefix(header.Get("User-Agent"), "claude-cli/") {
		t.Fatal("missing CLI headers")
	}
	firstRequestID := header.Get("X-Client-Request-Id")
	Profile{}.ApplyHeaders(header, "access", "session")
	if len(firstRequestID) != 36 || header.Get("X-Client-Request-Id") == firstRequestID {
		t.Fatal("missing per-attempt request ID")
	}
	beta := header.Get("Anthropic-Beta")
	if strings.Count(beta, "oauth-2025-04-20") != 1 || !strings.Contains(beta, "claude-code-20250219") || !strings.Contains(beta, "custom-beta") {
		t.Fatalf("beta = %q", beta)
	}
}

func TestPrepareValidatesSystemAndCreatesSession(t *testing.T) {
	for _, input := range []string{`null`, `[]`, `{"system":12}`, `{"system":[1]}`} {
		if _, _, err := (Profile{}).Prepare([]byte(input), "account"); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
	for _, input := range []string{`{}`, `{"system":"rules"}`, `{"system":null}`} {
		_, a, err := (Profile{}).Prepare([]byte(input), "account")
		if err != nil {
			t.Fatal(err)
		}
		_, b, _ := (Profile{}).Prepare([]byte(input), "account")
		if len(a) != 36 || a == b {
			t.Fatal("invalid session identity")
		}
	}
}
