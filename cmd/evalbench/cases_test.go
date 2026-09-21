package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/quailyquaily/uniai/evaluate"
)

func TestBuiltinDatasetCoverage(t *testing.T) {
	cases, err := loadCases(strings.NewReader(builtinCases))
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 614 {
		t.Fatalf("got %d cases, want 614", len(cases))
	}
	categories := map[string]int{}
	domains := map[string]int{}
	languages := map[string]int{}
	kinds := map[evaluate.Kind]int{}
	booleans := map[string]int{}
	seen := map[string]string{}
	for _, c := range cases {
		categories[c.Category]++
		domains[c.Domain]++
		languages[c.Language]++
		if c.Rationale == "" {
			t.Fatalf("%s lacks label rationale", c.ID)
		}
		payload, _ := json.Marshal(struct {
			State     json.RawMessage
			Questions map[string]evaluate.Question
		}{c.State, c.Questions})
		if prev := seen[string(payload)]; prev != "" {
			t.Fatalf("duplicate model input: %s and %s", prev, c.ID)
		}
		seen[string(payload)] = c.ID
		for id, q := range c.Questions {
			kinds[q.Kind]++
			if q.Kind == evaluate.Boolean {
				booleans[string(c.Expected[id])]++
			}
		}
	}
	wantCategories := map[string]int{
		"change_risk": 40, "content_topic": 30, "deadline_rules": 40,
		"eligibility_rules": 40, "event_order": 40, "evidence_injection": 30,
		"evidence_strength": 40, "form_completeness": 40, "incident_severity": 40,
		"intent_classification": 30, "language_detection": 20, "moderation_category": 32,
		"negation_scope": 30, "refund_intent": 30, "retrieval_relevance": 32,
		"sentiment_intensity": 30, "support_routing": 30, "workflow_action": 40,
	}
	if len(categories) != len(wantCategories) {
		t.Fatal(categories)
	}
	for cat, want := range wantCategories {
		if categories[cat] != want {
			t.Fatalf("%s: got %d cases, want %d", cat, categories[cat], want)
		}
	}
	wantKinds := map[evaluate.Kind]int{evaluate.Boolean: 210, evaluate.Choice: 182, evaluate.Score: 222}
	for kind, want := range wantKinds {
		if kinds[kind] != want {
			t.Fatalf("kind %s: got %d, want %d", kind, kinds[kind], want)
		}
	}
	if booleans["true"] != 110 || booleans["false"] != 100 {
		t.Fatal(booleans)
	}
	if languages["zh"] != 258 || languages["ja"] != 258 || languages["en"] != 90 || languages["es"] != 4 || languages["fr"] != 4 {
		t.Fatal(languages)
	}
	if domains["general"] != 566 || domains["crypto"] != 16 || domains["finance"] != 16 || domains["geopolitics"] != 16 {
		t.Fatal(domains)
	}
}

func TestBuiltinJapaneseCasesMatchChineseCases(t *testing.T) {
	cases, err := loadCases(strings.NewReader(builtinCases))
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]benchmarkCase, len(cases))
	for _, c := range cases {
		byID[c.ID] = c
	}
	for _, chinese := range cases {
		if chinese.Language != "zh" || chinese.Category == "language_detection" {
			continue
		}
		japanese, ok := byID[chinese.ID+"-ja"]
		if !ok {
			t.Fatalf("%s lacks Japanese counterpart", chinese.ID)
		}
		chineseExpected, _ := json.Marshal(chinese.Expected)
		japaneseExpected, _ := json.Marshal(japanese.Expected)
		if japanese.Language != "ja" || japanese.Category != chinese.Category || japanese.Domain != chinese.Domain || string(japaneseExpected) != string(chineseExpected) {
			t.Fatalf("mismatched pair: %s and %s", chinese.ID, japanese.ID)
		}
	}
}

func TestBuiltinSmallSelectionCoversScenesAndBothBooleanLabels(t *testing.T) {
	cases, err := loadCases(strings.NewReader(builtinCases))
	if err != nil {
		t.Fatal(err)
	}
	first, err := selectCases(cases, "", "", "", 18, 0)
	if err != nil {
		t.Fatal(err)
	}
	categories := map[string]bool{}
	labels := map[string]bool{}
	for _, c := range first {
		categories[c.Category] = true
		for id, q := range c.Questions {
			if q.Kind == evaluate.Boolean {
				labels[string(c.Expected[id])] = true
			}
		}
	}
	if len(categories) != 18 || len(labels) != 2 {
		t.Fatalf("smoke subset: categories=%d boolean labels=%v", len(categories), labels)
	}
}
