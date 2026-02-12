package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/lyricat/goutils/structs"
	"github.com/quailyquaily/uniai/chat"
	"github.com/quailyquaily/uniai/internal/diag"
	"github.com/quailyquaily/uniai/internal/httputil"
)

type Config struct {
	APIKey       string
	DefaultModel string
	Debug        bool
}

type Provider struct {
	cfg Config
}

func New(cfg Config) *Provider {
	return &Provider{cfg: cfg}
}

type anthropicMessage struct {
	Role    string                 `json:"role"`
	Content []anthropicContentPart `json:"content,omitempty"`
}

type anthropicContentPart struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Input     any    `json:"input,omitempty"`
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   any    `json:"content,omitempty"`
	IsError   *bool  `json:"is_error,omitempty"`
}

type anthropicRequest struct {
	Model         string               `json:"model"`
	System        string               `json:"system,omitempty"`
	Messages      []anthropicMessage   `json:"messages"`
	MaxTokens     int                  `json:"max_tokens"`
	Temperature   *float64             `json:"temperature,omitempty"`
	TopP          *float64             `json:"top_p,omitempty"`
	TopK          *int                 `json:"top_k,omitempty"`
	StopSequences []string             `json:"stop_sequences,omitempty"`
	Metadata      *anthropicMetadata   `json:"metadata,omitempty"`
	Tools         []anthropicTool      `json:"tools,omitempty"`
	ToolChoice    *anthropicToolChoice `json:"tool_choice,omitempty"`
	Stream        bool                 `json:"stream,omitempty"`
}

