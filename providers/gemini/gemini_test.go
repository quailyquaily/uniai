package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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

	out, err := buildRequest(req, req.Model)
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

func TestBuildRequestMapsUserImageBase64Part(t *testing.T) {
	req := &chat.Request{
		Messages: []chat.Message{
			chat.UserParts(
				chat.TextPart("describe this"),
				chat.ImageBase64Part("image/png", "QUJD"),
			),
		},
	}

	out, err := buildRequest(req, req.Model)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if len(out.Contents) != 1 {
		t.Fatalf("expected one content entry, got %d", len(out.Contents))
	}
	if out.Contents[0].Role != "user" {
		t.Fatalf("expected user role, got %q", out.Contents[0].Role)
	}
	if len(out.Contents[0].Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(out.Contents[0].Parts))
	}
	if out.Contents[0].Parts[0].Text != "describe this" {
		t.Fatalf("unexpected text part: %#v", out.Contents[0].Parts[0])
	}
	if out.Contents[0].Parts[1].InlineData == nil {
		t.Fatalf("expected inlineData part")
	}
	if out.Contents[0].Parts[1].InlineData.MimeType != "image/png" || out.Contents[0].Parts[1].InlineData.Data != "QUJD" {
		t.Fatalf("unexpected inlineData payload: %#v", out.Contents[0].Parts[1].InlineData)
	}
}

