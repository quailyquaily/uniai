package uniai

import (
	"context"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"

	"github.com/quailyquaily/uniai/evaluate"
)

func TestEvaluatePricingYAMLValidationAndClone(t *testing.T) {
	catalog, err := ParsePricingYAML([]byte(`evaluate:
  - inference_provider: typesafe
    model: jev-1.13.0
    aliases: [jev-latest]
    input_usd_per_million: 0.042
    output_usd_per_million: 0
`))
	if err != nil || len(catalog.Evaluate) != 1 {
		t.Fatalf("catalog=%v err=%v", catalog, err)
	}
	clone := catalog.Clone()
	clone.Evaluate[0].Aliases[0] = "changed"
	clone.Evaluate[0].Model = "changed"
	if catalog.Evaluate[0].Aliases[0] != "jev-latest" || catalog.Evaluate[0].Model != "jev-1.13.0" {
		t.Fatal("clone shares rules or aliases")
	}
	for _, mutate := range []func(*EvaluationPricingRule){
		func(r *EvaluationPricingRule) { r.InferenceProvider = " " },
		func(r *EvaluationPricingRule) { r.Model = " " },
		func(r *EvaluationPricingRule) { r.Aliases = []string{" "} },
		func(r *EvaluationPricingRule) { r.InputUSDPerMillion = -1 },
		func(r *EvaluationPricingRule) { r.OutputUSDPerMillion = -1 },
		func(r *EvaluationPricingRule) { r.InputUSDPerMillion = math.NaN() },
		func(r *EvaluationPricingRule) { r.OutputUSDPerMillion = math.Inf(1) },
	} {
		bad := catalog.Clone()
		mutate(&bad.Evaluate[0])
		if err := bad.Validate(); err == nil {
			t.Fatalf("accepted invalid rule: %#v", bad.Evaluate[0])
		}
	}
	duplicate := catalog.Clone()
	duplicate.Evaluate = append(duplicate.Evaluate, EvaluationPricingRule{InferenceProvider: " TYPESAFE ", Model: "other", Aliases: []string{"JEV-1-13-0"}})
	if err := duplicate.Validate(); err == nil {
		t.Fatal("accepted normalized duplicate alias")
	}
	duplicate.Evaluate[1].InferenceProvider = "self-hosted"
	if err := duplicate.Validate(); err != nil {
		t.Fatal(err)
	}
	if old, err := ParsePricingYAML([]byte("chat: []\nimage: []\n")); err != nil || len(old.Evaluate) != 0 {
		t.Fatalf("old YAML: %v %v", old, err)
	}
}

func TestEstimateEvaluateCostStrictMatching(t *testing.T) {
	catalog := &PricingCatalog{Evaluate: []EvaluationPricingRule{{InferenceProvider: "typesafe", Model: "jev-1.13.0", Aliases: []string{"jev-latest"}, InputUSDPerMillion: 0.042}}}
	usage := evaluate.Usage{InputTokens: evalPtr(1_000_000), OutputTokens: evalPtr(10)}
	for _, model := range []string{"jev-1.13.0", " JEV-1-13-0 ", "jev-latest"} {
		cost, ok := catalog.EstimateEvaluateCost(" TYPESAFE ", model, usage)
		if !ok || cost == nil || cost.Input != 0.042 || cost.Output != 0 || cost.Total != 0.042 || cost.Currency != "USD" || !cost.Estimated {
			t.Fatalf("%s: cost=%+v ok=%v", model, cost, ok)
		}
	}
	for _, target := range [][2]string{{"", "jev-latest"}, {"other", "jev-latest"}, {"typesafe", ""}, {"typesafe", "jev-1.14.0"}, {"typesafe", "other/jev-latest"}} {
		if cost, ok := catalog.EstimateEvaluateCost(target[0], target[1], usage); ok || cost != nil {
			t.Fatalf("unexpected match: %v %+v", target, cost)
		}
	}
	for _, incomplete := range []evaluate.Usage{{}, {InputTokens: evalPtr(1)}, {OutputTokens: evalPtr(0)}, {InputTokens: evalPtr(-1), OutputTokens: evalPtr(0)}} {
		if cost, ok := catalog.EstimateEvaluateCost("typesafe", "jev-latest", incomplete); ok || cost != nil {
			t.Fatalf("incomplete/negative usage priced: %+v", incomplete)
		}
	}
	zero := evaluate.Usage{InputTokens: evalPtr(0), OutputTokens: evalPtr(0)}
	if cost, ok := catalog.EstimateEvaluateCost("typesafe", "jev-latest", zero); !ok || cost == nil || cost.Total != 0 {
		t.Fatalf("zero usage: %+v %v", cost, ok)
	}
	for _, empty := range []*PricingCatalog{nil, {}} {
		if cost, ok := empty.EstimateEvaluateCost("typesafe", "jev-latest", usage); ok || cost != nil {
			t.Fatal("empty catalog priced usage")
		}
	}
	for _, model := range []string{"jev-1.13.0", "jev-latest", "jev-preview"} {
		if cost, ok := DefaultPricingCatalog().EstimateEvaluateCost("typesafe", model, usage); !ok || cost.Total != 0.042 {
			t.Fatalf("default %s: %+v %v", model, cost, ok)
		}
	}
}

func TestEvaluateNativeCost(t *testing.T) {
	for _, tc := range []struct {
		name    string
		pricing *PricingCatalog
		body    string
		want    *float64
	}{
		{"default", nil, evalNativeResponse, evalPtr(0.0000042)},
		{"override", &PricingCatalog{Evaluate: []EvaluationPricingRule{{InferenceProvider: "typesafe", Model: "jev-1.13.0", InputUSDPerMillion: 2, OutputUSDPerMillion: 3}}}, evalNativeResponse, evalPtr(0.000203)},
		{"disabled", &PricingCatalog{}, evalNativeResponse, nil},
		{"chat-isolated", &PricingCatalog{Chat: []ChatPricingRule{{Model: "jev-1.13.0", InputUSDPerMillion: 5}}}, evalNativeResponse, nil},
		{"unknown-version", nil, strings.ReplaceAll(evalNativeResponse, "jev-1.13.0", "jev-unknown"), nil},
		{"missing-output", nil, strings.ReplaceAll(evalNativeResponse, `,"output_tokens":1`, ""), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := New(Config{Pricing: tc.pricing, EvaluateModel: "jev-latest", TypeSafeAPIKey: "key", EvaluateHTTPClient: &http.Client{Transport: evalTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})}})
			out, err := c.Evaluate(context.Background(), evalRequest())
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == nil {
				if out.Usage.Cost != nil {
					t.Fatal(out.Usage.Cost)
				}
			} else if out.Usage.Cost == nil || out.Usage.Cost.Total != *tc.want {
				t.Fatalf("cost=%+v want=%v", out.Usage.Cost, *tc.want)
			}
		})
	}
}
