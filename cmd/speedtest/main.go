package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/quailyquaily/uniai"
	"github.com/quailyquaily/uniai/speedtest"
)

const (
	defaultConfigPath = "config.yaml"
	defaultCSVPath    = "speedtest_results.csv"
	defaultMethod     = "echo"
	defaultEchoText   = "speedtest-echo-20260211"
	defaultTimeout    = 90 * time.Second
	echoRuns          = 3

	methodEcho        = "echo"
	methodToolCalling = "toolcalling"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("speedtest", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	configPath := fs.String("config", defaultConfigPath, "path to config yaml")
	csvPath := fs.String("csv", defaultCSVPath, "output csv file path (deprecated, use --output)")
	outputPath := fs.String("output", "", "output csv file path")
	method := fs.String("method", defaultMethod, "test method: echo|toolcalling")

	if err := fs.Parse(args); err != nil {
		return err
	}

	rest := fs.Args()
	if len(rest) == 0 || rest[0] != "run" {
		return usageError()
	}

	var selectedName string
	switch len(rest) {
	case 1:
		// run all tests
	case 2:
		selectedName = rest[1]
	default:
		return usageError()
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}

	selectedMethod, err := normalizeMethod(*method)
	if err != nil {
		return err
	}

	tests, err := selectTests(cfg.Tests, selectedName)
	if err != nil {
		return err
	}

	results := make([]testResult, 0, len(tests))
	fmt.Printf("method=%s\n", styleMethod(selectedMethod))
	for _, t := range tests {
		results = append(results, runOne(cfg, t, selectedMethod, true))
	}

	finalOutput := strings.TrimSpace(*csvPath)
	if strings.TrimSpace(*outputPath) != "" {
		finalOutput = strings.TrimSpace(*outputPath)
	}

	if err := writeCSV(finalOutput, results); err != nil {
		return err
	}

	absCSV := finalOutput
	if v, err := filepath.Abs(finalOutput); err == nil {
		absCSV = v
	}
	fmt.Printf("\nCSV written: %s\n", absCSV)

	return nil
}

func runOne(cfg *fileConfig, t testConfig, method string, live bool) testResult {
	provider := strings.TrimSpace(t.Provider)
	if provider == "" {
		if strings.TrimSpace(t.APIBase) != "" {
			provider = "openai_custom"
		} else {
			provider = "openai"
		}
	}

	result := testResult{
		Name:                   t.Name,
		Provider:               provider,
		APIBase:                t.APIBase,
		APIKeyRef:              t.APIKeyRef,
		CloudflareAccountIDRef: t.CloudflareAccountIDRef,
		CloudflareAPITokenRef:  t.CloudflareAPITokenRef,
		Runs:                   make([]attemptResult, 0, echoRuns),
	}
	headerPrinted := false
	printHeaderIfNeeded := func() {
		if live && !headerPrinted {
			printTestHeader(result)
			headerPrinted = true
		}
	}

	model := strings.TrimSpace(t.Model)
	if model == "" {
		model = strings.TrimSpace(cfg.Model)
	}
	if model == "" {
		result.SetupError = "model is required (set tests[].model or top-level model)"
		if live {
			printHeaderIfNeeded()
			printSetupError(result.SetupError)
		}
		return result
	}
	result.Model = model

	echoText := strings.TrimSpace(t.EchoText)
	if echoText == "" {
		echoText = strings.TrimSpace(cfg.EchoText)
	}
	if echoText == "" {
		echoText = defaultEchoText
	}

	temperature := 1.0
	if cfg.Temperature != nil {
		temperature = *cfg.Temperature
	}
	if t.Temperature != nil {
		temperature = *t.Temperature
	}

	timeout := defaultTimeout
	timeoutSeconds := t.TimeoutSeconds
	if timeoutSeconds == 0 {
		timeoutSeconds = cfg.TimeoutSeconds
	}
	if timeoutSeconds > 0 {
		timeout = time.Duration(timeoutSeconds) * time.Second
	}

	clientCfg, keyRefUsed, accountIDRefUsed, tokenRefUsed, setupErr := buildClientConfig(provider, model, t)
	if setupErr != "" {
		result.SetupError = setupErr
		if live {
			printHeaderIfNeeded()
			printSetupError(result.SetupError)
		}
		return result
	}
	if keyRefUsed != "" {
		result.APIKeyRef = keyRefUsed
	}
	if accountIDRefUsed != "" {
		result.CloudflareAccountIDRef = accountIDRefUsed
	}
	if tokenRefUsed != "" {
		result.CloudflareAPITokenRef = tokenRefUsed
	}
	printHeaderIfNeeded()

	client := uniai.New(clientCfg)
	temp := temperature
	report, err := speedtest.Run(
		context.Background(),
		[]speedtest.Case{
			{
				Name:   result.Name,
				Client: client,
			},
		},
		speedtest.Config{
			Method:      toSpeedtestMethod(method),
			Attempts:    echoRuns,
			Timeout:     timeout,
			Temperature: &temp,
			EchoText:    echoText,
			OnEvent: func(ev speedtest.Event) {
				if !live || ev.Type != speedtest.EventAttemptDone || ev.Attempt == nil {
					return
				}
				attempt := fromSpeedtestAttempt(*ev.Attempt, provider, t.APIBase)
				printAttemptResult(attempt)
			},
		},
	)
	if err != nil {
		result.SetupError = err.Error()
		if live {
			printSetupError(result.SetupError)
		}
		return result
	}
	if len(report.Results) == 0 {
		result.SetupError = "no result returned"
		if live {
			printSetupError(result.SetupError)
		}
		return result
	}

	caseResult := report.Results[0]
	result.SetupError = caseResult.SetupError
	result.Average = caseResult.Average
	result.Runs = make([]attemptResult, 0, len(caseResult.Attempts))
	for _, a := range caseResult.Attempts {
		result.Runs = append(result.Runs, fromSpeedtestAttempt(a, provider, t.APIBase))
	}
	if result.Model == "" {
		result.Model = caseResult.Model
	}

	if live {
		if result.SetupError != "" {
			printSetupError(result.SetupError)
		} else {
			printAverage(result.Average)
		}
	}

	return result
}

func fromSpeedtestAttempt(a speedtest.AttemptResult, provider, apiBase string) attemptResult {
	out := attemptResult{
		Index:     a.Index,
		Duration:  a.Duration,
		OK:        a.OK,
		EchoMatch: a.Match,
		Err:       a.Err,
		Response:  a.Response,
	}
	if !out.OK && out.Err != "" {
		out.Err = annotateAPIErrorMessage(out.Err, provider, apiBase)
	}
	return out
}

func toSpeedtestMethod(method string) speedtest.Method {
	switch method {
	case methodToolCalling:
		return speedtest.MethodToolCalling
	case methodEcho:
		fallthrough
	default:
		return speedtest.MethodEcho
	}
}

func annotateAPIErrorMessage(msg, provider, apiBase string) string {
	lower := strings.ToLower(msg)

	if strings.Contains(lower, "content-type 'text/html") || strings.Contains(lower, "not 'application/json'") {
		base := strings.TrimRight(strings.TrimSpace(apiBase), "/")
		switch provider {
		case "openai", "openai_custom", "deepseek", "xai":
			if base == "" {
				return msg + "; hint: got HTML instead of JSON, check provider endpoint or proxy settings"
			}
			if strings.HasSuffix(base, "/api") {
				return msg + "; hint: api_base may be missing /v1, try " + base + "/v1"
			}
			return msg + "; hint: OpenAI-compatible api_base usually ends with /v1 (example: https://openrouter.ai/api/v1)"
		default:
			return msg + "; hint: got HTML instead of JSON, check api_base and endpoint"
		}
	}

	return msg
}
