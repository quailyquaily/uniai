package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3/responses"
	"github.com/quailyquaily/uniai/chat"
)

func TestChatCompletionsEndpoint(t *testing.T) {
	runner := &fakeChatRunner{run: func(_ context.Context, opts ...chat.Option) (*chat.Result, error) {
		req, err := chat.BuildRequest(opts...)
		if err != nil {
			return nil, err
		}
		if req.Model != "requested-model" {
			t.Fatalf("model = %q", req.Model)
		}
		if len(req.Messages) != 2 || req.Messages[0].Role != chat.RoleUser || req.Messages[1].Role != chat.RoleSystem {
			t.Fatalf("messages = %+v", req.Messages)
		}
		return &chat.Result{
			ID: "resp_123", Text: "hello", Model: "served-model",
			Usage: chat.Usage{InputTokens: 4, OutputTokens: 2, TotalTokens: 6},
		}, nil
	}}
	handler := newAPIHandler(runner, backendCodex, "default-model")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{
		"model":"requested-model",
		"messages":[{"role":"user","content":"hi"}]
	}`))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["id"] != "resp_123" || response["object"] != "chat.completion" || response["model"] != "served-model" {
		t.Fatalf("response = %#v", response)
	}
}

func TestDecodeChatCompletionRequest(t *testing.T) {
	params, stream, err := decodeChatCompletionRequest([]byte(`{
		"model":"gpt-5.4",
		"stream":true,
		"messages":[{"role":"user","content":"hi"}],
		"reasoning_effort":"high"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if !stream || params.Model != "gpt-5.4" || len(params.Messages) != 1 || params.ReasoningEffort != "high" {
		t.Fatalf("stream=%t params=%+v", stream, params)
	}
}

func TestDecodeResponsesRequestPreservesJSONNumbers(t *testing.T) {
	payload, err := decodeResponsesRequest([]byte(`{"model":"gpt-5.4","max_output_tokens":9007199254740993}`))
	if err != nil {
		t.Fatal(err)
	}
	value, ok := payload["max_output_tokens"].(json.Number)
	if !ok || value.String() != "9007199254740993" {
		t.Fatalf("max_output_tokens = %#v", payload["max_output_tokens"])
	}
}

func TestChatCompletionsAcceptsSingleTextChoiceAndReasoningEffort(t *testing.T) {
	runner := &fakeChatRunner{run: func(_ context.Context, opts ...chat.Option) (*chat.Result, error) {
		req, err := chat.BuildRequest(opts...)
		if err != nil {
			return nil, err
		}
		if req.Options.ReasoningEffort == nil || *req.Options.ReasoningEffort != chat.ReasoningEffortHigh {
			return nil, fmt.Errorf("reasoning effort = %#v", req.Options.ReasoningEffort)
		}
		if req.Options.OpenAI.HasKey("n") || req.Options.OpenAI.HasKey("modalities") || req.Options.OpenAI.HasKey("reasoning_effort") {
			return nil, fmt.Errorf("unexpected raw options: %#v", req.Options.OpenAI)
		}
		return &chat.Result{ID: "resp_123", Text: "hello", Model: "gpt-5.4"}, nil
	}}
	handler := newAPIHandler(runner, backendXAI, "gpt-5.4")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{
		"model":"gpt-5.4",
		"messages":[{"role":"user","content":"hi"}],
		"n":1,
		"modalities":["text"],
		"reasoning_effort":"high"
	}`))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestChatCompletionsRejectsUnsupportedParametersBeforeCallingBackend(t *testing.T) {
	tests := []struct {
		name  string
		field string
	}{
		{name: "multiple choices", field: `"n":2`},
		{name: "seed", field: `"seed":42`},
		{name: "log probabilities", field: `"logprobs":true`},
		{name: "audio modality", field: `"modalities":["audio"]`},
		{name: "logit bias", field: `"logit_bias":{"123":1}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			runner := &fakeChatRunner{run: func(_ context.Context, _ ...chat.Option) (*chat.Result, error) {
				called = true
				return &chat.Result{Text: "unexpected"}, nil
			}}
			handler := newAPIHandler(runner, backendXAI, "gpt-5.4")
			body := fmt.Sprintf(`{"messages":[{"role":"user","content":"hi"}],%s}`, tt.field)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)))
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
			}
			if called {
				t.Fatal("backend was called")
			}
		})
	}
}