func TestBuildRequestRejectsUserImageURLPart(t *testing.T) {
	req := &chat.Request{
		Messages: []chat.Message{
			chat.UserParts(
				chat.ImageURLPart("https://example.com/a.png"),
			),
		},
	}
	_, err := buildRequest(req, req.Model)
	if err == nil {
		t.Fatalf("expected unsupported image_url error")
	}
	if !strings.Contains(err.Error(), "unsupported part type") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildRequestMapsReasoningEffortForGemini3(t *testing.T) {
	req := &chat.Request{
		Model: "gemini-3.0-pro",
		Messages: []chat.Message{
			chat.User("hello"),
		},
		Options: chat.Options{
			ReasoningEffort: func() *chat.ReasoningEffort {
				v := chat.ReasoningEffortHigh
				return &v
			}(),
			ReasoningDetails: true,
		},
	}

	out, err := buildRequest(req, req.Model)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if out.GenerationConfig == nil || out.GenerationConfig.ThinkingConfig == nil {
		t.Fatalf("expected thinking config")
	}
	if got := out.GenerationConfig.ThinkingConfig.ThinkingLevel; got != "high" {
		t.Fatalf("unexpected thinking level: %q", got)
	}
	if out.GenerationConfig.ThinkingConfig.IncludeThoughts == nil || !*out.GenerationConfig.ThinkingConfig.IncludeThoughts {
		t.Fatalf("expected includeThoughts=true")
	}
}

func TestBuildRequestMapsReasoningBudgetForGemini25(t *testing.T) {
	budget := 4096
	req := &chat.Request{
		Model: "gemini-2.5-pro",
		Messages: []chat.Message{
			chat.User("hello"),
		},
		Options: chat.Options{
			ReasoningBudget: &budget,
		},
	}

	out, err := buildRequest(req, req.Model)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if out.GenerationConfig == nil || out.GenerationConfig.ThinkingConfig == nil || out.GenerationConfig.ThinkingConfig.ThinkingBudget == nil {
		t.Fatalf("expected thinking budget")
	}
	if got := *out.GenerationConfig.ThinkingConfig.ThinkingBudget; got != budget {
		t.Fatalf("unexpected thinking budget: %d", got)
	}
}

func TestBuildRequestRejectsReasoningBudgetForGemini3(t *testing.T) {
	budget := 1024
	req := &chat.Request{
		Model: "gemini-3.0-pro",
		Messages: []chat.Message{
			chat.User("hello"),
		},
		Options: chat.Options{
			ReasoningBudget: &budget,
		},
	}

	_, err := buildRequest(req, req.Model)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "reasoning effort") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildRequestRejectsEffortAndBudgetTogether(t *testing.T) {
	budget := 4096
	req := &chat.Request{
		Model: "gemini-2.5-pro",
		Messages: []chat.Message{
			chat.User("hello"),
		},
		Options: chat.Options{
			ReasoningBudget: &budget,
			ReasoningEffort: func() *chat.ReasoningEffort {
				v := chat.ReasoningEffortLow
				return &v
			}(),
		},
	}

	_, err := buildRequest(req, req.Model)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "together") {
		t.Fatalf("unexpected error: %v", err)
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

	out, err := toChatResult(in, "", false)
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

func TestToChatResultKeepsLaterParallelToolCallSignatureEmpty(t *testing.T) {
	in := &geminiResponse{
		Model: "gemini-3.1-pro-preview",
		Candidates: []geminiCandidate{
			{
				Content: geminiContent{
					Parts: []geminiPart{
						{
							ThoughtSignature: "sig_parallel",
							FunctionCall: &geminiFunctionCall{
								Name: "read_file",
								Args: map[string]any{"path": "/tmp/a.txt"},
							},
						},
						{
							FunctionCall: &geminiFunctionCall{
								Name: "read_file",
								Args: map[string]any{"path": "/tmp/b.txt"},
							},
						},
					},
				},
			},
		},
	}

	out, err := toChatResult(in, "", false)
	if err != nil {
		t.Fatalf("toChatResult: %v", err)
	}
	if len(out.ToolCalls) != 2 {
		t.Fatalf("expected two tool calls, got %d", len(out.ToolCalls))
	}
	if out.ToolCalls[0].ThoughtSignature != "sig_parallel" {
		t.Fatalf("first thought signature = %q, want %q", out.ToolCalls[0].ThoughtSignature, "sig_parallel")
	}
	if out.ToolCalls[1].ThoughtSignature != "" {
		t.Fatalf("second thought signature = %q, want empty", out.ToolCalls[1].ThoughtSignature)
	}
	if out.ToolCalls[1].ID != "call_2" {
		t.Fatalf("second tool call id = %q, want %q", out.ToolCalls[1].ID, "call_2")
	}
}

func TestToChatResultSeparatesThoughtSummary(t *testing.T) {
	in := &geminiResponse{
		Model: "gemini-2.5-pro",
		Candidates: []geminiCandidate{
			{
				Content: geminiContent{
					Parts: []geminiPart{
						{Text: "internal summary", Thought: true},
						{Text: "visible answer"},
					},
				},
			},
		},
	}

	out, err := toChatResult(in, "", true)
	if err != nil {
		t.Fatalf("toChatResult: %v", err)
	}
	if out.Text != "visible answer" {
		t.Fatalf("unexpected visible text: %q", out.Text)
	}
	if out.Reasoning == nil || len(out.Reasoning.Summary) != 1 || out.Reasoning.Summary[0] != "internal summary" {
		t.Fatalf("unexpected reasoning summary: %#v", out.Reasoning)
	}
}

func TestChatStreamsThoughtTextAndToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models/gemini-2.5-pro:streamGenerateContent" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("alt") != "sse" || r.URL.Query().Get("key") != "test-key" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}
		if !strings.Contains(string(body), `"includeThoughts":true`) {
			t.Fatalf("request did not enable thought summaries: %s", body)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		writeGeminiSSE(t, w, map[string]any{
			"modelVersion": "gemini-2.5-pro",
			"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{
				map[string]any{"thought": true, "text": "inspect "},
			}}}},
		})
		writeGeminiSSE(t, w, map[string]any{
			"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{
				map[string]any{"thought": true, "text": "first", "thoughtSignature": "summary_sig"},
			}}}},
		})
		writeGeminiSSE(t, w, map[string]any{
			"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{
				map[string]any{"text": "answer"},
			}}}},
		})
		writeGeminiSSE(t, w, map[string]any{
			"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{
				map[string]any{"text": " "},
			}}}},
		})
		writeGeminiSSE(t, w, map[string]any{
			"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{
				map[string]any{
					"thoughtSignature": "sig_stream",
					"functionCall": map[string]any{
						"name": "read_file",
						"args": map[string]any{"path": "README.md"},
					},
				},
				map[string]any{"text": "done"},
			}}}},
		})
		writeGeminiSSE(t, w, map[string]any{
			"usageMetadata": map[string]any{
				"promptTokenCount":     3,
				"candidatesTokenCount": 4,
				"totalTokenCount":      7,
				"thoughtsTokenCount":   2,
			},
		})
	}))
	defer server.Close()

	p, err := New(Config{
		APIKey:       "test-key",
		BaseURL:      server.URL,
		DefaultModel: "gemini-2.5-pro",
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}

	var events []chat.StreamEvent
	result, err := p.Chat(context.Background(), &chat.Request{
		Messages: []chat.Message{chat.User("inspect")},
		Options: chat.Options{
			ReasoningDetails: true,
			OnStream: func(event chat.StreamEvent) error {
				events = append(events, event)
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}

	if len(events) != 7 {
		t.Fatalf("unexpected stream events: %#v", events)
	}
	if events[0].ReasoningDelta == nil || events[0].ReasoningDelta.Index != 0 || events[0].ReasoningDelta.Type != chat.ReasoningDeltaSummary || events[0].ReasoningDelta.Delta != "inspect " {
		t.Fatalf("unexpected first reasoning event: %#v", events[0])
	}
	if events[1].ReasoningDelta == nil || events[1].ReasoningDelta.Index != 0 || events[1].ReasoningDelta.Delta != "first" {
		t.Fatalf("unexpected second reasoning event: %#v", events[1])
	}
	if events[2].Delta != "answer" || events[3].Delta != " " || events[4].ToolCallDelta == nil || events[4].ToolCallDelta.Name != "read_file" || events[5].Delta != "done" || !events[6].Done {
		t.Fatalf("unexpected normalized event order: %#v", events)
	}
	for _, event := range events[:6] {
		if event.Raw == nil {
			t.Fatalf("content event does not preserve raw chunk: %#v", event)
		}
	}

	blocking, err := toChatResult(&geminiResponse{
		Model: "gemini-2.5-pro",
		Usage: geminiUsage{InputTokens: 3, OutputTokens: 4, TotalTokens: 7, ThoughtsTokens: 2},
		Candidates: []geminiCandidate{{Content: geminiContent{Parts: []geminiPart{
			{Thought: true, Text: "inspect first"},
			{Text: "answer "},
			{
				ThoughtSignature: "sig_stream",
				FunctionCall: &geminiFunctionCall{
					Name: "read_file",
					Args: map[string]any{"path": "README.md"},
				},
			},
			{Text: "done"},
		}}}},
	}, "", true)
	if err != nil {
		t.Fatalf("blocking result: %v", err)
	}
	if result.Text != blocking.Text || result.Model != blocking.Model || result.Usage.InputTokens != blocking.Usage.InputTokens || result.Usage.OutputTokens != blocking.Usage.OutputTokens || result.Usage.TotalTokens != blocking.Usage.TotalTokens {
		t.Fatalf("stream and blocking result differ: stream=%#v blocking=%#v", result, blocking)
	}
	if result.Reasoning == nil || blocking.Reasoning == nil || len(result.Reasoning.Summary) != 1 || result.Reasoning.Summary[0] != blocking.Reasoning.Summary[0] {
		t.Fatalf("stream and blocking reasoning differ: stream=%#v blocking=%#v", result.Reasoning, blocking.Reasoning)
	}
	if len(result.ToolCalls) != 1 || len(blocking.ToolCalls) != 1 || result.ToolCalls[0].ID != blocking.ToolCalls[0].ID || result.ToolCalls[0].ThoughtSignature != "sig_stream" {
		t.Fatalf("stream and blocking tool calls differ: stream=%#v blocking=%#v", result.ToolCalls, blocking.ToolCalls)
	}
}

func TestChatStreamReturnsEventErrorWithoutDone(t *testing.T) {
	p := &Provider{}
	gotDone := false
	_, err := p.chatStream(
		strings.NewReader("data: {\"error\":{\"message\":\"quota exceeded\"}}\n\n"),
		"gemini-2.5-pro",
		true,
		func(event chat.StreamEvent) error {
			gotDone = gotDone || event.Done
			return nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "quota exceeded") {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotDone {
		t.Fatalf("failed stream emitted Done")
	}
}

func writeGeminiSSE(t *testing.T, w http.ResponseWriter, payload map[string]any) {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal SSE payload: %v", err)
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		t.Fatalf("write SSE payload: %v", err)
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
	toolResult, err := chat.ToolResultValue(callID, "ok")
	if err != nil {
		t.Fatalf("tool result value: %v", err)
	}
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
			toolResult,
		},
	}

	out, err := buildRequest(req, req.Model)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if len(out.Contents) < 2 || len(out.Contents[1].Parts) == 0 || out.Contents[1].Parts[0].FunctionCall == nil {
		t.Fatalf("expected assistant functionCall content")
	}
	if out.Contents[1].Parts[0].ThoughtSignature != "sig_from_id" {
		t.Fatalf("expected thought signature decoded from id")
	}
	resp, ok := out.Contents[2].Parts[0].FunctionResponse.Response.(map[string]any)
	if !ok {
		t.Fatalf("expected function response object, got %#v", out.Contents[2].Parts[0].FunctionResponse.Response)
	}
	if resp["result"] != "ok" {
		t.Fatalf("unexpected function response payload: %#v", resp)
	}
}

func TestBuildRequestAllowsMissingThoughtSignatureOnLaterParallelToolCall(t *testing.T) {
	toolResultA, err := chat.ToolResultValue("call_1", "a")
	if err != nil {
		t.Fatalf("tool result value a: %v", err)
	}
	toolResultB, err := chat.ToolResultValue("call_2", "b")
	if err != nil {
		t.Fatalf("tool result value b: %v", err)
	}
	req := &chat.Request{
		Messages: []chat.Message{
			chat.User("run tools"),
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
						ThoughtSignature: "sig_parallel",
					},
					{
						ID:   "call_2",
						Type: "function",
						Function: chat.ToolCallFunction{
							Name:      "read_file",
							Arguments: `{"path":"/tmp/b.txt"}`,
						},
					},
				},
			},
			toolResultA,
			toolResultB,
		},
	}

	out, err := buildRequest(req, req.Model)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if len(out.Contents) < 3 {
		t.Fatalf("expected assistant and tool response contents, got %d", len(out.Contents))
	}
	if len(out.Contents[1].Parts) != 2 {
		t.Fatalf("expected two assistant functionCall parts, got %d", len(out.Contents[1].Parts))
	}
	if out.Contents[1].Parts[0].ThoughtSignature != "sig_parallel" {
		t.Fatalf("first thought signature = %q, want %q", out.Contents[1].Parts[0].ThoughtSignature, "sig_parallel")
	}
	if out.Contents[1].Parts[1].ThoughtSignature != "" {
		t.Fatalf("second thought signature = %q, want empty", out.Contents[1].Parts[1].ThoughtSignature)
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

	_, err := buildRequest(req, req.Model)
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
