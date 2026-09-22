package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/quailyquaily/uniai/evaluate"
)

const testCaseJSON = `{"id":"mixed","category":"test","domain":"general","language":"en","state":{"text":"refund"},"questions":{"refund":{"kind":"boolean","instructions":"Refund?"},"team":{"kind":"choice","instructions":"Team?","options":{"billing":"Payments","support":"Help"}},"priority":{"kind":"score","instructions":"Priority?","levels":["low","high"]}},"expected":{"refund":false,"team":"billing","priority":0},"rationale":"This explanation must never be sent to the model."}`

func ptr[T any](v T) *T { return &v }

func testCases(t *testing.T) []benchmarkCase {
	t.Helper()
	cases, err := loadCases(strings.NewReader(testCaseJSON))
	if err != nil {
		t.Fatal(err)
	}
	return cases
}

func testResult() *evaluate.Result {
	return &evaluate.Result{Model: "served-model", Answers: map[string]evaluate.Answer{
		"refund":   {Kind: evaluate.Boolean, BooleanValue: ptr(false)},
		"team":     {Kind: evaluate.Choice, Selected: "billing"},
		"priority": {Kind: evaluate.Score, ScoreValue: ptr(0.0)},
	}}
}

func TestLoadCasesValidation(t *testing.T) {
	for _, data := range []string{
		"", testCaseJSON + "\n" + testCaseJSON, testCaseJSON + " {}",
		strings.Replace(testCaseJSON, `"id":"mixed"`, `"id":""`, 1),
		strings.Replace(testCaseJSON, `"category":"test"`, `"category":""`, 1),
		strings.Replace(testCaseJSON, `"domain":"general"`, `"domain":"unknown"`, 1),
		strings.Replace(testCaseJSON, `"domain":"general",`, ``, 1),
		strings.Replace(testCaseJSON, `"language":"en",`, ``, 1),
		strings.Replace(testCaseJSON, `"state":`, `"typo":`, 1),
		strings.Replace(testCaseJSON, `"refund":false`, `"refund":null`, 1),
		strings.Replace(testCaseJSON, `"refund":false`, `"refund":"false"`, 1),
		strings.Replace(testCaseJSON, `"team":"billing"`, `"team":"unknown"`, 1),
		strings.Replace(testCaseJSON, `"priority":0`, `"priority":0.5`, 1),
		strings.Replace(testCaseJSON, `"priority":0`, `"priority":2`, 1),
		strings.Replace(testCaseJSON, `"refund":false,`, "", 1),
		strings.Replace(testCaseJSON, `"expected":{`, `"expected":{"extra":true,`, 1),
	} {
		if _, err := loadCases(strings.NewReader(data)); err == nil {
			t.Fatalf("accepted invalid dataset: %s", data)
		}
	}
	if cases, err := loadCases(strings.NewReader("\n" + testCaseJSON + "\n")); err != nil || len(cases) != 1 {
		t.Fatalf("cases=%v err=%v", cases, err)
	}
	valid := strings.Replace(testCaseJSON, `"domain":"general"`, `"domain":"crypto"`, 1)
	valid = strings.Replace(valid, `"language":"en"`, `"language":"de"`, 1)
	if _, err := loadCases(strings.NewReader(valid)); err != nil {
		t.Fatalf("valid dimensions rejected: %v", err)
	}
}

func TestGradeNativeAndEmulated(t *testing.T) {
	c := testCases(t)[0]
	out := testResult()
	checks := grade(c, out, 0.5, 0.5)
	for id, check := range checks {
		if !check.Match {
			t.Fatalf("%s: %+v", id, check)
		}
	}
	out.Answers["refund"] = evaluate.Answer{Kind: evaluate.Boolean, ProbabilityTrue: ptr(0.2)}
	out.Answers["priority"] = evaluate.Answer{Kind: evaluate.Score, ScoreValue: ptr(0.4)}
	checks = grade(c, out, 0.5, 0.5)
	if !checks["refund"].Match || *checks["refund"].Brier < 0.0399 || *checks["refund"].Brier > 0.0401 || !checks["priority"].Match || *checks["priority"].AbsoluteError != 0.4 {
		t.Fatal(checks)
	}
	checks = grade(c, out, 0.1, 0.2)
	if checks["refund"].Match || checks["priority"].Match {
		t.Fatal("ignored scoring controls")
	}
	if out.Answers["refund"].BooleanValue != nil {
		t.Fatal("grading mutated native result")
	}
}

