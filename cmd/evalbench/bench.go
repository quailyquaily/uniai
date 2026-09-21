package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/quailyquaily/uniai/evaluate"
)

type runOptions struct {
	Repeat           int           `json:"repeat"`
	Warmup           int           `json:"warmup"`
	Timeout          time.Duration `json:"timeout_ns"`
	BooleanThreshold float64       `json:"boolean_threshold"`
	ScoreTolerance   float64       `json:"score_tolerance"`
}

type answerCheck struct {
	Kind          evaluate.Kind   `json:"kind"`
	Expected      json.RawMessage `json:"expected"`
	Actual        any             `json:"actual"`
	Match         bool            `json:"match"`
	AbsoluteError *float64        `json:"absolute_error,omitempty"`
	Brier         *float64        `json:"brier,omitempty"`
}

type attemptResult struct {
	CaseID            string                 `json:"case_id"`
	Category          string                 `json:"category"`
	Iteration         int                    `json:"iteration"`
	DurationMS        float64                `json:"duration_ms"`
	ExpectedJudgments int                    `json:"expected_judgments"`
	Error             string                 `json:"error,omitempty"`
	Result            *evaluate.Result       `json:"result,omitempty"`
	Checks            map[string]answerCheck `json:"checks,omitempty"`
}

type latencyStats struct {
	Count  int     `json:"count"`
	MinMS  float64 `json:"min_ms"`
	MeanMS float64 `json:"mean_ms"`
	P50MS  float64 `json:"p50_ms"`
	P95MS  float64 `json:"p95_ms"`
	MaxMS  float64 `json:"max_ms"`
}

type summary struct {
	Attempts          int          `json:"attempts"`
	Succeeded         int          `json:"succeeded"`
	Failed            int          `json:"failed"`
	MatchedCases      int          `json:"matched_cases"`
	ExpectedJudgments int          `json:"expected_judgments"`
	ValidJudgments    int          `json:"valid_judgments"`
	MatchedJudgments  int          `json:"matched_judgments"`
	Accuracy          *float64     `json:"accuracy,omitempty"`
	ValidAccuracy     *float64     `json:"valid_accuracy,omitempty"`
	ScoreCount        int          `json:"score_count"`
	ScoreMAE          *float64     `json:"score_mae,omitempty"`
	ProbabilityCount  int          `json:"probability_count"`
	BrierMean         *float64     `json:"brier_mean,omitempty"`
	SuccessLatency    latencyStats `json:"success_latency"`
	ErrorLatency      latencyStats `json:"error_latency"`
}

type benchmarkReport struct {
	Version               int                        `json:"version"`
	Preset                string                     `json:"preset,omitempty"`
	APIPattern            string                     `json:"api_pattern"`
	DatasetSHA256         string                     `json:"dataset_sha256"`
	CaseIDs               []string                   `json:"case_ids"`
	Seed                  int64                      `json:"shuffle_seed"`
	StartedAt             time.Time                  `json:"started_at"`
	WallTimeMS            float64                    `json:"wall_time_ms"`
	Provider              string                     `json:"provider"`
	Model                 string                     `json:"requested_model"`
	Mode                  evaluate.EmulationMode     `json:"emulation_mode"`
	EmulationOptions      *evaluate.EmulationOptions `json:"emulation_options,omitempty"`
	InferenceProvider     string                     `json:"inference_provider,omitempty"`
	Options               runOptions                 `json:"options"`
	Warmups               []attemptResult            `json:"warmups,omitempty"`
	Attempts              []attemptResult            `json:"attempts"`
	Summary               summary                    `json:"summary"`
	ClassificationSummary summary                    `json:"classification_summary"`
	ByCategory            map[string]summary         `json:"by_category"`
	Error                 string                     `json:"error,omitempty"`
}

func runBenchmark(ctx context.Context, cases []benchmarkCase, base evaluate.Request, opts runOptions, call func(context.Context, evaluate.Request) (*evaluate.Result, error), onAttempt func(attemptResult) error) (report *benchmarkReport, err error) {
	if len(cases) == 0 {
		return nil, fmt.Errorf("empty case selection")
	}
	if err := opts.validate(); err != nil {
		return nil, err
	}
	start := time.Now()
	report = &benchmarkReport{Version: 1, StartedAt: start.UTC(), Provider: base.Provider, Model: base.Model, Mode: base.EmulationMode, EmulationOptions: base.EmulationOptions, InferenceProvider: base.InferenceProvider, Options: opts, Attempts: []attemptResult{}}
	for _, c := range cases {
		report.CaseIDs = append(report.CaseIDs, c.ID)
	}
	defer func() {
		report.WallTimeMS = float64(time.Since(start)) / float64(time.Millisecond)
		report.Summary = summarize(report.Attempts)
		groups := map[string][]attemptResult{}
		var classification []attemptResult
		for _, a := range report.Attempts {
			groups[a.Category] = append(groups[a.Category], a)
			switch a.Category {
			case "refund_intent", "support_routing", "content_topic", "language_detection", "intent_classification", "moderation_category", "sentiment_intensity", "retrieval_relevance":
				classification = append(classification, a)
			}
		}
		report.ClassificationSummary = summarize(classification)
		report.ByCategory = make(map[string]summary, len(groups))
		for key, attempts := range groups {
			report.ByCategory[key] = summarize(attempts)
		}
		if err != nil {
			report.Error = err.Error()
		}
	}()
	for n := 0; n < opts.Warmup; n++ {
		if err = ctx.Err(); err != nil {
			return report, err
		}
		c := cases[n%len(cases)]
		a := runAttempt(ctx, c, base, opts, 0, call)
		report.Warmups = append(report.Warmups, a)
		if a.Error != "" {
			return report, fmt.Errorf("warmup failed: %s", a.Error)
		}
	}
	for iteration := 1; iteration <= opts.Repeat; iteration++ {
		for _, c := range cases {
			if err = ctx.Err(); err != nil {
				return report, err
			}
			a := runAttempt(ctx, c, base, opts, iteration, call)
			report.Attempts = append(report.Attempts, a)
			if onAttempt != nil {
				if err = onAttempt(a); err != nil {
					return report, err
				}
			}
		}
	}
	return report, ctx.Err()
}

