package gemini

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lyricat/goutils/structs"
	"github.com/quailyquaily/uniai/chat"
)

func TestBuildRequestMapsToolsAndThoughtSignature(t *testing.T) {
	temperature := 0.2
	maxTokens := 128
	openaiOpts := structs.NewJSONMap()
	openaiOpts.SetValue("top_k", 12)
	req := &chat.Request{
		Messages: []chat.Message{
			chat.System("You are a helper."),
			chat.User("Read /tmp/a.txt"),
			{
				Role: chat.RoleAssistant,
				ToolCalls: []chat.ToolCall{
					{
						ID:   "call_1",
						Type: "function",
						Function: chat.ToolCallFunction{
							Name:      "read_file",
							Arguments: `{"path":"/tmp/a.txt"}`,
						},
						ThoughtSignature: "sig_abc",
					},
				},
			},
			chat.ToolResult("call_1", `{"content":"hello"}`),
		},
		Options: chat.Options{
			Temperature: &temperature,
			MaxTokens:   &maxTokens,
			OpenAI:      openaiOpts,
		},
		Tools: []chat.Tool{
			chat.FunctionTool("read_file", "Read local file", []byte(`{
				"type":"object",
				"properties":{"path":{"type":"string"}},
				"required":["path"]
			}`)),
		},
		ToolChoice: func() *chat.ToolChoice {
			c := chat.ToolChoiceFunction("read_file")
			return &c
		}(),
	}

	out, err := buildRequest(req)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if out.SystemInstruction == nil || len(out.SystemInstruction.Parts) != 1 {
		t.Fatalf("expected system instruction")
	}
	if len(out.Contents) != 3 {
		t.Fatalf("expected 3 contents, got %d", len(out.Contents))
	}
	if out.Contents[1].Role != "model" || len(out.Contents[1].Parts) != 1 || out.Contents[1].Parts[0].FunctionCall == nil {
		t.Fatalf("expected model functionCall part")
	}
	if out.Contents[1].Parts[0].ThoughtSignature != "sig_abc" {
		t.Fatalf("expected thought signature to be forwarded")
	}
	if out.Contents[2].Role != "user" || len(out.Contents[2].Parts) != 1 || out.Contents[2].Parts[0].FunctionResponse == nil {
		t.Fatalf("expected user functionResponse part")
	}
	if out.Contents[2].Parts[0].FunctionResponse.Name != "read_file" {
		t.Fatalf("expected function response name to match tool call")
	}
	if out.ToolConfig == nil || out.ToolConfig.FunctionCallingConfig == nil {
		t.Fatalf("expected tool config")
	}
	if out.ToolConfig.FunctionCallingConfig.Mode != "ANY" {
		t.Fatalf("expected ANY mode for ToolChoiceFunction")
	}
	if len(out.ToolConfig.FunctionCallingConfig.AllowedFunctionNames) != 1 || out.ToolConfig.FunctionCallingConfig.AllowedFunctionNames[0] != "read_file" {
		t.Fatalf("expected allowed function names")
	}
	if out.GenerationConfig == nil || out.GenerationConfig.TopK == nil || *out.GenerationConfig.TopK != 12 {
		t.Fatalf("expected top_k option to map to generation config")
	}

	if len(out.Tools) != 1 || len(out.Tools[0].FunctionDeclarations) != 1 {
		t.Fatalf("expected one function declaration")
	}
	params, ok := out.Tools[0].FunctionDeclarations[0].Parameters.(map[string]any)
	if !ok {
		t.Fatalf("expected parameters map")
	}
	if params["type"] != "OBJECT" {
		t.Fatalf("expected OBJECT schema type, got %#v", params["type"])
	}
}

