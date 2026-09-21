package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/quailyquaily/uniai/evaluate"
)

const jinaCaseJSON = `{"id":"gold-case-id","category":"content_topic","domain":"general","language":"zh","state":"请把重复扣的钱退回来。","questions":{"refund":{"kind":"boolean","instructions":"是否申请退款？","true_description":"申请退款","false_description":"未申请退款"},"team":{"kind":"choice","instructions":"负责团队？","options":{"billing":"账单问题","shipping":"物流问题"}},"priority":{"kind":"score","instructions":"紧急程度？","levels":["低","中","高"]}},"expected":{"refund":true,"team":"billing","priority":2},"rationale":"gold-rationale"}`

func TestJinaPresetAndUnsupportedControls(t *testing.T) {
	o, err := parseOptions([]string{"--preset", "jina"}, env(map[string]string{"EVALUATE_REASONING_EFFORT": "high", "EVALUATE_MAX_TOKENS": "1024"}), &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if o.request.Provider != "jina" || o.request.Model != "jina-embeddings-v5-text-small" || o.request.EmulationOptions != nil || o.request.EmulationMode != evaluate.EmulationOff {
		t.Fatal(o.request)
	}
	cfg, err := clientConfig(o, env(map[string]string{"JINA_API_KEY": "key", "JINA_API_BASE": "https://api.example"}))
	if err != nil || cfg.JinaAPIKey != "key" || cfg.JinaAPIBase != "https://api.example" {
		t.Fatal(cfg.JinaAPIBase, err)
	}
	if _, err := clientConfig(o, env(nil)); err == nil || !strings.Contains(err.Error(), "JINA_API_KEY") {
		t.Fatal(err)
	}
	for _, flags := range [][]string{{"--max-tokens", "100"}, {"--reasoning-effort", "none"}, {"--mode", "force"}, {"--mode", "fallback"}, {"--inference-provider", "other"}} {
		if _, err := parseOptions(append([]string{"--preset", "jina"}, flags...), env(nil), &bytes.Buffer{}); err == nil {
			t.Fatalf("accepted unsupported controls %v", flags)
		}
	}
}

func TestJinaCLIMappingAndReport(t *testing.T) {
	dir := t.TempDir()
	casePath, output := filepath.Join(dir, "cases.jsonl"), filepath.Join(dir, "report.json")
	if err := os.WriteFile(casePath, []byte(jinaCaseJSON), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/classify" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("wrong endpoint or credentials")
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body) != 3 {
			t.Errorf("unexpected request fields: %v", body)
		}
		var input, labels []string
		var model string
		json.Unmarshal(body["input"], &input)
		json.Unmarshal(body["labels"], &labels)
		json.Unmarshal(body["model"], &model)
		if model != "jina-embeddings-v5-text-small" || len(input) != 1 || !strings.Contains(input[0], "请把重复扣的钱退回来。") || strings.Contains(input[0], "gold-") || strings.Contains(input[0], "expected") {
			t.Errorf("invalid model input: %s", body["input"])
		}
		prediction := ""
		switch strings.Join(labels, ",") {
		case "低,中,高":
			prediction = "高"
			if !strings.Contains(input[0], "紧急程度？") {
				t.Error("missing instructions")
			}
		case "未申请退款,申请退款":
			prediction = "申请退款"
			if !strings.Contains(input[0], "是否申请退款？") {
				t.Error("missing instructions")
			}
		case "账单问题,物流问题":
			prediction = "账单问题"
			if !strings.Contains(input[0], "负责团队？") {
				t.Error("missing instructions")
			}
		default:
			t.Errorf("labels must contain semantic descriptions: %v", labels)
		}
		fmt.Fprintf(w, `{"data":[{"index":0,"prediction":%q,"score":0.73}],"usage":{"total_tokens":40}}`, prediction)
	}))
	defer server.Close()
	getenv := env(map[string]string{"JINA_API_KEY": "secret", "JINA_API_BASE": server.URL})
	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"--preset", "jina", "--cases", casePath, "--dry-run"}, env(nil), &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "requests=3") || !strings.Contains(stdout.String(), "api_pattern=jina_classify") || calls != 0 {
		t.Fatal(stdout.String(), calls)
	}
	if err := run(context.Background(), []string{"--preset", "jina", "--cases", casePath, "--output", output}, getenv, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		APIPattern            string          `json:"api_pattern"`
		Summary               summary         `json:"summary"`
		ClassificationSummary summary         `json:"classification_summary"`
		Attempts              []attemptResult `json:"attempts"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if calls != 3 || report.APIPattern != "jina_classify" || report.Summary.MatchedJudgments != 3 || report.ClassificationSummary.Attempts != 1 {
		t.Fatalf("calls=%d report=%+v", calls, report)
	}
	out := report.Attempts[0].Result
	if out.Answers["refund"].ProbabilityTrue != nil || out.Answers["team"].Probabilities != nil || *out.Answers["priority"].ScoreValue != 2 || out.ProviderMetadata["jina_classify"] == nil || *out.Usage.TotalTokens != 120 || out.Usage.InputTokens != nil || out.Usage.OutputTokens != nil {
		t.Fatalf("incorrect score/usage semantics: %+v", out)
	}
}

func TestJinaRejectsInvalidLabelsBeforeNetwork(t *testing.T) {
	for _, replacement := range []string{`"true_description":null`, `"true_description":"未申请退款"`} {
		data := strings.Replace(jinaCaseJSON, `"true_description":"申请退款"`, replacement, 1)
		casePath := filepath.Join(t.TempDir(), "cases.jsonl")
		if err := os.WriteFile(casePath, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		err := run(context.Background(), []string{"--preset", "jina", "--cases", casePath, "--dry-run"}, env(nil), &out, &out)
		if err == nil || !strings.Contains(err.Error(), "label") {
			t.Fatalf("invalid labels accepted: %v", err)
		}
	}
}

func TestJinaRejectsInvalidResponses(t *testing.T) {
	for _, response := range []string{`{"data":[]}`, `{"data":[{"index":1,"prediction":"高"}]}`, `{"data":[{"index":0,"prediction":"unknown"}]}`, `{"data":[{"index":0,"prediction":"高"}],"usage":{"total_tokens":-1}}`, `{"data":[{"index":0,"prediction":"高"},{"index":0,"prediction":"高"}]}`} {
		t.Run(response, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, response) }))
			defer server.Close()
			casePath := filepath.Join(t.TempDir(), "cases.jsonl")
			if err := os.WriteFile(casePath, []byte(jinaCaseJSON), 0600); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			err := run(context.Background(), []string{"--preset", "jina", "--cases", casePath}, env(map[string]string{"JINA_API_KEY": "key", "JINA_API_BASE": server.URL}), &out, &out)
			if err == nil || !strings.Contains(out.String(), "ERROR") {
				t.Fatalf("accepted response: %v %s", err, out.String())
			}
		})
	}
}

func TestJinaBuiltinDatasetDryRun(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"--preset", "jina", "--dry-run"}, env(nil), &out, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "requests=614") {
		t.Fatal(out.String())
	}
}

func TestClassificationSummaryExcludesRuleCases(t *testing.T) {
	one := testCases(t)[0]
	one.Category = "content_topic"
	two := one
	two.ID, two.Category = "rule-case", "deadline_rules"
	base := evaluate.Request{Provider: "openai", Model: "test"}
	report, err := runBenchmark(context.Background(), []benchmarkCase{one, two}, base, runOptions{Repeat: 1, Timeout: time.Second, BooleanThreshold: 0.5, ScoreTolerance: 0.5}, func(context.Context, evaluate.Request) (*evaluate.Result, error) { return testResult(), nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(report)
	var decoded struct {
		Summary               summary `json:"summary"`
		ClassificationSummary summary `json:"classification_summary"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Summary.Attempts != 2 || decoded.ClassificationSummary.Attempts != 1 || decoded.ClassificationSummary.MatchedJudgments != 3 {
		t.Fatal(string(data))
	}
}
