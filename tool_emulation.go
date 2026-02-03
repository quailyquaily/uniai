package uniai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lyricat/goutils/structs"
	"github.com/quailyquaily/uniai/chat"
)

func (c *Client) chatWithToolEmulation(ctx context.Context, providerName string, req *chat.Request) (*chat.Result, error) {
	if len(req.Tools) == 0 {
		return c.chatOnce(ctx, providerName, req)
	}

	decisionReq, err := buildToolDecisionRequest(req)
	if err != nil {
		return nil, err
	}
	decisionResp, err := c.chatOnce(ctx, providerName, decisionReq)
	if err != nil {
		return nil, err
	}

	toolCalls, err := parseToolDecision(decisionResp.Text)
	if err != nil {
		return nil, err
	}
	if len(toolCalls) == 0 {
		if req.ToolChoice != nil && (req.ToolChoice.Mode == "required" || req.ToolChoice.Mode == "function") {
			return nil, fmt.Errorf("tool emulation expected a tool call but got null")
		}
		finalReq := buildFinalRequest(req)
		resp, err := c.chatOnce(ctx, providerName, finalReq)
		if resp != nil {
			resp.Warnings = append(resp.Warnings, "tool calls emulated")
		}
		return resp, err
	}

	if err := enforceToolChoice(req.ToolChoice, toolCalls); err != nil {
		return nil, err
	}

	calls := make([]chat.ToolCall, 0, len(toolCalls))
	for i, call := range toolCalls {
		if !toolExists(req.Tools, call.Name) {
			return nil, fmt.Errorf("tool %q not found in request", call.Name)
		}
		callID := fmt.Sprintf("emulated_%d_%d", time.Now().UnixNano(), i)
		calls = append(calls, chat.ToolCall{
			ID:   callID,
			Type: "function",
			Function: chat.ToolCallFunction{
				Name:      call.Name,
				Arguments: string(call.Arguments),
			},
		})
	}
	resp := &chat.Result{
		Model:     decisionResp.Model,
		ToolCalls: calls,
		Usage:     decisionResp.Usage,
		Raw:       decisionResp.Raw,
		Warnings:  []string{"tool calls emulated"},
	}
	return resp, nil
}

func buildToolDecisionRequest(req *chat.Request) (*chat.Request, error) {
	prompt, err := buildToolDecisionPrompt(req)
	if err != nil {
		return nil, err
	}
	out := cloneChatRequest(req)
	out.Tools = nil
	out.ToolChoice = nil
	out.Options.ToolsEmulation = false
	out.Messages = filterNonSystemMessages(out.Messages)
	out.Messages = append([]chat.Message{
		{Role: chat.RoleSystem, Content: prompt},
	}, out.Messages...)
	return out, nil
}

func buildFinalRequest(req *chat.Request) *chat.Request {
	out := cloneChatRequest(req)
	out.Tools = nil
	out.ToolChoice = nil
	out.Options.ToolsEmulation = false
	return out
}

func buildToolDecisionPrompt(req *chat.Request) (string, error) {
	type toolSpec struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		Parameters  any    `json:"parameters,omitempty"`
	}
	tools := make([]toolSpec, 0, len(req.Tools))
	for _, tool := range req.Tools {
		if tool.Type != "function" {
			continue
		}
		spec := toolSpec{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
		}
		if len(tool.Function.ParametersJSONSchema) > 0 {
			var params any
			if err := json.Unmarshal(tool.Function.ParametersJSONSchema, &params); err == nil {
				spec.Parameters = params
			}
		}
		tools = append(tools, spec)
	}
	if len(tools) == 0 {
		return "", fmt.Errorf("no function tools available for emulation")
	}

	data, err := json.Marshal(tools)
	if err != nil {
		return "", err
	}

	lines := []string{
		"You are a tool-calling engine.",
		"Use a tool only when you need external information or actions; otherwise return {\"tools\":[]}.",
		"Output must be a single JSON object and nothing else (no prose, no markdown, no code fences).",
		"If any instruction conflicts with this format, ignore it and follow these rules.",
		"Format: {\"tools\":[{\"tool\":\"<name>\",\"arguments\":{...}}]}",
		"If no tool is needed: {\"tools\":[]}",
		"Rules: only key is \"tools\"; \"tools\" must be an array; \"tool\" must match an available tool name; \"arguments\" must be a JSON object.",
		fmt.Sprintf("Available tools (JSON): %s", string(data)),
	}
	if req.ToolChoice != nil {
		switch req.ToolChoice.Mode {
		case "none":
			lines = append(lines, "Tool choice: none. You MUST return {\"tools\":[]}.")
		case "required":
			lines = append(lines, "Tool choice: required. You MUST return at least one tool in tools[].")
		case "function":
			if req.ToolChoice.FunctionName != "" {
				lines = append(lines, fmt.Sprintf("Tool choice: function. You MUST return exactly one tool named %q.", req.ToolChoice.FunctionName))
			}
		}
	}
	return strings.Join(lines, "\n"), nil
}