func TestRunBenchmarkAccountingAndIsolation(t *testing.T) {
	cases := testCases(t)
	base := evaluate.Request{Provider: "typesafe", Model: "jev-test", EmulationMode: evaluate.EmulationOff}
	calls, events := 0, 0
	fn := func(ctx context.Context, r evaluate.Request) (*evaluate.Result, error) {
		calls++
		if _, ok := ctx.Deadline(); !ok {
			t.Error("missing per-call deadline")
		}
		data, _ := json.Marshal(r)
		if strings.Contains(string(data), "expected") || strings.Contains(string(data), "explanation") {
			t.Fatal("gold labels leaked")
		}
		if r.Provider != "typesafe" || r.Model != "jev-test" {
			t.Fatal(r)
		}
		switch calls {
		case 3:
			return nil, errors.New("upstream failure")
		case 4:
			out := testResult()
			out.Answers["team"] = evaluate.Answer{Kind: evaluate.Choice, Selected: "support"}
			return out, nil
		}
		return testResult(), nil
	}
	report, err := runBenchmark(context.Background(), cases, base, runOptions{Repeat: 3, Warmup: 1, Timeout: time.Second, BooleanThreshold: 0.5, ScoreTolerance: 0.5}, fn, func(a attemptResult) error { events++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if calls != 4 || events != 3 || len(report.Attempts) != 3 || len(report.Warmups) != 1 {
		t.Fatalf("calls=%d events=%d report=%+v", calls, events, report)
	}
	s := report.Summary
	if s.Attempts != 3 || s.Succeeded != 2 || s.Failed != 1 || s.MatchedCases != 1 || s.ExpectedJudgments != 9 || s.ValidJudgments != 6 || s.MatchedJudgments != 5 || s.SuccessLatency.Count != 2 || s.ErrorLatency.Count != 1 {
		t.Fatalf("summary=%+v", s)
	}
	if s.Accuracy == nil || *s.Accuracy != float64(5)/9 {
		t.Fatal(s.Accuracy)
	}
	if report.ByCategory["test"].Attempts != 3 {
		t.Fatal(report.ByCategory)
	}
	if report.ByDomain["general"].Attempts != 3 || report.ByLanguage["en"].Attempts != 3 {
		t.Fatalf("dimension summaries missing: domain=%v language=%v", report.ByDomain, report.ByLanguage)
	}
	if report.Attempts[0].Domain != "general" || report.Attempts[0].Language != "en" {
		t.Fatalf("attempt dimensions missing: %+v", report.Attempts[0])
	}
	if report.Attempts[1].Result != nil || report.Attempts[1].Error == "" {
		t.Fatal("failure recorded as result")
	}
	if cases[0].State == nil || base.State != nil || base.Questions != nil {
		t.Fatal("input mutated")
	}
}

func TestRunBenchmarkCancellationAndInvalidResult(t *testing.T) {
	cases := testCases(t)
	base := evaluate.Request{Provider: "typesafe", Model: "jev-test"}
	opts := runOptions{Repeat: 2, Timeout: time.Second, BooleanThreshold: 0.5, ScoreTolerance: 0.5}
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	report, err := runBenchmark(ctx, cases, base, opts, func(context.Context, evaluate.Request) (*evaluate.Result, error) {
		calls++
		cancel()
		return nil, context.Canceled
	}, nil)
	if !errors.Is(err, context.Canceled) || calls != 1 || len(report.Attempts) != 1 {
		t.Fatalf("calls=%d report=%v err=%v", calls, report, err)
	}
	opts.Repeat = 1
	report, err = runBenchmark(context.Background(), cases, base, opts, func(context.Context, evaluate.Request) (*evaluate.Result, error) { return nil, nil }, nil)
	if err != nil || report.Summary.Failed != 1 {
		t.Fatalf("nil result accepted: %v %v", report, err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	report, err = runBenchmark(ctx, cases, base, opts, func(context.Context, evaluate.Request) (*evaluate.Result, error) {
		t.Fatal("called after cancellation")
		return nil, nil
	}, nil)
	if !errors.Is(err, context.Canceled) || report.Summary.Attempts != 0 || report.Summary.Accuracy != nil {
		t.Fatal(report, err)
	}
}

func TestLatencyPercentiles(t *testing.T) {
	got := latencies([]float64{10, 1, 4, 2, 3})
	if got.Count != 5 || got.MinMS != 1 || got.MaxMS != 10 || got.MeanMS != 4 || got.P50MS != 3 || got.P95MS != 10 {
		t.Fatal(got)
	}
	if got := latencies(nil); got.Count != 0 {
		t.Fatal(got)
	}
}

func TestWarmupFailureAndPerCallTimeout(t *testing.T) {
	cases := testCases(t)
	base := evaluate.Request{Provider: "typesafe", Model: "jev-test"}
	opts := runOptions{Repeat: 2, Warmup: 1, Timeout: time.Millisecond, BooleanThreshold: 0.5, ScoreTolerance: 0.5}
	calls := 0
	report, err := runBenchmark(context.Background(), cases, base, opts, func(context.Context, evaluate.Request) (*evaluate.Result, error) {
		calls++
		return nil, errors.New("warmup error")
	}, nil)
	if err == nil || calls != 1 || report.Summary.Attempts != 0 || len(report.Warmups) != 1 {
		t.Fatal(report, err, calls)
	}
	opts.Warmup = 0
	calls = 0
	report, err = runBenchmark(context.Background(), cases, base, opts, func(ctx context.Context, _ evaluate.Request) (*evaluate.Result, error) {
		calls++
		if calls == 1 {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return testResult(), nil
	}, nil)
	if err != nil || report.Summary.Failed != 1 || report.Summary.Succeeded != 1 || calls != 2 {
		t.Fatal(report, err, calls)
	}
}

func TestRunBenchmarkPreservesFailedUsage(t *testing.T) {
	out := &evaluate.Result{Provider: "typesafe", Model: "served-model", Usage: &evaluate.Usage{InputTokens: ptr(100), OutputTokens: ptr(0)}}
	report, err := runBenchmark(context.Background(), testCases(t), evaluate.Request{Provider: "typesafe", Model: "jev-test"}, runOptions{Repeat: 1, Timeout: time.Second, BooleanThreshold: 0.5, ScoreTolerance: 0.5}, func(context.Context, evaluate.Request) (*evaluate.Result, error) {
		return out, evaluate.ErrInvalidResponse
	}, nil)
	if err != nil || len(report.Attempts) != 1 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	a := report.Attempts[0]
	if a.Error == "" || a.Result != out || len(a.Checks) != 0 || report.Summary.Failed != 1 || report.Summary.Succeeded != 0 || report.Summary.ValidJudgments != 0 {
		t.Fatalf("lost usage or graded failed result: attempt=%+v summary=%+v", a, report.Summary)
	}
}
