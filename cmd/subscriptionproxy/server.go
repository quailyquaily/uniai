package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lyricat/goutils/structs"
	openai "github.com/openai/openai-go/v3"
	"github.com/quailyquaily/uniai/chat"
	openaiadapter "github.com/quailyquaily/uniai/chat/openai"
)

const (
	maxRequestBody      = 32 << 20
	defaultInstructions = "You are a helpful assistant."
)

type chatRunner interface {
	Chat(context.Context, ...chat.Option) (*chat.Result, error)
}

type chatCompletionParams openai.ChatCompletionNewParams

type chatCompletionRequest struct {
	chatCompletionParams
	Stream bool `json:"stream"`
}

type apiHandler struct {
	runners      map[string]chatRunner
	backend      string
	defaultModel string
}

func newAPIHandler(runner chatRunner, backend, defaultModel string) http.Handler {
	return &apiHandler{
		runners:      map[string]chatRunner{backend: runner},
		backend:      backend,
		defaultModel: strings.TrimSpace(defaultModel),
	}
}

func newRoutedAPIHandler(codexRunner, xaiRunner chatRunner, defaultModel string) http.Handler {
	return &apiHandler{
		runners: map[string]chatRunner{
			backendCodex: codexRunner,
			backendXAI:   xaiRunner,
		},
		defaultModel: strings.TrimSpace(defaultModel),
	}
}

func (h *apiHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/healthz":
		if r.Method != http.MethodGet {
			writeOpenAIError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
			return
		}
		backend := h.backend
		if backend == "" {
			backend = "codex,grok"
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "backend": backend})
	case "/v1/chat/completions":
		if r.Method != http.MethodPost {
			writeOpenAIError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
			return
		}
		h.handleChatCompletions(w, r)
	case "/v1/responses":
		if r.Method != http.MethodPost {
			writeOpenAIError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
			return
		}
		h.handleResponses(w, r)
	default:
		writeOpenAIError(w, http.StatusNotFound, "not found", "invalid_request_error")
	}
}

func (h *apiHandler) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	body, err := readRequestBody(w, r)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, err.Error(), "invalid_request_error")
		return
	}
	params, stream, err := decodeChatCompletionRequest(body)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid JSON request", "invalid_request_error")
		return
	}
	if err := validateSubscriptionChatParams(params); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, err.Error(), "invalid_request_error")
		return
	}
	model, backend, runner, err := h.resolveTarget(string(params.Model))
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, err.Error(), "invalid_request_error")
		return
	}
	options, err := openaiadapter.ToChatOptions(params)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, err.Error(), "invalid_request_error")
		return
	}
	options = append(options, chat.WithModel(model))
	if backend == backendCodex && !chatParamsHaveInstructions(params) {
		options = append(options, chat.WithMessages(chat.System(defaultInstructions)))
	}
	if stream {
		h.streamChatCompletions(w, r, runner, model, options)
		return
	}
	result, err := runner.Chat(r.Context(), options...)
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, err.Error(), "upstream_error")
		return
	}
	if result == nil {
		writeOpenAIError(w, http.StatusBadGateway, "upstream returned an empty response", "upstream_error")
		return
	}
	writeJSON(w, http.StatusOK, openaiadapter.ToOpenAIResponse(result, model))
}