func TestResponsesEndpointReturnsRawUpstreamResponse(t *testing.T) {
	raw := map[string]any{"id": "resp_raw", "object": "response", "status": "completed", "output": []any{}}
	runner := &fakeChatRunner{run: func(_ context.Context, opts ...chat.Option) (*chat.Result, error) {
		req, err := chat.BuildRequest(opts...)
		if err != nil {
			return nil, err
		}
		if req.Model != "requested-model" || req.Options.OpenAI.GetString("instructions") != "be concise" {
			t.Fatalf("request = %+v", req)
		}
		if !req.Options.OpenAI.HasKey("input") || req.Options.OpenAI.HasKey("model") || req.Options.OpenAI.HasKey("stream") {
			t.Fatalf("openai options = %#v", req.Options.OpenAI)
		}
		return &chat.Result{Raw: raw}, nil
	}}
	handler := newAPIHandler(runner, backendXAI, "default-model")
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{
		"model":"requested-model",
		"instructions":"be concise",
		"input":"hello"
	}`))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["id"] != "resp_raw" || response["object"] != "response" {
		t.Fatalf("response = %#v", response)
	}
}

func TestResponsesEndpointPreservesSDKFunctionCallArguments(t *testing.T) {
	const arguments = `{"cmd":"uname -a"}`
	response := decodeSDKResponse(t, `{
		"id":"resp_1",
		"object":"response",
		"model":"gpt-5.4",
		"status":"completed",
		"output":[{
			"id":"fc_1",
			"type":"function_call",
			"status":"completed",
			"call_id":"call_1",
			"name":"bash",
			"arguments":"{\"cmd\":\"uname -a\"}"
		}]
	}`)
	runner := &fakeChatRunner{run: func(context.Context, ...chat.Option) (*chat.Result, error) {
		return &chat.Result{Raw: response}, nil
	}}
	handler := newAPIHandler(runner, backendXAI, "gpt-5.4")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"input":"run uname"}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "OfString") || strings.Contains(recorder.Body.String(), "OfResponseToolSearchCallArguments") {
		t.Fatalf("SDK union fields leaked into response: %s", recorder.Body.String())
	}
	var roundTrip responses.Response
	if err := json.Unmarshal(recorder.Body.Bytes(), &roundTrip); err != nil {
		t.Fatal(err)
	}
	if len(roundTrip.Output) != 1 {
		t.Fatalf("output = %#v", roundTrip.Output)
	}
	call, ok := roundTrip.Output[0].AsAny().(responses.ResponseFunctionToolCall)
	if !ok || call.Arguments != arguments {
		t.Fatalf("function call = %#v", roundTrip.Output[0].AsAny())
	}
}

func TestDualBackendRoutesChatCompletionsByModel(t *testing.T) {
	calls := map[string]int{}
	newRunner := func(backend string) chatRunner {
		return &fakeChatRunner{run: func(_ context.Context, opts ...chat.Option) (*chat.Result, error) {
			req, err := chat.BuildRequest(opts...)
			if err != nil {
				return nil, err
			}
			calls[backend]++
			return &chat.Result{ID: backend, Text: backend, Model: req.Model}, nil
		}}
	}
	handler := newRoutedAPIHandler(newRunner(backendCodex), newRunner(backendXAI), "")

	for _, model := range []string{"gpt-5.4", "grok-4.5"} {
		recorder := httptest.NewRecorder()
		body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}]}`, model)
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)))
		if recorder.Code != http.StatusOK {
			t.Fatalf("model=%s status=%d body=%s", model, recorder.Code, recorder.Body.String())
		}
	}
	if calls[backendCodex] != 1 || calls[backendXAI] != 1 {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestDualBackendRoutesResponsesByModel(t *testing.T) {
	calls := map[string]int{}
	newRunner := func(backend string) chatRunner {
		return &fakeChatRunner{run: func(_ context.Context, opts ...chat.Option) (*chat.Result, error) {
			req, err := chat.BuildRequest(opts...)
			if err != nil {
				return nil, err
			}
			calls[backend]++
			return &chat.Result{Raw: map[string]any{
				"id": "resp_" + backend, "object": "response", "model": req.Model,
			}}, nil
		}}
	}
	handler := newRoutedAPIHandler(newRunner(backendCodex), newRunner(backendXAI), "")

	for _, model := range []string{"o3", "GROK-4.5"} {
		recorder := httptest.NewRecorder()
		body := fmt.Sprintf(`{"model":%q,"input":"hi"}`, model)
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)))
		if recorder.Code != http.StatusOK {
			t.Fatalf("model=%s status=%d body=%s", model, recorder.Code, recorder.Body.String())
		}
	}
	if calls[backendCodex] != 1 || calls[backendXAI] != 1 {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestDualBackendRequiresModelWithoutDefault(t *testing.T) {
	called := false
	runner := &fakeChatRunner{run: func(_ context.Context, _ ...chat.Option) (*chat.Result, error) {
		called = true
		return &chat.Result{}, nil
	}}
	handler := newRoutedAPIHandler(runner, runner, "")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"input":"hi"}`)))
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "model is required") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if called {
		t.Fatal("backend was called")
	}
}

func TestChatCompletionsStreamingEndpoint(t *testing.T) {
	runner := &fakeChatRunner{run: func(_ context.Context, opts ...chat.Option) (*chat.Result, error) {
		req, err := chat.BuildRequest(opts...)
		if err != nil {
			return nil, err
		}
		if req.Options.OnStream == nil {
			t.Fatal("stream callback is missing")
		}
		_ = req.Options.OnStream(chat.StreamEvent{Delta: "hel"})
		_ = req.Options.OnStream(chat.StreamEvent{Delta: "lo"})
		_ = req.Options.OnStream(chat.StreamEvent{Done: true, Usage: &chat.Usage{InputTokens: 2, OutputTokens: 1, TotalTokens: 3}})
		return &chat.Result{ID: "resp_stream", Text: "hello", Model: "model"}, nil
	}}
	handler := newAPIHandler(runner, backendXAI, "default-model")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{
		"stream":true,
		"messages":[{"role":"user","content":"hi"}]
	}`))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK || !strings.Contains(body, `"object":"chat.completion.chunk"`) ||
		!strings.Contains(body, `"content":"hel"`) || !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("status=%d body=%s", recorder.Code, body)
	}
}

