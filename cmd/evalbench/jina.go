package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/quailyquaily/uniai"
	"github.com/quailyquaily/uniai/classify"
	"github.com/quailyquaily/uniai/evaluate"
)

// This adapter belongs to the benchmark: Classify cannot implement the full
// instruction-following contract of the SDK's Evaluate method.
type jinaQuestion struct {
	id      string
	input   string
	labels  []string
	answers []evaluate.Answer
}

func prepareJinaQuestions(req evaluate.Request) ([]jinaQuestion, error) {
	if err := evaluate.ValidateRequest(&req); err != nil {
		return nil, err
	}
	state, err := jinaText(req.State)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(req.Questions))
	for id := range req.Questions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	questions := make([]jinaQuestion, 0, len(ids))
	for _, id := range ids {
		q := req.Questions[id]
		instructions, err := jinaText(q.Instructions)
		if err != nil {
			return nil, err
		}
		p := jinaQuestion{id: id, input: "判断要求：" + instructions + "\n待分类内容：" + state}
		var descriptions []any
		switch q.Kind {
		case evaluate.Boolean:
			descriptions = []any{q.FalseDescription, q.TrueDescription}
			for _, value := range []bool{false, true} {
				p.answers = append(p.answers, evaluate.Answer{Kind: q.Kind, BooleanValue: &value})
			}
		case evaluate.Choice:
			keys := make([]string, 0, len(q.Options))
			for key := range q.Options {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				descriptions = append(descriptions, q.Options[key])
				p.answers = append(p.answers, evaluate.Answer{Kind: q.Kind, Selected: key})
			}
		case evaluate.Score:
			descriptions = q.Levels
			for i := range q.Levels {
				value := float64(i)
				p.answers = append(p.answers, evaluate.Answer{Kind: q.Kind, ScoreValue: &value})
			}
		}
		if len(descriptions) < 2 || len(descriptions) > 256 {
			return nil, fmt.Errorf("question %q: jina requires 2–256 labels", id)
		}
		seen := map[string]bool{}
		for _, description := range descriptions {
			label, err := jinaText(description)
			if err != nil || strings.TrimSpace(label) == "" {
				return nil, fmt.Errorf("question %q: jina requires nonempty semantic label descriptions", id)
			}
			if seen[label] {
				return nil, fmt.Errorf("question %q: duplicate jina label %q", id, label)
			}
			seen[label] = true
			p.labels = append(p.labels, label)
		}
		questions = append(questions, p)
	}
	return questions, nil
}

// Decode JSON strings as plain text; serialize structured state/descriptions
// deterministically. Null is not a usable classification label.
func jinaText(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	if string(data) == "null" {
		return "", fmt.Errorf("null content")
	}
	var text string
	if json.Unmarshal(data, &text) == nil {
		return text, nil
	}
	return string(data), nil
}

func classifyWithJina(ctx context.Context, client *uniai.Client, req evaluate.Request) (*evaluate.Result, error) {
	questions, err := prepareJinaQuestions(req)
	if err != nil {
		return nil, err
	}
	out := &evaluate.Result{Provider: "jina", Model: req.Model, Emulated: true, Answers: map[string]evaluate.Answer{}}
	responses := make(map[string]*classify.Result, len(questions))
	totalTokens := 0
	for _, q := range questions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		response, err := client.Classify(ctx, classify.WithProvider("jina"), classify.Classify(req.Model, q.labels, classify.Input{Text: q.input}))
		if err != nil {
			return nil, fmt.Errorf("question %q: %w", q.id, err)
		}
		if response == nil || len(response.Data) != 1 || response.Data[0].Index != 0 {
			return nil, fmt.Errorf("question %q: jina must return exactly one result with index 0", q.id)
		}
		found := false
		for i, label := range q.labels {
			if response.Data[0].Prediction == label {
				out.Answers[q.id] = q.answers[i]
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("question %q: unknown jina prediction %q", q.id, response.Data[0].Prediction)
		}
		if response.Usage.TotalTokens < 0 || response.Usage.TotalTokens > int(^uint(0)>>1)-totalTokens {
			return nil, fmt.Errorf("question %q: invalid jina token usage", q.id)
		}
		totalTokens += response.Usage.TotalTokens
		responses[q.id] = response
	}
	metadata, err := json.Marshal(responses)
	if err != nil {
		return nil, err
	}
	out.Usage = &evaluate.Usage{TotalTokens: &totalTokens}
	// Scores remain provider metadata; they are neither ordinal answers nor
	// calibrated probabilities. No Brier score is computed from them.
	out.ProviderMetadata = map[string]json.RawMessage{"jina_classify": metadata}
	return out, nil
}