type anthropicResponse struct {
	Content    []anthropicContentPart `json:"content"`
	Model      string                 `json:"model"`
	StopReason string                 `json:"stop_reason,omitempty"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

type anthropicMetadata struct {
	UserID string `json:"user_id,omitempty"`
}

type anthropicTool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	InputSchema any    `json:"input_schema"`
}

type anthropicToolChoice struct {
	Type                   string `json:"type"`
	Name                   string `json:"name,omitempty"`
	DisableParallelToolUse *bool  `json:"disable_parallel_tool_use,omitempty"`
}

func (p *Provider) Chat(ctx context.Context, req *chat.Request) (*chat.Result, error) {
	debugFn := req.Options.DebugFn
	if p.cfg.APIKey == "" {
		return nil, fmt.Errorf("anthropic api key is required")
	}
	model := req.Model
	if model == "" {
		model = p.cfg.DefaultModel
	}
	if model == "" {
		return nil, fmt.Errorf("model is required")
	}

	systemParts := make([]string, 0, 1)
	messages := make([]anthropicMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		text, err := chat.MessageText(m)
		if err != nil {
			return nil, fmt.Errorf("anthropic provider model %q: role %q: %w", model, m.Role, err)
		}
		switch m.Role {
		case chat.RoleSystem:
			if text != "" {
				systemParts = append(systemParts, text)
			}
			continue
		case chat.RoleUser:
			msg := anthropicMessage{Role: "user"}
			if text != "" {
				msg.Content = append(msg.Content, anthropicContentPart{Type: "text", Text: text})
			}
			if len(msg.Content) > 0 {
				messages = append(messages, msg)
			}
		case chat.RoleAssistant:
			msg := anthropicMessage{Role: "assistant"}
			if text != "" {
				msg.Content = append(msg.Content, anthropicContentPart{Type: "text", Text: text})
			}
			if len(m.ToolCalls) > 0 {
				toolParts, err := toAnthropicToolUses(m.ToolCalls)
				if err != nil {
					return nil, err
				}
				msg.Content = append(msg.Content, toolParts...)
			}
			if len(msg.Content) > 0 {
				messages = append(messages, msg)
			}
		case chat.RoleTool:
			if m.ToolCallID == "" {
				return nil, fmt.Errorf("tool_call_id is required for tool messages")
			}
			messages = append(messages, anthropicMessage{
				Role: "user",
				Content: []anthropicContentPart{{
					Type:      "tool_result",
					ToolUseID: m.ToolCallID,
					Content:   text,
				}},
			})
		default:
			return nil, fmt.Errorf("anthropic provider does not support role %q", m.Role)
		}
	}
	if len(messages) == 0 {
		return nil, fmt.Errorf("at least one non-system message is required")
	}

	maxTokens := 8192
	if req.Options.MaxTokens != nil {
		maxTokens = *req.Options.MaxTokens
	}

	body := anthropicRequest{
		Model:         model,
		System:        strings.Join(systemParts, "\n"),
		Messages:      messages,
		MaxTokens:     maxTokens,
		Temperature:   req.Options.Temperature,
		TopP:          req.Options.TopP,
		StopSequences: req.Options.Stop,
	}
	if len(req.Tools) > 0 {
		tools, err := toAnthropicTools(req.Tools)
		if err != nil {
			return nil, err
		}
		if len(tools) > 0 {
			body.Tools = tools
		}
	}
	if req.ToolChoice != nil {
		choice, err := toAnthropicToolChoice(req.ToolChoice)
		if err != nil {
			return nil, err
		}
		if choice != nil {
			body.ToolChoice = choice
		}
	}
	applyAnthropicOptions(&body, req.Options.Anthropic)

	if req.Options.OnStream != nil {
		body.Stream = true
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	diag.LogText(p.cfg.Debug, debugFn, "anthropic.chat.request", string(data))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.cfg.APIKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := httputil.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if req.Options.OnStream != nil {
		if resp.StatusCode != http.StatusOK {
			respData, err := httputil.ReadBody(resp.Body)
			if err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("anthropic api error: status %d: %s", resp.StatusCode, strings.TrimSpace(string(respData)))
		}
		return p.chatStream(resp.Body, req.Options.OnStream)
	}

	respData, err := httputil.ReadBody(resp.Body)
	if err != nil {
		return nil, err
	}
	diag.LogText(p.cfg.Debug, debugFn, "anthropic.chat.response", string(respData))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("anthropic api error: status %d: %s", resp.StatusCode, strings.TrimSpace(string(respData)))
	}

	var out anthropicResponse
	if err := json.Unmarshal(respData, &out); err != nil {
		return nil, err
	}

	textParts := make([]string, 0, len(out.Content))
	toolCalls := make([]chat.ToolCall, 0)
	for _, part := range out.Content {
		switch part.Type {
		case "text":
			if strings.TrimSpace(part.Text) != "" {
				textParts = append(textParts, part.Text)
			}
		case "tool_use":
			call, err := fromAnthropicToolUse(part)
			if err != nil {
				return nil, err
			}
			toolCalls = append(toolCalls, call)
		}
	}
	text := strings.Join(textParts, "\n")

	result := &chat.Result{
		Text:      text,
		Model:     out.Model,
		Parts:     []chat.Part{},
		ToolCalls: toolCalls,
		Usage: chat.Usage{
			InputTokens:  out.Usage.InputTokens,
			OutputTokens: out.Usage.OutputTokens,
			TotalTokens:  out.Usage.InputTokens + out.Usage.OutputTokens,
		},
		Raw: out,
	}
	if text != "" {
		result.Parts = append(result.Parts, chat.TextPart(text))
	}

	return result, nil
}

func applyAnthropicOptions(body *anthropicRequest, opts structs.JSONMap) {
	if body == nil || len(opts) == 0 {
		return
	}
	opt := &opts
	if opt.HasKey("top_k") {
		if top := int(opt.GetInt64("top_k")); top > 0 {
			body.TopK = &top
		}
	}
	if userID := readUserID(opt); userID != "" {
		body.Metadata = &anthropicMetadata{UserID: userID}
	}
}

func toAnthropicTools(tools []chat.Tool) ([]anthropicTool, error) {
	out := make([]anthropicTool, 0, len(tools))
	for _, tool := range tools {
		if tool.Type != "function" {
			continue
		}
		if tool.Function.Name == "" {
			continue
		}
		var schema any
		if len(tool.Function.ParametersJSONSchema) > 0 {
			if err := json.Unmarshal(tool.Function.ParametersJSONSchema, &schema); err != nil {
				return nil, err
			}
		} else {
			schema = map[string]any{"type": "object"}
		}
		at := anthropicTool{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			InputSchema: schema,
		}
		out = append(out, at)
	}
	return out, nil
}

func toAnthropicToolChoice(choice *chat.ToolChoice) (*anthropicToolChoice, error) {
	if choice == nil {
		return nil, nil
	}
	switch choice.Mode {
	case "auto":
		return &anthropicToolChoice{Type: "auto"}, nil
	case "none":
		return &anthropicToolChoice{Type: "none"}, nil
	case "required":
		return &anthropicToolChoice{Type: "any"}, nil
	case "function":
		if strings.TrimSpace(choice.FunctionName) == "" {
			return nil, fmt.Errorf("tool_choice function_name is required")
		}
		return &anthropicToolChoice{Type: "tool", Name: choice.FunctionName}, nil
	default:
		return nil, nil
	}
}

func toAnthropicToolUses(calls []chat.ToolCall) ([]anthropicContentPart, error) {
	out := make([]anthropicContentPart, 0, len(calls))
	for _, call := range calls {
		if call.Function.Name == "" {
			continue
		}
		id := strings.TrimSpace(call.ID)
		if id == "" {
			return nil, fmt.Errorf("tool call id is required for anthropic tool_use")
		}
		args := strings.TrimSpace(call.Function.Arguments)
		if args == "" {
			args = "{}"
		}
		var input any
		if err := json.Unmarshal([]byte(args), &input); err != nil {
			return nil, fmt.Errorf("invalid tool call arguments: %w", err)
		}
		out = append(out, anthropicContentPart{
			Type:  "tool_use",
			ID:    id,
			Name:  call.Function.Name,
			Input: input,
		})
	}
	return out, nil
}

func fromAnthropicToolUse(part anthropicContentPart) (chat.ToolCall, error) {
	if strings.TrimSpace(part.ID) == "" || strings.TrimSpace(part.Name) == "" {
		return chat.ToolCall{}, fmt.Errorf("anthropic tool_use missing id or name")
	}
	args := "{}"
	if part.Input != nil {
		data, err := json.Marshal(part.Input)
		if err != nil {
			return chat.ToolCall{}, err
		}
		args = string(data)
	}
	return chat.ToolCall{
		ID:   part.ID,
		Type: "function",
		Function: chat.ToolCallFunction{
			Name:      part.Name,
			Arguments: args,
		},
	}, nil
}

// SSE event data types for streaming.

type sseMessageStart struct {
	Message struct {
		Model string `json:"model"`
		Usage struct {
			InputTokens int `json:"input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

type sseContentBlockStart struct {
	Index        int `json:"index"`
	ContentBlock struct {
		Type string `json:"type"`
		ID   string `json:"id,omitempty"`
		Name string `json:"name,omitempty"`
	} `json:"content_block"`
}

type sseContentBlockDelta struct {
	Index int `json:"index"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text,omitempty"`
		PartialJSON string `json:"partial_json,omitempty"`
	} `json:"delta"`
}