func TestChatCompletionsStreamingRemapsToolCallIndexes(t *testing.T) {
	runner := &fakeChatRunner{run: func(_ context.Context, opts ...chat.Option) (*chat.Result, error) {
		req, err := chat.BuildRequest(opts...)
		if err != nil {
			return nil, err
		}
		_ = req.Options.OnStream(chat.StreamEvent{ToolCallDelta: &chat.ToolCallDelta{
			Index: 1, ID: "call_1", Name: "first", ArgsChunk: `{"a":`,
		}})
		_ = req.Options.OnStream(chat.StreamEvent{ToolCallDelta: &chat.ToolCallDelta{
			Index: 1, ArgsChunk: `1}`,
		}})
		_ = req.Options.OnStream(chat.StreamEvent{ToolCallDelta: &chat.ToolCallDelta{
			Index: 3, ID: "call_2", Name: "second", ArgsChunk: `{}`,
		}})
		_ = req.Options.OnStream(chat.StreamEvent{Done: true})
		return &chat.Result{}, nil
	}}
	handler := newAPIHandler(runner, backendXAI, "gpt-5.4")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{
		"stream":true,
		"messages":[{"role":"user","content":"hi"}]
	}`))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	var indexes []int
	for _, event := range decodeSSEJSONEvents(t, recorder.Body.String()) {
		choices, _ := event["choices"].([]any)
		if len(choices) == 0 {
			continue
		}
		choice, _ := choices[0].(map[string]any)
		delta, _ := choice["delta"].(map[string]any)
		toolCalls, _ := delta["tool_calls"].([]any)
		if len(toolCalls) == 0 {
			continue
		}
		toolCall, _ := toolCalls[0].(map[string]any)
		index, _ := toolCall["index"].(float64)
		indexes = append(indexes, int(index))
	}
	if got := fmt.Sprint(indexes); got != "[0 0 1]" {
		t.Fatalf("tool call indexes = %s body=%s", got, recorder.Body.String())
	}
}

func TestResponsesStreamingEndpoint(t *testing.T) {
	runner := &fakeChatRunner{run: func(_ context.Context, opts ...chat.Option) (*chat.Result, error) {
		req, err := chat.BuildRequest(opts...)
		if err != nil {
			return nil, err
		}
		_ = req.Options.OnStream(chat.StreamEvent{
			Delta: "hi", Raw: map[string]any{"type": "response.output_text.delta", "delta": "hi"},
		})
		_ = req.Options.OnStream(chat.StreamEvent{
			Done: true, Raw: map[string]any{"id": "resp_stream", "object": "response", "status": "completed"},
		})
		return &chat.Result{}, nil
	}}
	handler := newAPIHandler(runner, backendXAI, "default-model")
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{
		"stream":true,
		"input":"hello"
	}`))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK || !strings.Contains(body, `"type":"response.output_text.delta"`) ||
		!strings.Contains(body, `"type":"response.completed"`) || !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("status=%d body=%s", recorder.Code, body)
	}
}

