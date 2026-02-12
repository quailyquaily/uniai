package speedtest

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/quailyquaily/uniai"
)

const (
	DefaultAttempts    = 3
	DefaultMethod      = MethodEcho
	DefaultEchoText    = "speedtest-echo-20260211"
	DefaultTimeout     = 90 * time.Second
	DefaultTemperature = 1.0
)

type Method string

const (
	MethodEcho        Method = "echo"
	MethodToolCalling Method = "toolcalling"
)

type Case struct {
	Name   string
	Client *uniai.Client
}

type Config struct {
	Method      Method
	Attempts    int
	Timeout     time.Duration
	Temperature *float64
	EchoText    string
	OnEvent     func(Event)
}

type EventType string

const (
	EventCaseStart   EventType = "case_start"
	EventAttemptDone EventType = "attempt_done"
	EventCaseDone    EventType = "case_done"
)

type Event struct {
	Type    EventType
	Method  Method
	Case    CaseResult
	Attempt *AttemptResult
}

type Report struct {
	Method  Method
	Results []CaseResult
}

type CaseResult struct {
	Name       string
	Provider   string
	Model      string
	APIBase    string
	SetupError string
	Attempts   []AttemptResult
	Average    time.Duration
}

type AttemptResult struct {
	Index    int
	Duration time.Duration
	OK       bool
	Match    bool
	Err      string
	Response string
}

type methodExecResult struct {
	Match          bool
	Response       string
	MismatchReason string
}

