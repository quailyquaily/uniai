package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/quailyquaily/uniai"
	"github.com/quailyquaily/uniai/evaluate"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) error {
	o, err := parseOptions(args, getenv, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	data := []byte(builtinCases)
	if o.casePath != "" {
		data, err = os.ReadFile(o.casePath)
		if err != nil {
			return fmt.Errorf("read cases: %w", err)
		}
	}
	cases, err := loadCases(strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	cases, err = selectCases(cases, o.category, o.limit, o.seed)
	if err != nil {
		return err
	}
	if o.list {
		for _, c := range cases {
			if _, err := fmt.Fprintf(stdout, "%s\t%s\t%s\tquestions=%d\n", c.ID, c.Category, c.Language, len(c.Questions)); err != nil {
				return err
			}
		}
		return nil
	}
	apiPattern := "evaluate"
	requestsPerPass, warmupRequests := len(cases), o.options.Warmup
	if o.request.Provider == "jina" {
		apiPattern = "jina_classify"
		requestsPerPass = 0
		for _, c := range cases {
			r := o.request
			r.State, r.Questions = c.State, c.Questions
			questions, err := prepareJinaQuestions(r)
			if err != nil {
				return fmt.Errorf("case %q: %w", c.ID, err)
			}
			requestsPerPass += len(questions)
		}
		// Warmup cycles over cases, including each question in a case.
		cycles, remainder := o.options.Warmup/len(cases), o.options.Warmup%len(cases)
		partial := 0
		for _, c := range cases[:remainder] {
			partial += len(c.Questions)
		}
		if cycles > (int(^uint(0)>>1)-partial)/requestsPerPass {
			return fmt.Errorf("request count overflows int")
		}
		warmupRequests = cycles*requestsPerPass + partial
	}
	// Check arithmetic before displaying or running the requested work.
	if o.options.Repeat > (int(^uint(0)>>1)-warmupRequests)/requestsPerPass {
		return fmt.Errorf("request count overflows int")
	}
	digest := sha256.Sum256(data)
	fingerprint := hex.EncodeToString(digest[:])
	controls, _ := json.Marshal(o.request.EmulationOptions)
	if o.dryRun {
		_, err = fmt.Fprintf(stdout, "preset=%s api_pattern=%s provider=%s model=%s mode=%s emulation_options=%s cases=%d repeat=%d warmup=%d requests=%d seed=%d dataset_sha256=%s\n", o.preset, apiPattern, o.request.Provider, o.request.Model, o.request.EmulationMode, controls, len(cases), o.options.Repeat, o.options.Warmup, requestsPerPass*o.options.Repeat+warmupRequests, o.seed, fingerprint)
		return err
	}
	cfg, err := clientConfig(o, getenv)
	if err != nil {
		return err
	}
	var output *os.File
	if o.output != "" {
		if err := os.MkdirAll(filepath.Dir(o.output), 0755); err != nil {
			return err
		}
		output, err = os.OpenFile(o.output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return fmt.Errorf("create output (must be a new file): %w", err)
		}
		defer output.Close()
	}
	if _, err := fmt.Fprintf(stdout, "preset=%s api_pattern=%s provider=%s model=%s mode=%s emulation_options=%s cases=%d repeat=%d warmup=%d timeout=%s seed=%d dataset_sha256=%s\n", o.preset, apiPattern, o.request.Provider, o.request.Model, o.request.EmulationMode, controls, len(cases), o.options.Repeat, o.options.Warmup, o.options.Timeout, o.seed, fingerprint); err != nil {
		return err
	}
	client := uniai.New(cfg)
	call := client.Evaluate
	if o.request.Provider == "jina" {
		call = func(ctx context.Context, req evaluate.Request) (*evaluate.Result, error) {
			return classifyWithJina(ctx, client, req)
		}
	}
	index := 0
	report, runErr := runBenchmark(ctx, cases, o.request, o.options, call, func(a attemptResult) error {
		index++
		if a.Error != "" {
			_, err := fmt.Fprintf(stdout, "[%d] %s repeat=%d %.2fms ERROR %q\n", index, a.CaseID, a.Iteration, a.DurationMS, a.Error)
			return err
		}
		answers, err := json.Marshal(a.Result.Answers)
		if err != nil {
			return err
		}
		matched := 0
		for _, c := range a.Checks {
			if c.Match {
				matched++
			}
		}
		_, err = fmt.Fprintf(stdout, "[%d] %s repeat=%d %.2fms match=%d/%d model=%s emulated=%t answers=%s\n", index, a.CaseID, a.Iteration, a.DurationMS, matched, len(a.Checks), a.Result.Model, a.Result.Emulated, answers)
		return err
	})
	if report == nil {
		return runErr
	}
	report.DatasetSHA256 = fingerprint
	report.Preset = o.preset
	report.APIPattern = apiPattern
	report.Seed = o.seed
	if output != nil {
		encoder := json.NewEncoder(output)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			return fmt.Errorf("write report: %w", err)
		}
		if err := output.Close(); err != nil {
			return err
		}
	}
	if err := printSummary(stdout, report); err != nil {
		return err
	}
	if runErr != nil {
		return runErr
	}
	if report.Summary.Failed > 0 {
		return fmt.Errorf("%d measured calls failed; see individual errors", report.Summary.Failed)
	}
	return nil
}

func selectCases(all []benchmarkCase, category string, limit int, seed int64) ([]benchmarkCase, error) {
	selected := make([]benchmarkCase, 0, len(all))
	for _, c := range all {
		if category == "" || c.Category == category {
			selected = append(selected, c)
		}
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("no cases match category %q", category)
	}
	if seed != 0 {
		rand.New(rand.NewSource(seed)).Shuffle(len(selected), func(i, j int) { selected[i], selected[j] = selected[j], selected[i] })
	}
	if limit > 0 && limit < len(selected) {
		selected = selected[:limit]
	}
	return selected, nil
}

func printSummary(w io.Writer, r *benchmarkReport) error {
	s := r.Summary
	if _, err := fmt.Fprintf(w, "\nsummary: attempts=%d ok=%d errors=%d matched_cases=%d/%d judgments=%d/%d valid_judgments=%d wall=%.2fms\n", s.Attempts, s.Succeeded, s.Failed, s.MatchedCases, s.Attempts, s.MatchedJudgments, s.ExpectedJudgments, s.ValidJudgments, r.WallTimeMS); err != nil {
		return err
	}
	for _, group := range []struct {
		name  string
		stats latencyStats
	}{{"success", s.SuccessLatency}, {"error", s.ErrorLatency}} {
		v := group.stats
		if _, err := fmt.Fprintf(w, "latency[%s]: n=%d mean=%.2fms p50=%.2fms p95=%.2fms min=%.2fms max=%.2fms\n", group.name, v.Count, v.MeanMS, v.P50MS, v.P95MS, v.MinMS, v.MaxMS); err != nil {
			return err
		}
	}
	if s.Accuracy != nil {
		if _, err := fmt.Fprintf(w, "judgment_match_rate=%.2f%% (call errors count as mismatches)\n", 100**s.Accuracy); err != nil {
			return err
		}
	}
	if c := r.ClassificationSummary; c.Attempts > 0 {
		if _, err := fmt.Fprintf(w, "classification_subset: attempts=%d ok=%d errors=%d judgments=%d/%d success_mean=%.2fms p50=%.2fms p95=%.2fms\n", c.Attempts, c.Succeeded, c.Failed, c.MatchedJudgments, c.ExpectedJudgments, c.SuccessLatency.MeanMS, c.SuccessLatency.P50MS, c.SuccessLatency.P95MS); err != nil {
			return err
		}
	}
	if s.ScoreMAE != nil {
		if _, err := fmt.Fprintf(w, "score_mae=%.4f n=%d tolerance=%.4f\n", *s.ScoreMAE, s.ScoreCount, r.Options.ScoreTolerance); err != nil {
			return err
		}
	}
	if s.BrierMean != nil {
		if _, err := fmt.Fprintf(w, "boolean_brier=%.4f n=%d threshold=%.4f\n", *s.BrierMean, s.ProbabilityCount, r.Options.BooleanThreshold); err != nil {
			return err
		}
	}
	keys := make([]string, 0, len(r.ByCategory))
	for key := range r.ByCategory {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		v := r.ByCategory[key]
		if _, err := fmt.Fprintf(w, "category[%s]: ok=%d errors=%d judgments=%d/%d p95=%.2fms\n", key, v.Succeeded, v.Failed, v.MatchedJudgments, v.ExpectedJudgments, v.SuccessLatency.P95MS); err != nil {
			return err
		}
	}
	return nil
}
