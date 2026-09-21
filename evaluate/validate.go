package evaluate

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ValidateRequest checks provider-independent constraints after defaults have
// been resolved. It does not mutate the request or impose provider limits.
func ValidateRequest(r *Request) error {
	if r == nil {
		return fmt.Errorf("%w: request is nil", ErrInvalidRequest)
	}
	if strings.TrimSpace(r.Provider) == "" {
		return fmt.Errorf("%w: provider is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(r.Model) == "" {
		return fmt.Errorf("%w: model is required", ErrInvalidRequest)
	}
	switch r.EmulationMode {
	case "", EmulationOff, EmulationFallback, EmulationForce:
	default:
		return fmt.Errorf("%w: unknown emulation_mode %q", ErrInvalidRequest, r.EmulationMode)
	}
	if r.EmulationOptions != nil && r.EmulationOptions.MaxTokens != nil && *r.EmulationOptions.MaxTokens <= 0 {
		return fmt.Errorf("%w: emulation_options.max_tokens must be positive", ErrInvalidRequest)
	}
	if err := validateContent("state", r.State, false, false); err != nil {
		return err
	}
	if len(r.Questions) == 0 {
		return fmt.Errorf("%w: questions must not be empty", ErrInvalidRequest)
	}
	for id, q := range r.Questions {
		path := fmt.Sprintf("questions[%q]", id)
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("%w: %s has an empty ID", ErrInvalidRequest, path)
		}
		if err := validateContent(path+".instructions", q.Instructions, false, true); err != nil {
			return err
		}
		switch q.Kind {
		case Boolean:
			if q.Options != nil || q.Levels != nil {
				return fmt.Errorf("%w: %s: boolean cannot have options or levels", ErrInvalidRequest, path)
			}
			if err := validateContent(path+".true_description", q.TrueDescription, true, false); err != nil {
				return err
			}
			if err := validateContent(path+".false_description", q.FalseDescription, true, false); err != nil {
				return err
			}
		case Choice:
			if len(q.Options) == 0 || q.Levels != nil || q.TrueDescription != nil || q.FalseDescription != nil {
				return fmt.Errorf("%w: %s: choice requires options and no other kind's fields", ErrInvalidRequest, path)
			}
			for option, description := range q.Options {
				if strings.TrimSpace(option) == "" {
					return fmt.Errorf("%w: %s.options: empty option ID", ErrInvalidRequest, path)
				}
				if err := validateContent(fmt.Sprintf("%s.options[%q]", path, option), description, true, false); err != nil {
					return err
				}
			}
		case Score:
			if len(q.Levels) < 2 || q.Options != nil || q.TrueDescription != nil || q.FalseDescription != nil {
				return fmt.Errorf("%w: %s: score requires at least two levels and no other kind's fields", ErrInvalidRequest, path)
			}
			for i, level := range q.Levels {
				if err := validateContent(fmt.Sprintf("%s.levels[%d]", path, i), level, false, false); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("%w: %s.kind: unknown kind %q", ErrInvalidRequest, path, q.Kind)
		}
	}
	return nil
}

func validateContent(path string, value any, nullable, nonblank bool) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("%w: %s is not JSON encodable: %w", ErrInvalidRequest, path, err)
	}
	var decoded any
	if err = json.Unmarshal(data, &decoded); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrInvalidRequest, path, err)
	}
	switch v := decoded.(type) {
	case nil:
		if nullable {
			return nil
		}
	case string:
		if !nonblank || strings.TrimSpace(v) != "" {
			return nil
		}
		return fmt.Errorf("%w: %s must not be blank", ErrInvalidRequest, path)
	case map[string]any, []any:
		return nil
	}
	return fmt.Errorf("%w: %s must be a string, object or array", ErrInvalidRequest, path)
}

// ValidateResult checks answer completeness and common value semantics. Native
// adapters additionally validate their required fields and rounding precision.
func ValidateResult(r *Request, out *Result) error {
	if err := ValidateRequest(r); err != nil {
		return err
	}
	if out == nil {
		return fmt.Errorf("%w: result is nil", ErrInvalidResponse)
	}
	if len(out.Answers) != len(r.Questions) {
		return fmt.Errorf("%w: answers must cover exactly the requested questions", ErrInvalidResponse)
	}
	for id, q := range r.Questions {
		a, ok := out.Answers[id]
		if !ok || a.Kind != q.Kind {
			return fmt.Errorf("%w: answers[%q]: missing answer or mismatched kind", ErrInvalidResponse, id)
		}
		if a.ProbabilityTrue != nil && !validProbability(*a.ProbabilityTrue) {
			return fmt.Errorf("%w: answers[%q].probability_true is outside [0,1]", ErrInvalidResponse, id)
		}
		switch q.Kind {
		case Boolean:
			if (a.BooleanValue == nil && a.ProbabilityTrue == nil) || a.Selected != "" || a.ScoreValue != nil || a.Probabilities != nil {
				return fmt.Errorf("%w: answers[%q]: invalid boolean fields", ErrInvalidResponse, id)
			}
		case Choice:
			if _, ok := q.Options[a.Selected]; !ok || a.BooleanValue != nil || a.ProbabilityTrue != nil || a.ScoreValue != nil {
				return fmt.Errorf("%w: answers[%q]: invalid choice fields", ErrInvalidResponse, id)
			}
		case Score:
			if a.ScoreValue == nil || math.IsNaN(*a.ScoreValue) || *a.ScoreValue < 0 || *a.ScoreValue > float64(len(q.Levels)-1) || a.BooleanValue != nil || a.ProbabilityTrue != nil || a.Selected != "" {
				return fmt.Errorf("%w: answers[%q]: invalid score fields", ErrInvalidResponse, id)
			}
		}
		if a.Probabilities != nil {
			n := len(q.Options)
			if q.Kind == Score {
				n = len(q.Levels)
			}
			if len(a.Probabilities) != n {
				return fmt.Errorf("%w: answers[%q].probabilities has missing or extra keys", ErrInvalidResponse, id)
			}
			for key, p := range a.Probabilities {
				_, validKey := q.Options[key]
				if q.Kind == Score {
					index, err := strconv.Atoi(key)
					validKey = err == nil && index >= 0 && index < n && strconv.Itoa(index) == key
				}
				if !validKey || !validProbability(p) {
					return fmt.Errorf("%w: answers[%q].probabilities[%q] is invalid", ErrInvalidResponse, id, key)
				}
			}
		}
	}
	if out.Usage != nil {
		for name, value := range map[string]*int{"input_tokens": out.Usage.InputTokens, "output_tokens": out.Usage.OutputTokens, "total_tokens": out.Usage.TotalTokens} {
			if value != nil && *value < 0 {
				return fmt.Errorf("%w: usage.%s must be nonnegative", ErrInvalidResponse, name)
			}
		}
	}
	return nil
}

func validProbability(p float64) bool { return !math.IsNaN(p) && p >= 0 && p <= 1 }
