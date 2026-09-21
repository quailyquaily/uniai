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
	if len(cases) != 360 {
		t.Fatalf("got %d cases, want 360", len(cases))
	}
	categories := map[string]int{}
	kinds := map[evaluate.Kind]int{}
	booleans := map[string]int{}
	seen := map[string]string{}
	for _, c := range cases {
		categories[c.Category]++
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
	if len(categories) != 18 {
		t.Fatal(categories)
	}
	for cat, count := range categories {
		if count != 20 {
			t.Fatalf("%s: %d cases", cat, count)
		}
	}
	for _, kind := range []evaluate.Kind{evaluate.Boolean, evaluate.Choice, evaluate.Score} {
		if kinds[kind] != 120 {
			t.Fatal(kinds)
		}
	}
	if booleans["true"] != 60 || booleans["false"] != 60 {
		t.Fatal(booleans)
	}
}

func TestBuiltinSmallSelectionCoversScenesAndBothBooleanLabels(t *testing.T) {
	cases, err := loadCases(strings.NewReader(builtinCases))
	if err != nil {
		t.Fatal(err)
	}
	first, err := selectCases(cases, "", 18, 0)
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