func Run(ctx context.Context, cases []Case, cfg Config) (*Report, error) {
	if len(cases) == 0 {
		return nil, fmt.Errorf("cases are required")
	}

	method, err := normalizeMethod(cfg.Method)
	if err != nil {
		return nil, err
	}
	attempts := cfg.Attempts
	if attempts <= 0 {
		attempts = DefaultAttempts
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	temperature := DefaultTemperature
	if cfg.Temperature != nil {
		temperature = *cfg.Temperature
	}
	echoText := strings.TrimSpace(cfg.EchoText)
	if echoText == "" {
		echoText = DefaultEchoText
	}

	report := &Report{
		Method:  method,
		Results: make([]CaseResult, 0, len(cases)),
	}

	for _, item := range cases {
		r := runOne(ctx, item, method, attempts, timeout, temperature, echoText, cfg.OnEvent)
		report.Results = append(report.Results, r)
	}

	return report, nil
}

func runOne(
	ctx context.Context,
	item Case,
	method Method,
	attempts int,
	timeout time.Duration,
	temperature float64,
	echoText string,
	onEvent func(Event),
) CaseResult {
	result := CaseResult{
		Name:     strings.TrimSpace(item.Name),
		Attempts: make([]AttemptResult, 0, attempts),
	}
	if result.Name == "" {
		result.Name = "unnamed"
	}
	if item.Client == nil {
		result.SetupError = "client is required"
		emitEvent(onEvent, Event{Type: EventCaseDone, Method: method, Case: result})
		return result
	}

	meta := item.Client.GetConfig()
	result.Provider = strings.TrimSpace(meta.Provider)
	if result.Provider == "" {
		result.Provider = "openai"
	}
	result.Model = strings.TrimSpace(meta.Model)
	result.APIBase = strings.TrimSpace(meta.APIBase)

	emitEvent(onEvent, Event{Type: EventCaseStart, Method: method, Case: result})

	if result.Model == "" {
		result.SetupError = "model is required in client config"
		emitEvent(onEvent, Event{Type: EventCaseDone, Method: method, Case: result})
		return result
	}

	for i := 1; i <= attempts; i++ {
		attempt := AttemptResult{Index: i}
		attemptCtx, cancel := context.WithTimeout(ctx, timeout)
		start := time.Now()
		exec, err := runAttemptByMethod(attemptCtx, item.Client, result.Provider, result.Model, temperature, echoText, method)
		attempt.Duration = time.Since(start)
		cancel()

		if err != nil {
			attempt.Err = err.Error()
			result.Attempts = append(result.Attempts, attempt)
			emitEvent(onEvent, Event{Type: EventAttemptDone, Method: method, Case: result, Attempt: &attempt})
			continue
		}

		attempt.OK = true
		attempt.Match = exec.Match
		attempt.Response = exec.Response
		if !exec.Match {
			attempt.Err = exec.MismatchReason
		}
		result.Attempts = append(result.Attempts, attempt)
		emitEvent(onEvent, Event{Type: EventAttemptDone, Method: method, Case: result, Attempt: &attempt})
	}

	var sum time.Duration
	for _, a := range result.Attempts {
		sum += a.Duration
	}
	if len(result.Attempts) > 0 {
		result.Average = sum / time.Duration(len(result.Attempts))
	}

	emitEvent(onEvent, Event{Type: EventCaseDone, Method: method, Case: result})
	return result
}

func runAttemptByMethod(
	ctx context.Context,
	client *uniai.Client,
	provider string,
	model string,
	temperature float64,
	echoText string,
	method Method,
) (methodExecResult, error) {
	switch method {
	case MethodEcho:
		return runEchoAttempt(ctx, client, provider, model, temperature, echoText)
	case MethodToolCalling:
		return runToolCallingAttempt(ctx, client, provider, model, temperature)
	default:
		return methodExecResult{}, fmt.Errorf("unsupported method %q", method)
	}
}

func runEchoAttempt(
	ctx context.Context,
	client *uniai.Client,
	provider string,
	model string,
	temperature float64,
	echoText string,
) (methodExecResult, error) {
	resp, err := client.Chat(ctx,
		uniai.WithProvider(provider),
		uniai.WithModel(model),
		uniai.WithTemperature(temperature),
		uniai.WithMessages(
			uniai.System("Reply with exactly the user's message. Keep all characters unchanged. Do not add, remove, or modify any character. Do not add quotes, markdown. Do not add explanations. Output only the text."),
			uniai.User(echoText),
		),
	)
	if err != nil {
		return methodExecResult{}, err
	}

	out := methodExecResult{
		Match:    resp.Text == echoText,
		Response: resp.Text,
	}
	if !out.Match {
		out.MismatchReason = fmt.Sprintf(
			"echo mismatch, expected_len=%d got_len=%d got=%q",
			len(echoText),
			len(resp.Text),
			resp.Text,
		)
	}
	return out, nil
}

func runToolCallingAttempt(
	ctx context.Context,
	client *uniai.Client,
	provider string,
	model string,
	temperature float64,
) (methodExecResult, error) {
	tools := []uniai.Tool{
		uniai.FunctionTool(
			"get_weather",
			"Get weather for a city or place.",
			[]byte(`{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}`),
		),
		uniai.FunctionTool(
			"get_direction",
			"Get route direction from one place to another place.",
			[]byte(`{"type":"object","properties":{"from":{"type":"string"},"to":{"type":"string"}},"required":["from","to"]}`),
		),
		uniai.FunctionTool(
			"send_message",
			"Send a message to a recipient.",
			[]byte(`{"type":"object","properties":{"to":{"type":"string"},"message":{"type":"string"}},"required":["to","message"]}`),
		),
	}

	resp, err := client.Chat(ctx,
		uniai.WithProvider(provider),
		uniai.WithModel(model),
		uniai.WithTemperature(temperature),
		uniai.WithMessages(
			uniai.System("You are a tool-calling assistant. You must choose exactly one tool from the provided tools based on the user request. Do not answer directly when a tool is appropriate."),
			uniai.User("从 tokyo station 到 shinjuku station 怎么走"),
		),
		uniai.WithTools(tools),
		uniai.WithToolChoice(uniai.ToolChoiceAuto()),
	)
	if err != nil {
		return methodExecResult{}, err
	}

	toolNames := make([]string, 0, len(resp.ToolCalls))
	match := false
	for _, tc := range resp.ToolCalls {
		name := strings.TrimSpace(tc.Function.Name)
		if name == "" {
			continue
		}
		toolNames = append(toolNames, name)
		if name == "get_direction" {
			match = true
		}
	}

	out := methodExecResult{
		Match:    match,
		Response: buildToolCallingResponse(toolNames, resp.Text),
	}
	if out.Match {
		return out, nil
	}
	if len(toolNames) == 0 {
		out.MismatchReason = fmt.Sprintf("toolcalling mismatch, no tool call, text=%q", resp.Text)
		return out, nil
	}
	out.MismatchReason = fmt.Sprintf("toolcalling mismatch, called=%s", strings.Join(toolNames, ","))
	return out, nil
}

func buildToolCallingResponse(toolNames []string, text string) string {
	if len(toolNames) == 0 {
		return text
	}
	if strings.TrimSpace(text) == "" {
		return "tool_calls=" + strings.Join(toolNames, ",")
	}
	return fmt.Sprintf("tool_calls=%s text=%q", strings.Join(toolNames, ","), text)
}

func normalizeMethod(m Method) (Method, error) {
	value := Method(strings.ToLower(strings.TrimSpace(string(m))))
	switch value {
	case "", MethodEcho:
		return MethodEcho, nil
	case MethodToolCalling:
		return MethodToolCalling, nil
	default:
		return "", fmt.Errorf("unsupported method %q (supported: %s, %s)", m, MethodEcho, MethodToolCalling)
	}
}

func emitEvent(fn func(Event), event Event) {
	if fn != nil {
		fn(event)
	}
}