func TestResponsesStreamingPreservesLifecycleEventsAndSequenceNumbers(t *testing.T) {
	response := map[string]any{"id": "resp_stream", "object": "response", "status": "completed"}
	runner := &fakeChatRunner{run: func(_ context.Context, opts ...chat.Option) (*chat.Result, error) {
		req, err := chat.BuildRequest(opts...)
		if err != nil {
			return nil, err
		}
		rawEvents := []map[string]any{
			{"type": "response.created", "sequence_number": 1, "response": map[string]any{"id": "resp_stream"}},
			{"type": "response.output_item.added", "sequence_number": 2, "output_index": 0},
			{"type": "response.output_text.delta", "sequence_number": 3, "delta": "hi"},
			{"type": "response.completed", "sequence_number": 4, "response": response},
		}
		for _, raw := range rawEvents {
			if err := req.Options.OnStream(chat.StreamEvent{Raw: raw}); err != nil {
				return nil, err
			}
		}
		if err := req.Options.OnStream(chat.StreamEvent{Done: true, Raw: response}); err != nil {
			return nil, err
		}
		return &chat.Result{Raw: response}, nil
	}}
	handler := newAPIHandler(runner, backendXAI, "gpt-5.4")
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{
		"stream":true,
		"input":"hello"
	}`))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	events := decodeSSEJSONEvents(t, recorder.Body.String())
	var types []string
	var sequences []int
	for _, event := range events {
		eventType, _ := event["type"].(string)
		if eventType == "" {
			continue
		}
		types = append(types, eventType)
		sequence, _ := event["sequence_number"].(float64)
		sequences = append(sequences, int(sequence))
	}
	if got := strings.Join(types, ","); got != "response.created,response.output_item.added,response.output_text.delta,response.completed" {
		t.Fatalf("event types = %s body=%s", got, recorder.Body.String())
	}
	if got := fmt.Sprint(sequences); got != "[1 2 3 4]" {
		t.Fatalf("sequence numbers = %s body=%s", got, recorder.Body.String())
	}
}

func TestResponsesStreamingPreservesSDKFunctionCallArguments(t *testing.T) {
	const arguments = `{"cmd":"uname -a"}`
	response := decodeSDKResponse(t, `{
		"id":"resp_1",
		"object":"response",
		"model":"gpt-5.4",
		"status":"completed",
		"output":[{
			"id":"fc_1",
			"type":"function_call",
			"status":"completed",
			"call_id":"call_1",
			"name":"bash",
			"arguments":"{\"cmd\":\"uname -a\"}"
		}]
	}`)
	eventJSON := []string{
		`{"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"id":"fc_1","type":"function_call","status":"in_progress","call_id":"call_1","name":"bash","arguments":"{\"cmd\":\"uname -a\"}"}}`,
		`{"type":"response.output_item.done","sequence_number":2,"output_index":0,"item":{"id":"fc_1","type":"function_call","status":"completed","call_id":"call_1","name":"bash","arguments":"{\"cmd\":\"uname -a\"}"}}`,
		`{"type":"response.function_call_arguments.done","sequence_number":3,"output_index":0,"item_id":"fc_1","name":"bash","arguments":"{\"cmd\":\"uname -a\"}"}`,
		`{"type":"response.completed","sequence_number":4,"response":{"id":"resp_1","object":"response","model":"gpt-5.4","status":"completed","output":[{"id":"fc_1","type":"function_call","status":"completed","call_id":"call_1","name":"bash","arguments":"{\"cmd\":\"uname -a\"}"}]}}`,
	}
	runner := &fakeChatRunner{run: func(_ context.Context, opts ...chat.Option) (*chat.Result, error) {
		req, err := chat.BuildRequest(opts...)
		if err != nil {
			return nil, err
		}
		for _, raw := range eventJSON {
			if err := req.Options.OnStream(chat.StreamEvent{Raw: decodeSDKStreamEvent(t, raw)}); err != nil {
				return nil, err
			}
		}
		if err := req.Options.OnStream(chat.StreamEvent{Done: true, Raw: response}); err != nil {
			return nil, err
		}
		return &chat.Result{Raw: response}, nil
	}}
	handler := newAPIHandler(runner, backendXAI, "gpt-5.4")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"stream":true,"input":"run uname"}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "OfString") || strings.Contains(recorder.Body.String(), "OfResponseToolSearchCallArguments") {
		t.Fatalf("SDK union fields leaked into stream: %s", recorder.Body.String())
	}

	seen := map[string]bool{}
	for _, raw := range decodeSSERawEvents(t, recorder.Body.String()) {
		var event responses.ResponseStreamEventUnion
		if err := json.Unmarshal(raw, &event); err != nil {
			t.Fatalf("client SDK decode %s: %v", raw, err)
		}
		seen[event.Type] = true
		switch typed := event.AsAny().(type) {
		case responses.ResponseOutputItemAddedEvent:
			assertSDKFunctionCallArguments(t, typed.Item, arguments)
		case responses.ResponseOutputItemDoneEvent:
			assertSDKFunctionCallArguments(t, typed.Item, arguments)
		case responses.ResponseFunctionCallArgumentsDoneEvent:
			if typed.Arguments != arguments {
				t.Fatalf("done arguments = %q", typed.Arguments)
			}
		case responses.ResponseCompletedEvent:
			if len(typed.Response.Output) != 1 {
				t.Fatalf("completed output = %#v", typed.Response.Output)
			}
			assertSDKFunctionCallArguments(t, typed.Response.Output[0], arguments)
		}
	}
	for _, eventType := range []string{
		"response.output_item.added",
		"response.output_item.done",
		"response.function_call_arguments.done",
		"response.completed",
	} {
		if !seen[eventType] {
			t.Fatalf("missing event %s in %s", eventType, recorder.Body.String())
		}
	}
}

func TestAPIRejectsUnknownPathWithOpenAIError(t *testing.T) {
	handler := newAPIHandler(&fakeChatRunner{}, backendXAI, "model")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/unknown", nil))
	if recorder.Code != http.StatusNotFound || !strings.Contains(recorder.Body.String(), `"error"`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func decodeSSEJSONEvents(t *testing.T, body string) []map[string]any {
	t.Helper()
	var events []map[string]any
	for _, data := range decodeSSERawEvents(t, body) {
		var event map[string]any
		if err := json.Unmarshal(data, &event); err != nil {
			t.Fatalf("decode SSE event %q: %v", data, err)
		}
		events = append(events, event)
	}
	return events
}

func decodeSSERawEvents(t *testing.T, body string) [][]byte {
	t.Helper()
	var events [][]byte
	for _, block := range strings.Split(body, "\n\n") {
		for _, line := range strings.Split(block, "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				continue
			}
			events = append(events, []byte(data))
		}
	}
	return events
}

func decodeSDKResponse(t *testing.T, raw string) *responses.Response {
	t.Helper()
	var response responses.Response
	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		t.Fatal(err)
	}
	return &response
}

func decodeSDKStreamEvent(t *testing.T, raw string) responses.ResponseStreamEventUnion {
	t.Helper()
	var event responses.ResponseStreamEventUnion
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		t.Fatal(err)
	}
	return event
}

func assertSDKFunctionCallArguments(t *testing.T, item responses.ResponseOutputItemUnion, want string) {
	t.Helper()
	call, ok := item.AsAny().(responses.ResponseFunctionToolCall)
	if !ok || call.Arguments != want {
		t.Fatalf("function call = %#v", item.AsAny())
	}
}

type fakeChatRunner struct {
	run func(context.Context, ...chat.Option) (*chat.Result, error)
}

func (r *fakeChatRunner) Chat(ctx context.Context, opts ...chat.Option) (*chat.Result, error) {
	if r.run == nil {
		return &chat.Result{}, nil
	}
	return r.run(ctx, opts...)
}
