package main

import (
	"bufio"
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/quailyquaily/uniai/evaluate"
)

//go:embed cases/general.jsonl
var builtinCases string

type benchmarkCase struct {
	ID        string                       `json:"id"`
	Category  string                       `json:"category"`
	Language  string                       `json:"language"`
	State     json.RawMessage              `json:"state"`
	Questions map[string]evaluate.Question `json:"questions"`
	Expected  map[string]json.RawMessage   `json:"expected"`
	Rationale string                       `json:"rationale,omitempty"`
}

func loadCases(r io.Reader) ([]benchmarkCase, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), 4*1024*1024)
	var cases []benchmarkCase
	seen := map[string]bool{}
	line := 0
	for scanner.Scan() {
		line++
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		var c benchmarkCase
		decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&c); err != nil {
			return nil, fmt.Errorf("cases line %d: %w", line, err)
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return nil, fmt.Errorf("cases line %d: expected one JSON object", line)
		}
		if strings.TrimSpace(c.ID) == "" || strings.TrimSpace(c.Category) == "" || strings.TrimSpace(c.Language) == "" {
			return nil, fmt.Errorf("cases line %d: id, category and language are required", line)
		}
		if seen[c.ID] {
			return nil, fmt.Errorf("cases line %d: duplicate id %q", line, c.ID)
		}
		r := evaluate.Request{Provider: "validation", Model: "validation", State: c.State, Questions: c.Questions}
		if err := evaluate.ValidateRequest(&r); err != nil {
			return nil, fmt.Errorf("case %q: %w", c.ID, err)
		}
		if len(c.Expected) != len(c.Questions) {
			return nil, fmt.Errorf("case %q: expected must cover every question exactly once", c.ID)
		}
		gold := &evaluate.Result{Answers: make(map[string]evaluate.Answer)}
		for id, q := range c.Questions {
			value, ok := c.Expected[id]
			if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return nil, fmt.Errorf("case %q: missing expected answer %q", c.ID, id)
			}
			a := evaluate.Answer{Kind: q.Kind}
			var err error
			switch q.Kind {
			case evaluate.Boolean:
				var v bool
				err = json.Unmarshal(value, &v)
				a.BooleanValue = &v
			case evaluate.Choice:
				err = json.Unmarshal(value, &a.Selected)
			case evaluate.Score:
				var v int
				err = json.Unmarshal(value, &v)
				score := float64(v)
				a.ScoreValue = &score
			}
			if err != nil {
				return nil, fmt.Errorf("case %q expected[%q]: %w", c.ID, id, err)
			}
			gold.Answers[id] = a
		}
		if err := evaluate.ValidateResult(&r, gold); err != nil {
			return nil, fmt.Errorf("case %q: invalid expected answers: %w", c.ID, err)
		}
		seen[c.ID] = true
		cases = append(cases, c)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read cases: %w", err)
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("dataset is empty")
	}
	return cases, nil
}