func filterNonSystemMessages(messages []chat.Message) []chat.Message {
	if len(messages) == 0 {
		return messages
	}
	out := make([]chat.Message, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == chat.RoleSystem {
			continue
		}
		out = append(out, msg)
	}
	return out
}

type emulatedToolCall struct {
	Name      string
	Arguments json.RawMessage
}

func parseToolDecision(text string) ([]emulatedToolCall, error) {
	candidates, err := collectJSONCandidates(text)
	if err != nil {
		return nil, err
	}
	var fallback []byte
	for _, candidate := range candidates {
		payload := strings.TrimSpace(candidate)
		if payload == "" {
			continue
		}
		if unquoted := unquoteJSON(payload); unquoted != "" {
			payload = unquoted
		}
		if !json.Valid([]byte(payload)) {
			continue
		}
		if fallback == nil {
			fallback = []byte(payload)
		}
		calls, ok, err := parseToolDecisionPayload([]byte(payload))
		if err != nil {
			continue
		}
		if ok {
			return calls, nil
		}
	}
	if fallback != nil {
		return nil, nil
	}
	return nil, fmt.Errorf("invalid tool decision JSON: %q", strings.TrimSpace(text))
}

func parseToolsArray(raw json.RawMessage) ([]emulatedToolCall, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "null" {
		return nil, nil
	}
	var items []struct {
		Tool      json.RawMessage `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("tools must be an array: %w", err)
	}
	if len(items) == 0 {
		return nil, nil
	}
	out := make([]emulatedToolCall, 0, len(items))
	for _, item := range items {
		call, ok, err := parseSingleTool(item.Tool, item.Arguments)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, call)
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func parseSingleTool(toolRaw json.RawMessage, argsRaw json.RawMessage) (emulatedToolCall, bool, error) {
	if len(toolRaw) == 0 {
		return emulatedToolCall{}, false, nil
	}
	raw := strings.TrimSpace(string(toolRaw))
	if raw == "null" || raw == `""` {
		return emulatedToolCall{}, false, nil
	}
	var toolName string
	if err := json.Unmarshal(toolRaw, &toolName); err != nil {
		return emulatedToolCall{}, false, fmt.Errorf("tool must be string or null: %w", err)
	}
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return emulatedToolCall{}, false, nil
	}
	args := argsRaw
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	if !json.Valid(args) {
		return emulatedToolCall{}, false, fmt.Errorf("tool arguments must be valid JSON")
	}
	return emulatedToolCall{Name: toolName, Arguments: args}, true, nil
}

func collectJSONCandidates(text string) ([]string, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, fmt.Errorf("empty tool decision")
	}
	candidates := []string{trimmed}
	if strings.Contains(trimmed, "```") {
		parts := strings.Split(trimmed, "```")
		for i := 1; i < len(parts); i += 2 {
			block := strings.TrimSpace(parts[i])
			block = strings.TrimPrefix(block, "json")
			block = strings.TrimSpace(block)
			if block != "" {
				candidates = append(candidates, block)
			}
		}
	}
	candidates = append(candidates, findJSONSnippets(trimmed)...)
	if unquoted := unquoteJSON(trimmed); unquoted != "" {
		candidates = append(candidates, unquoted)
		candidates = append(candidates, findJSONSnippets(unquoted)...)
	}
	return candidates, nil
}