func (h *apiHandler) streamChatCompletions(w http.ResponseWriter, r *http.Request, runner chatRunner, model string, options []chat.Option) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeOpenAIError(w, http.StatusInternalServerError, "streaming is unavailable", "server_error")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	id := fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	created := time.Now().Unix()
	_ = writeSSE(w, map[string]any{
		"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
		"choices": []any{map[string]any{
			"index": 0, "delta": map[string]any{"role": "assistant"}, "finish_reason": nil,
		}},
	})
	flusher.Flush()

	toolCallsSeen := false
	toolCallIndexes := map[int]int{}
	nextToolCallIndex := 0
	options = append(options, chat.WithOnStream(func(event chat.StreamEvent) error {
		if event.Done {
			finishReason := "stop"
			if toolCallsSeen {
				finishReason = "tool_calls"
			}
			chunk := map[string]any{
				"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
				"choices": []any{map[string]any{
					"index": 0, "delta": map[string]any{}, "finish_reason": finishReason,
				}},
			}
			if event.Usage != nil {
				chunk["usage"] = chatCompletionUsage(*event.Usage)
			}
			if err := writeSSE(w, chunk); err != nil {
				return err
			}
			flusher.Flush()
			return nil
		}

		delta := map[string]any{}
		if event.Delta != "" {
			delta["content"] = event.Delta
		}
		if event.ReasoningDelta != nil && event.ReasoningDelta.Delta != "" {
			delta["reasoning_content"] = event.ReasoningDelta.Delta
		}
		if event.ToolCallDelta != nil {
			toolCallsSeen = true
			toolCallIndex, ok := toolCallIndexes[event.ToolCallDelta.Index]
			if !ok {
				toolCallIndex = nextToolCallIndex
				toolCallIndexes[event.ToolCallDelta.Index] = toolCallIndex
				nextToolCallIndex++
			}
			toolCall := map[string]any{"index": toolCallIndex, "type": "function"}
			if event.ToolCallDelta.ID != "" {
				toolCall["id"] = event.ToolCallDelta.ID
			}
			function := map[string]any{}
			if event.ToolCallDelta.Name != "" {
				function["name"] = event.ToolCallDelta.Name
			}
			if event.ToolCallDelta.ArgsChunk != "" {
				function["arguments"] = event.ToolCallDelta.ArgsChunk
			}
			toolCall["function"] = function
			delta["tool_calls"] = []any{toolCall}
		}
		if len(delta) == 0 {
			return nil
		}
		if err := writeSSE(w, map[string]any{
			"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": nil}},
		}); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}))
	_, err := runner.Chat(r.Context(), options...)
	if err != nil {
		_ = writeSSE(w, map[string]any{"error": map[string]string{
			"message": err.Error(), "type": "upstream_error",
		}})
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func (h *apiHandler) handleResponses(w http.ResponseWriter, r *http.Request) {
	body, err := readRequestBody(w, r)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, err.Error(), "invalid_request_error")
		return
	}
	payload, err := decodeResponsesRequest(body)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid JSON request", "invalid_request_error")
		return
	}
	stream, _ := payload["stream"].(bool)
	requestedModel, _ := payload["model"].(string)
	model, backend, runner, err := h.resolveTarget(requestedModel)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, err.Error(), "invalid_request_error")
		return
	}
	delete(payload, "model")
	delete(payload, "stream")
	if backend == backendCodex {
		if instructions, _ := payload["instructions"].(string); strings.TrimSpace(instructions) == "" {
			payload["instructions"] = defaultInstructions
		}
	}
	options := []chat.Option{
		chat.WithModel(model),
		chat.WithOpenAIOptions(structs.JSONMap(payload)),
	}
	if stream {
		h.streamResponses(w, r, runner, options)
		return
	}
	result, err := runner.Chat(r.Context(), options...)
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, err.Error(), "upstream_error")
		return
	}
	if result == nil || result.Raw == nil {
		writeOpenAIError(w, http.StatusBadGateway, "upstream returned an empty Responses payload", "upstream_error")
		return
	}
	responsePayload, err := responsesResultPayload(result)
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, err.Error(), "upstream_error")
		return
	}
	writeJSON(w, http.StatusOK, responsePayload)
}