func runAttempt(ctx context.Context, c benchmarkCase, base evaluate.Request, opts runOptions, iteration int, call func(context.Context, evaluate.Request) (*evaluate.Result, error)) attemptResult {
	r := base
	r.State = c.State
	r.Questions = c.Questions
	a := attemptResult{CaseID: c.ID, Category: c.Category, Iteration: iteration, ExpectedJudgments: len(c.Questions)}
	callCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	start := time.Now()
	out, err := call(callCtx, r)
	a.DurationMS = float64(time.Since(start)) / float64(time.Millisecond)
	if err == nil {
		err = callCtx.Err()
	}
	cancel()
	if err == nil {
		err = evaluate.ValidateResult(&r, out)
	}
	if err != nil {
		a.Error = err.Error()
		return a
	}
	a.Result = out
	a.Checks = grade(c, out, opts.BooleanThreshold, opts.ScoreTolerance)
	return a
}

// Gold labels stay here: the provider receives only State and Questions.
func grade(c benchmarkCase, out *evaluate.Result, threshold, tolerance float64) map[string]answerCheck {
	checks := make(map[string]answerCheck, len(c.Questions))
	for id, q := range c.Questions {
		a := out.Answers[id]
		check := answerCheck{Kind: q.Kind, Expected: c.Expected[id]}
		switch q.Kind {
		case evaluate.Boolean:
			var want bool
			_ = json.Unmarshal(c.Expected[id], &want)
			var actual bool
			if a.BooleanValue != nil {
				actual = *a.BooleanValue
			} else {
				actual = *a.ProbabilityTrue >= threshold
			}
			check.Actual = actual
			check.Match = actual == want
			if a.ProbabilityTrue != nil {
				target := 0.0
				if want {
					target = 1
				}
				brier := math.Pow(*a.ProbabilityTrue-target, 2)
				check.Brier = &brier
			}
		case evaluate.Choice:
			var want string
			_ = json.Unmarshal(c.Expected[id], &want)
			check.Actual = a.Selected
			check.Match = a.Selected == want
		case evaluate.Score:
			var want float64
			_ = json.Unmarshal(c.Expected[id], &want)
			absolute := math.Abs(*a.ScoreValue - want)
			check.Actual = *a.ScoreValue
			check.Match = absolute <= tolerance
			check.AbsoluteError = &absolute
		}
		checks[id] = check
	}
	return checks
}

func summarize(attempts []attemptResult) summary {
	s := summary{Attempts: len(attempts)}
	var successTimes, errorTimes []float64
	var scoreError, brier float64
	for _, a := range attempts {
		s.ExpectedJudgments += a.ExpectedJudgments
		if a.Error != "" {
			s.Failed++
			errorTimes = append(errorTimes, a.DurationMS)
			continue
		}
		s.Succeeded++
		successTimes = append(successTimes, a.DurationMS)
		allMatch := true
		for _, check := range a.Checks {
			s.ValidJudgments++
			if check.Match {
				s.MatchedJudgments++
			} else {
				allMatch = false
			}
			if check.AbsoluteError != nil {
				s.ScoreCount++
				scoreError += *check.AbsoluteError
			}
			if check.Brier != nil {
				s.ProbabilityCount++
				brier += *check.Brier
			}
		}
		if allMatch {
			s.MatchedCases++
		}
	}
	if s.ExpectedJudgments > 0 {
		v := float64(s.MatchedJudgments) / float64(s.ExpectedJudgments)
		s.Accuracy = &v
	}
	if s.ValidJudgments > 0 {
		v := float64(s.MatchedJudgments) / float64(s.ValidJudgments)
		s.ValidAccuracy = &v
	}
	if s.ScoreCount > 0 {
		v := scoreError / float64(s.ScoreCount)
		s.ScoreMAE = &v
	}
	if s.ProbabilityCount > 0 {
		v := brier / float64(s.ProbabilityCount)
		s.BrierMean = &v
	}
	s.SuccessLatency = latencies(successTimes)
	s.ErrorLatency = latencies(errorTimes)
	return s
}

func latencies(values []float64) latencyStats {
	if len(values) == 0 {
		return latencyStats{}
	}
	ordered := append([]float64(nil), values...)
	sort.Float64s(ordered)
	var sum float64
	for _, v := range ordered {
		sum += v
	}
	return latencyStats{Count: len(ordered), MinMS: ordered[0], MeanMS: sum / float64(len(ordered)), P50MS: ordered[int(math.Ceil(0.5*float64(len(ordered))))-1], P95MS: ordered[int(math.Ceil(0.95*float64(len(ordered))))-1], MaxMS: ordered[len(ordered)-1]}
}
