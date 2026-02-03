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
		"When you need a tool, output ONLY JSON:",
		`{"tools": [{"tool": "get_weather", "arguments": {"city": "Tokyo"}}]}`,
		"If no tool needed, output:",
		`{"tools": []}`,
		fmt.Sprintf("Available tools (JSON): %s", string(data)),
	}
	if req.ToolChoice != nil {
		switch req.ToolChoice.Mode {
		case "none":
			lines = append(lines, "You MUST NOT call any tool. Return tools=[].")
		case "required":
			lines = append(lines, "You MUST call at least one tool. tools must not be empty.")
		case "function":
			if req.ToolChoice.FunctionName != "" {
				lines = append(lines, fmt.Sprintf("You MUST call the tool named %q. tools must contain exactly one item.", req.ToolChoice.FunctionName))
			}
		}
	}
	return strings.Join(lines, "\n"), nil
}

type emulatedToolCall struct {
	Name      string
	Arguments json.RawMessage
}

func parseToolDecision(text string) ([]emulatedToolCall, error) {
	payload, err := extractJSONPayload(text)
	if err != nil {
		return nil, err
	}
	var decision struct {
		Tools     json.RawMessage `json:"tools"`
		Tool      json.RawMessage `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(payload, &decision); err != nil {
		return nil, err
	}
	if len(decision.Tools) > 0 {
		return parseToolsArray(decision.Tools)
	}
	if len(decision.Tool) == 0 {
		return nil, nil
	}
	call, ok, err := parseSingleTool(decision.Tool, decision.Arguments)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	return []emulatedToolCall{call}, nil
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

func extractJSONPayload(text string) ([]byte, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, fmt.Errorf("empty tool decision")
	}
	if strings.HasPrefix(trimmed, "```") {
		parts := strings.SplitN(trimmed, "```", 3)
		if len(parts) >= 2 {
			trimmed = strings.TrimSpace(parts[1])
			trimmed = strings.TrimPrefix(trimmed, "json")
			trimmed = strings.TrimSpace(trimmed)
		}
	}
	if json.Valid([]byte(trimmed)) {
		return []byte(trimmed), nil
	}
	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start >= 0 && end > start {
		candidate := trimmed[start : end+1]
		if json.Valid([]byte(candidate)) {
			return []byte(candidate), nil
		}
	}
	return nil, fmt.Errorf("invalid tool decision JSON: %q", trimmed)
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