func responsesResultPayload(result *chat.Result) (any, error) {
	if result == nil {
		return nil, nil
	}
	if result.Text == "" && len(result.ToolCalls) == 0 {
		return result.Raw, nil
	}

	data, err := marshalJSON(result.Raw)
	if err != nil {
		return nil, fmt.Errorf("encode upstream Responses payload: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	response := map[string]any{}
	if err := decoder.Decode(&response); err != nil {
		return nil, fmt.Errorf("decode upstream Responses payload: %w", err)
	}

	var output []any
	if rawOutput, exists := response["output"]; exists && rawOutput != nil {
		var ok bool
		output, ok = rawOutput.([]any)
		if !ok {
			return nil, fmt.Errorf("upstream Responses output is not an array")
		}
	}

	hasText := false
	functionCalls := make(map[string]map[string]any)
	for _, value := range output {
		item, ok := value.(map[string]any)
		if !ok {
			continue
		}
		switch item["type"] {
		case "message":
			content, _ := item["content"].([]any)
			for _, value := range content {
				part, ok := value.(map[string]any)
				if !ok || part["type"] != "output_text" {
					continue
				}
				if text, _ := part["text"].(string); text != "" {
					hasText = true
				}
			}
		case "function_call":
			if callID, _ := item["call_id"].(string); callID != "" {
				functionCalls[callID] = item
			}
		}
	}

	if result.Text != "" && !hasText {
		item := map[string]any{
			"type": "message", "role": "assistant", "status": "completed",
			"content": []any{map[string]any{
				"type": "output_text", "text": result.Text, "annotations": []any{},
			}},
		}
		if responseID, _ := response["id"].(string); responseID != "" {
			item["id"] = "msg_" + strings.TrimPrefix(responseID, "resp_")
		}
		output = append(output, item)
	}

	for _, call := range result.ToolCalls {
		if item := functionCalls[call.ID]; item != nil {
			if name, _ := item["name"].(string); name == "" {
				item["name"] = call.Function.Name
			}
			if arguments, _ := item["arguments"].(string); arguments == "" {
				item["arguments"] = call.Function.Arguments
			}
			continue
		}
		output = append(output, map[string]any{
			"type":      "function_call",
			"status":    "completed",
			"call_id":   call.ID,
			"name":      call.Function.Name,
			"arguments": call.Function.Arguments,
		})
	}
	response["output"] = output
	return response, nil
}

func (h *apiHandler) streamResponses(w http.ResponseWriter, r *http.Request, runner chatRunner, options []chat.Option) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeOpenAIError(w, http.StatusInternalServerError, "streaming is unavailable", "server_error")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	lastSequence := int64(0)
	terminal := false
	options = append(options, chat.WithOnStream(func(event chat.StreamEvent) error {
		if event.Done {
			if terminal {
				return nil
			}
			responseJSON, err := marshalJSON(event.Raw)
			if err != nil {
				return err
			}
			lastSequence++
			terminal = true
			if err := writeSSE(w, map[string]any{
				"type": "response.completed", "sequence_number": lastSequence, "response": json.RawMessage(responseJSON),
			}); err != nil {
				return err
			}
			flusher.Flush()
			return nil
		}
		if event.Raw == nil {
			return nil
		}
		eventType, sequence, hasSequence := responseStreamEventMetadata(event.Raw)
		if hasSequence && sequence > lastSequence {
			lastSequence = sequence
		}
		switch eventType {
		case "response.completed", "response.failed", "response.incomplete":
			terminal = true
		}
		if err := writeSSE(w, event.Raw); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}))
	result, err := runner.Chat(r.Context(), options...)
	if err != nil {
		if !terminal {
			_ = writeSSE(w, map[string]any{"error": map[string]string{
				"message": err.Error(), "type": "upstream_error",
			}})
		}
	} else if !terminal && result != nil && result.Raw != nil {
		responseJSON, rawErr := marshalJSON(result.Raw)
		if rawErr != nil {
			_ = writeSSE(w, map[string]any{"error": map[string]string{
				"message": "encode upstream response", "type": "server_error",
			}})
		} else {
			lastSequence++
			_ = writeSSE(w, map[string]any{
				"type": "response.completed", "sequence_number": lastSequence, "response": json.RawMessage(responseJSON),
			})
		}
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func decodeChatCompletionRequest(body []byte) (openai.ChatCompletionNewParams, bool, error) {
	var request chatCompletionRequest
	if err := json.Unmarshal(body, &request); err != nil {
		return openai.ChatCompletionNewParams{}, false, err
	}
	return openai.ChatCompletionNewParams(request.chatCompletionParams), request.Stream, nil
}

func decodeResponsesRequest(body []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	payload := map[string]any{}
	if err := decoder.Decode(&payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func (h *apiHandler) resolveTarget(requestedModel string) (string, string, chatRunner, error) {
	model := strings.TrimSpace(requestedModel)
	if model == "" {
		model = h.defaultModel
	}
	if model == "" {
		return "", "", nil, fmt.Errorf("model is required")
	}
	backend := h.backend
	if backend == "" {
		backend = backendCodex
		if strings.HasPrefix(strings.ToLower(model), "grok-") {
			backend = backendXAI
		}
	}
	runner := h.runners[backend]
	if runner == nil {
		return "", "", nil, fmt.Errorf("backend %s is not configured", displayBackend(backend))
	}
	return model, backend, runner, nil
}

func validateSubscriptionChatParams(params openai.ChatCompletionNewParams) error {
	if params.N.Valid() && params.N.Value != 1 {
		return fmt.Errorf("subscription proxy supports only n=1")
	}
	if params.Seed.Valid() {
		return fmt.Errorf("subscription proxy does not support seed")
	}
	if params.Logprobs.Valid() && params.Logprobs.Value {
		return fmt.Errorf("subscription proxy does not support logprobs")
	}
	if len(params.Modalities) > 0 && !(len(params.Modalities) == 1 && params.Modalities[0] == "text") {
		return fmt.Errorf("subscription proxy supports only the text modality")
	}
	if len(params.LogitBias) > 0 {
		return fmt.Errorf("subscription proxy does not support logit_bias")
	}
	return nil
}

func responseStreamEventMetadata(raw any) (string, int64, bool) {
	data, err := marshalJSON(raw)
	if err != nil {
		return "", 0, false
	}
	var envelope struct {
		Type           string `json:"type"`
		SequenceNumber *int64 `json:"sequence_number"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return "", 0, false
	}
	if envelope.SequenceNumber == nil {
		return envelope.Type, 0, false
	}
	return envelope.Type, *envelope.SequenceNumber, true
}

func chatParamsHaveInstructions(params openai.ChatCompletionNewParams) bool {
	for _, message := range params.Messages {
		if message.OfSystem != nil || message.OfDeveloper != nil {
			return true
		}
	}
	return false
}

func chatCompletionUsage(usage chat.Usage) map[string]any {
	return map[string]any{
		"prompt_tokens": usage.InputTokens, "completion_tokens": usage.OutputTokens, "total_tokens": usage.TotalTokens,
		"prompt_tokens_details": map[string]any{"cached_tokens": usage.Cache.CachedInputTokens},
	}
}

func readRequestBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("read request body: %w", err)
	}
	return body, nil
}

func writeSSE(w io.Writer, value any) error {
	data, err := marshalJSON(value)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", data)
	return err
}

func writeOpenAIError(w http.ResponseWriter, status int, message, errorType string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{
		"message": message, "type": errorType,
	}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	data, err := marshalJSON(value)
	if err != nil {
		status = http.StatusInternalServerError
		data = []byte(`{"error":{"message":"encode JSON response","type":"server_error"}}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

func marshalJSON(value any) ([]byte, error) {
	if source, ok := value.(interface{ RawJSON() string }); ok {
		raw := strings.TrimSpace(source.RawJSON())
		if raw != "" {
			data := []byte(raw)
			if !json.Valid(data) {
				return nil, fmt.Errorf("upstream returned invalid raw JSON")
			}
			return data, nil
		}
	}
	return json.Marshal(value)
}
