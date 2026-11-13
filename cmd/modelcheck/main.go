// Command modelcheck runs a standard set of live chat checks against many
// models: basic text, JSON output, prompt-cache hits, tool calling, and pricing.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/quailyquaily/uniai"
)

type modelResult struct {
	Name     string       `json:"name"`
	Provider string       `json:"provider"`
	Model    string       `json:"model"`
	KeyEnv   string       `json:"key_env,omitempty"`
	APIBase  string       `json:"api_base,omitempty"`
	Skip     string       `json:"skip,omitempty"`
	Cases    []caseResult `json:"cases"`
}

type options struct {
	configPath string
	only       []string
	cases      []string
	parallel   int
	jsonOut    string
	list       bool
	verbose    bool
	color      string
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	failed, err := run(ctx, os.Args[1:], os.Stdout, os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "modelcheck:", err)
		os.Exit(2)
	}
	if failed {
		os.Exit(1)
	}
}

func parseFlags(args []string) (options, error) {
	var o options
	var only, cases string
	fs := flag.NewFlagSet("modelcheck", flag.ContinueOnError)
	fs.StringVar(&o.configPath, "config", "", "YAML config path (default: built-in latest-model config)")
	fs.StringVar(&only, "only", "", "comma-separated model names or substrings to run")
	fs.StringVar(&cases, "cases", strings.Join(allCases, ","), "comma-separated cases: "+strings.Join(allCases, ","))
	fs.IntVar(&o.parallel, "parallel", 4, "models to check concurrently")
	fs.StringVar(&o.jsonOut, "json", "", "write the full report as JSON to this path")
	fs.BoolVar(&o.list, "list", false, "list models and credential status without calling any API")
	fs.BoolVar(&o.verbose, "v", false, "print every case detail and response, not only failures")
	fs.StringVar(&o.color, "color", "auto", "colored output: auto (terminal only), always, or never")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if fs.NArg() > 0 {
		return o, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	o.only = splitList(only)
	o.cases = splitList(cases)
	for _, c := range o.cases {
		if !isKnownCase(c) {
			return o, fmt.Errorf("unknown case %q", c)
		}
	}
	if len(o.cases) == 0 {
		return o, errors.New("no cases selected")
	}
	if o.parallel < 1 {
		o.parallel = 1
	}
	switch o.color {
	case "auto", "always", "never":
	default:
		return o, fmt.Errorf("invalid --color %q; use auto, always, or never", o.color)
	}
	return o, nil
}

func run(ctx context.Context, args []string, out io.Writer, getenv func(string) string) (bool, error) {
	o, err := parseFlags(args)
	if err != nil {
		return false, err
	}
	cfg, err := loadConfig(o.configPath)
	if err != nil {
		return false, err
	}
	models := filterModels(cfg.Models, o.only)
	if len(models) == 0 {
		return false, fmt.Errorf("no models match --only %s", strings.Join(o.only, ","))
	}

	p := painter{enabled: colorEnabled(o.color, out, getenv)}
	if o.list {
		printList(out, p, models, getenv)
		return false, nil
	}

	nonce := fmt.Sprintf("%d", time.Now().UnixNano())
	results := make([]modelResult, len(models))
	sem := make(chan struct{}, o.parallel)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i, m := range models {
		wg.Add(1)
		go func(i int, m modelConfig) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = checkModel(ctx, cfg, m, o.cases, nonce+"-"+fmt.Sprint(i), getenv)
			mu.Lock()
			fmt.Fprintln(out, progressLine(p, results[i]))
			mu.Unlock()
		}(i, m)
	}
	wg.Wait()

	fmt.Fprintln(out)
	printTable(out, p, results, o.cases)
	printDetails(out, p, results, o.verbose)
	printSummary(out, p, results)
	if o.jsonOut != "" {
		data, _ := json.MarshalIndent(results, "", "  ")
		if err := os.WriteFile(o.jsonOut, data, 0o644); err != nil {
			return false, fmt.Errorf("write report: %w", err)
		}
		fmt.Fprintf(out, "report written to %s\n", o.jsonOut)
	}
	return anyFailed(results), nil
}

func checkModel(ctx context.Context, cfg *fileConfig, m modelConfig, selected []string, nonce string, getenv func(string) string) modelResult {
	res := modelResult{Name: m.Name, Provider: m.Provider, Model: m.Model}
	r, skip := resolveEnv(m, getenv)
	res.KeyEnv, res.APIBase = r.keyEnv, r.apiBase
	cases := modelCases(m, selected)
	if skip != "" {
		res.Skip = skip
		for _, c := range cases {
			res.Cases = append(res.Cases, caseResult{Case: c, Status: statusSkip, Detail: skip})
		}
		return res
	}

	client := uniai.New(buildClientConfig(m, r, getenv))
	timeout := cfg.TimeoutSeconds
	if m.TimeoutSeconds > 0 {
		timeout = m.TimeoutSeconds
	}
	maxTokens := cfg.MaxTokens
	if m.MaxTokens > 0 {
		maxTokens = m.MaxTokens
	}
	rn := &runner{
		chat:          client.Chat,
		model:         m,
		apiBase:       r.apiBase,
		maxTokens:     maxTokens,
		timeout:       time.Duration(timeout) * time.Second,
		cacheAttempts: cfg.CacheAttempts,
		cacheDelay:    2 * time.Second,
		nonce:         nonce,
		catalog:       uniai.DefaultPricingCatalog(),
	}
	for _, c := range cases {
		if ctx.Err() != nil {
			res.Cases = append(res.Cases, caseResult{Case: c, Status: statusSkip, Detail: "interrupted"})
			continue
		}
		res.Cases = append(res.Cases, rn.run(ctx, c))
	}
	return res
}

// modelCases intersects the CLI selection with the model's own case list.
// The pricing case always runs last so it can inspect earlier responses.
func modelCases(m modelConfig, selected []string) []string {
	var out []string
	pricing := false
	for _, c := range selected {
		if len(m.Cases) > 0 && !contains(m.Cases, c) {
			continue
		}
		if c == casePricing {
			pricing = true
			continue
		}
		out = append(out, c)
	}
	if pricing {
		out = append(out, casePricing)
	}
	return out
}

func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

func filterModels(models []modelConfig, only []string) []modelConfig {
	if len(only) == 0 {
		return models
	}
	var out []modelConfig
	for _, m := range models {
		for _, pattern := range only {
			if m.Name == pattern || strings.Contains(m.Name, pattern) || strings.Contains(m.Model, pattern) {
				out = append(out, m)
				break
			}
		}
	}
	return out
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func anyFailed(results []modelResult) bool {
	for _, r := range results {
		for _, c := range r.Cases {
			if c.Status == statusFail {
				return true
			}
		}
	}
	return false
}