type sseMessageDelta struct {
	Usage struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func (p *Provider) chatStream(body io.Reader, onStream chat.OnStreamFunc) (*chat.Result, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024) // allow lines up to 1 MB

	var (
		model        string
		inputTokens  int
		outputTokens int
		textParts    []string
		toolCalls    []chat.ToolCall

		// per-tool-call accumulator
		currentToolIndex int = -1
		currentToolID    string
		currentToolName  string
		currentToolArgs  strings.Builder
	)

	flushToolCall := func() {
		if currentToolIndex >= 0 && currentToolName != "" {
			toolCalls = append(toolCalls, chat.ToolCall{
				ID:   currentToolID,
				Type: "function",
				Function: chat.ToolCallFunction{
					Name:      currentToolName,
					Arguments: currentToolArgs.String(),
				},
			})
		}
		currentToolIndex = -1
		currentToolID = ""
		currentToolName = ""
		currentToolArgs.Reset()
	}

	var eventType string
	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
			continue
		}

		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		switch eventType {
		case "message_start":
			var ev sseMessageStart
			if err := json.Unmarshal([]byte(data), &ev); err == nil {
				model = ev.Message.Model
				inputTokens = ev.Message.Usage.InputTokens
			}

		case "content_block_start":
			var ev sseContentBlockStart
			if err := json.Unmarshal([]byte(data), &ev); err == nil {
				if ev.ContentBlock.Type == "tool_use" {
					flushToolCall()
					currentToolIndex = ev.Index
					currentToolID = ev.ContentBlock.ID
					currentToolName = ev.ContentBlock.Name
					if err := onStream(chat.StreamEvent{
						ToolCallDelta: &chat.ToolCallDelta{
							Index: ev.Index,
							ID:    ev.ContentBlock.ID,
							Name:  ev.ContentBlock.Name,
						},
					}); err != nil {
						return nil, err
					}
				}
			}

		case "content_block_delta":
			var ev sseContentBlockDelta
			if err := json.Unmarshal([]byte(data), &ev); err == nil {
				switch ev.Delta.Type {
				case "text_delta":
					textParts = append(textParts, ev.Delta.Text)
					if err := onStream(chat.StreamEvent{
						Delta: ev.Delta.Text,
					}); err != nil {
						return nil, err
					}
				case "input_json_delta":
					currentToolArgs.WriteString(ev.Delta.PartialJSON)
					if err := onStream(chat.StreamEvent{
						ToolCallDelta: &chat.ToolCallDelta{
							Index:     currentToolIndex,
							ArgsChunk: ev.Delta.PartialJSON,
						},
					}); err != nil {
						return nil, err
					}
				}
			}

		case "content_block_stop":
			flushToolCall()

		case "message_delta":
			var ev sseMessageDelta
			if err := json.Unmarshal([]byte(data), &ev); err == nil {
				outputTokens = ev.Usage.OutputTokens
			}

		case "message_stop":
			// handled after the loop
		}
		eventType = ""
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	flushToolCall()

	totalTokens := inputTokens + outputTokens
	if err := onStream(chat.StreamEvent{
		Done: true,
		Usage: &chat.Usage{
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
			TotalTokens:  totalTokens,
		},
	}); err != nil {
		return nil, err
	}

	return &chat.Result{
		Text:  strings.Join(textParts, ""),
		Model: model,
		Parts: func() []chat.Part {
			text := strings.Join(textParts, "")
			if text == "" {
				return nil
			}
			return []chat.Part{chat.TextPart(text)}
		}(),
		ToolCalls: toolCalls,
		Usage: chat.Usage{
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
			TotalTokens:  totalTokens,
		},
	}, nil
}

func readUserID(opt *structs.JSONMap) string {
	if opt == nil {
		return ""
	}
	if opt.HasKey("user_id") {
		return strings.TrimSpace(opt.GetString("user_id"))
	}
	if !opt.HasKey("metadata") {
		return ""
	}
	raw := (*opt)["metadata"]
	switch v := raw.(type) {
	case map[string]any:
		if id, ok := v["user_id"].(string); ok {
			return strings.TrimSpace(id)
		}
	case structs.JSONMap:
		if id, ok := v["user_id"].(string); ok {
			return strings.TrimSpace(id)
		}
	}
	return ""
}