func TestToChatResultParsesFunctionCallAndSignature(t *testing.T) {
	in := &geminiResponse{
		Model: "gemini-2.5-pro",
		Usage: geminiUsage{InputTokens: 10, OutputTokens: 4, TotalTokens: 14},
		Candidates: []geminiCandidate{
			{
				Content: geminiContent{
					Parts: []geminiPart{
						{Text: "I will call a tool."},
						{
							ThoughtSignature: "sig_xyz",
							FunctionCall: &geminiFunctionCall{
								Name: "read_file",
								Args: map[string]any{"path": "/tmp/a.txt"},
							},
						},
					},
				},
			},
		},
	}

	out, err := toChatResult(in, "")
	if err != nil {
		t.Fatalf("toChatResult: %v", err)
	}
	if out.Text != "I will call a tool." {
		t.Fatalf("unexpected text: %q", out.Text)
	}
	if len(out.ToolCalls) != 1 {
		t.Fatalf("expected one tool call")
	}
	if out.ToolCalls[0].Function.Name != "read_file" {
		t.Fatalf("unexpected tool name: %q", out.ToolCalls[0].Function.Name)
	}
	if out.ToolCalls[0].ThoughtSignature != "sig_xyz" {
		t.Fatalf("missing thought signature")
	}
	if out.ToolCalls[0].ID == "" || out.ToolCalls[0].ID == "call_2" {
		t.Fatalf("expected encoded call id with thought signature, got %q", out.ToolCalls[0].ID)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(out.ToolCalls[0].Function.Arguments), &args); err != nil {
		t.Fatalf("invalid tool args json: %v", err)
	}
	if args["path"] != "/tmp/a.txt" {
		t.Fatalf("unexpected args: %#v", args)
	}
}

func TestToGeminiSchemaConvertsTypeUnion(t *testing.T) {
	in := map[string]any{
		"type": []any{"string", "null"},
	}
	out, ok := toGeminiSchema(in).(map[string]any)
	if !ok {
		t.Fatalf("expected map schema")
	}
	if out["type"] != "STRING" {
		t.Fatalf("expected STRING, got %#v", out["type"])
	}
	if out["nullable"] != true {
		t.Fatalf("expected nullable=true")
	}
}

func TestToGeminiSchemaDropsAdditionalProperties(t *testing.T) {
	in := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"headers": map[string]any{
				"type": "object",
				"additionalProperties": map[string]any{
					"type": "string",
				},
			},
		},
	}

	out, ok := toGeminiSchema(in).(map[string]any)
	if !ok {
		t.Fatalf("expected map schema")
	}
	if _, exists := out["additionalProperties"]; exists {
		t.Fatalf("expected root additionalProperties to be removed")
	}
	props, ok := out["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected properties map")
	}
	headers, ok := props["headers"].(map[string]any)
	if !ok {
		t.Fatalf("expected nested headers schema map")
	}
	if _, exists := headers["additionalProperties"]; exists {
		t.Fatalf("expected nested additionalProperties to be removed")
	}
}

func TestNormalizeGeminiBaseStripsOpenAICompatSuffix(t *testing.T) {
	got := normalizeGeminiBase("https://generativelanguage.googleapis.com/v1beta/openai")
	if got != "https://generativelanguage.googleapis.com" {
		t.Fatalf("unexpected base: %s", got)
	}
}

func TestBuildRequestRecoversThoughtSignatureFromToolCallID(t *testing.T) {
	callID := encodeToolCallID("call_1", "sig_from_id")
	req := &chat.Request{
		Messages: []chat.Message{
			chat.User("run tool"),
			{
				Role: chat.RoleAssistant,
				ToolCalls: []chat.ToolCall{
					{
						ID:   callID,
						Type: "function",
						Function: chat.ToolCallFunction{
							Name:      "read_file",
							Arguments: `{"path":"/tmp/a.txt"}`,
						},
					},
				},
			},
			chat.ToolResult(callID, `{"content":"ok"}`),
		},
	}

	out, err := buildRequest(req)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if len(out.Contents) < 2 || len(out.Contents[1].Parts) == 0 || out.Contents[1].Parts[0].FunctionCall == nil {
		t.Fatalf("expected assistant functionCall content")
	}
	if out.Contents[1].Parts[0].ThoughtSignature != "sig_from_id" {
		t.Fatalf("expected thought signature decoded from id")
	}
}

func TestBuildRequestFailsWithoutThoughtSignature(t *testing.T) {
	req := &chat.Request{
		Messages: []chat.Message{
			chat.User("run tool"),
			{
				Role: chat.RoleAssistant,
				ToolCalls: []chat.ToolCall{
					{
						ID:   "call_1",
						Type: "function",
						Function: chat.ToolCallFunction{
							Name:      "read_file",
							Arguments: `{"path":"/tmp/a.txt"}`,
						},
					},
				},
			},
		},
	}

	_, err := buildRequest(req)
	if err == nil {
		t.Fatalf("expected missing thought signature error")
	}
	if got := err.Error(); got == "" || !containsAll(got, "missing thought_signature", "read_file") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func containsAll(haystack string, needles ...string) bool {
	for _, n := range needles {
		if !strings.Contains(haystack, n) {
			return false
		}
	}
	return true
}