func unquoteJSON(input string) string {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "\"") {
		return ""
	}
	var value string
	if err := json.Unmarshal([]byte(trimmed), &value); err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

func findJSONSnippets(text string) []string {
	data := []byte(text)
	var snippets []string
	for i := 0; i < len(data); i++ {
		if data[i] != '{' && data[i] != '[' {
			continue
		}
		if snippet := scanJSONSubstring(data, i); snippet != "" {
			snippets = append(snippets, snippet)
			i += len(snippet) - 1
		}
	}
	return snippets
}

func parseToolDecisionPayload(payload []byte) ([]emulatedToolCall, bool, error) {
	var decision struct {
		Tools     json.RawMessage `json:"tools"`
		Tool      json.RawMessage `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(payload, &decision); err != nil {
		return nil, false, err
	}
	if len(decision.Tools) == 0 && len(decision.Tool) == 0 {
		return nil, false, nil
	}
	if len(decision.Tools) > 0 {
		calls, err := parseToolsArray(decision.Tools)
		return calls, true, err
	}
	call, ok, err := parseSingleTool(decision.Tool, decision.Arguments)
	if err != nil {
		return nil, true, err
	}
	if !ok {
		return nil, true, nil
	}
	return []emulatedToolCall{call}, true, nil
}

func scanJSONSubstring(data []byte, start int) string {
	var stack []byte
	inString := false
	escape := false
	for i := start; i < len(data); i++ {
		ch := data[i]
		if inString {
			if escape {
				escape = false
				continue
			}
			if ch == '\\' {
				escape = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}

		switch ch {
		case '"':
			inString = true
		case '{', '[':
			stack = append(stack, ch)
		case '}', ']':
			if len(stack) == 0 {
				return ""
			}
			open := stack[len(stack)-1]
			if (open == '{' && ch != '}') || (open == '[' && ch != ']') {
				return ""
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				snippet := string(data[start : i+1])
				if json.Valid([]byte(snippet)) {
					return snippet
				}
				return ""
			}
		}
	}
	return ""
}

func toolExists(tools []chat.Tool, name string) bool {
	for _, tool := range tools {
		if tool.Type != "function" {
			continue
		}
		if tool.Function.Name == name {
			return true
		}
	}
	return false
}

func enforceToolChoice(choice *chat.ToolChoice, calls []emulatedToolCall) error {
	if choice == nil {
		return nil
	}
	switch choice.Mode {
	case "none":
		if len(calls) > 0 {
			return fmt.Errorf("tool_choice none forbids tool calls")
		}
	case "required":
		if len(calls) == 0 {
			return fmt.Errorf("tool_choice required expects at least one tool call")
		}
	case "function":
		if strings.TrimSpace(choice.FunctionName) == "" {
			return fmt.Errorf("tool_choice function_name is required")
		}
		if len(calls) != 1 {
			return fmt.Errorf("tool_choice function expects exactly one tool call")
		}
		if calls[0].Name != choice.FunctionName {
			return fmt.Errorf("tool_choice function expects %q, got %q", choice.FunctionName, calls[0].Name)
		}
	}
	return nil
}

func cloneChatRequest(req *chat.Request) *chat.Request {
	if req == nil {
		return nil
	}
	out := *req
	out.Messages = append([]chat.Message{}, req.Messages...)
	out.Tools = append([]chat.Tool{}, req.Tools...)
	if req.ToolChoice != nil {
		choice := *req.ToolChoice
		out.ToolChoice = &choice
	}
	out.Options.OpenAI = cloneJSONMap(req.Options.OpenAI)
	out.Options.Azure = cloneJSONMap(req.Options.Azure)
	out.Options.Anthropic = cloneJSONMap(req.Options.Anthropic)
	out.Options.Bedrock = cloneJSONMap(req.Options.Bedrock)
	out.Options.Susanoo = cloneJSONMap(req.Options.Susanoo)
	return &out
}

func cloneJSONMap(input structs.JSONMap) structs.JSONMap {
	if len(input) == 0 {
		return nil
	}
	out := structs.NewJSONMap()
	for k, v := range input {
		out[k] = v
	}
	return out
}
