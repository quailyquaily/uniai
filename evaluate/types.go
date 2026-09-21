// Package evaluate defines structured judgments over a shared state.
package evaluate

import (
	"encoding/json"

	"github.com/quailyquaily/uniai/chat"
)

type Kind string

const (
	Boolean Kind = "boolean"
	Choice  Kind = "choice"
	Score   Kind = "score"
)

type EmulationMode string

const (
	EmulationOff      EmulationMode = "off"
	EmulationFallback EmulationMode = "fallback"
	EmulationForce    EmulationMode = "force"
)

// Request defines the state and the complete set of judgments to return.
// Callers must not mutate its maps, slices or pointed-to values during a call.
type Request struct {
	Provider          string              `json:"provider,omitempty"`
	Model             string              `json:"model,omitempty"`
	EmulationMode     EmulationMode       `json:"emulation_mode,omitempty"`
	EmulationOptions  *EmulationOptions   `json:"emulation_options,omitempty"`
	InferenceProvider string              `json:"inference_provider,omitempty"`
	State             any                 `json:"state"`
	Questions         map[string]Question `json:"questions"`
}

// EmulationOptions controls generation through the selected Chat provider.
// Nil fields inherit client defaults; token accounting depends on the provider.
type EmulationOptions struct {
	ReasoningEffort *chat.ReasoningEffort `json:"reasoning_effort,omitempty"`
	MaxTokens       *int                  `json:"max_tokens,omitempty"`
}

type Question struct {
	Kind             Kind           `json:"kind"`
	Instructions     any            `json:"instructions"`
	Options          map[string]any `json:"options,omitempty"`
	Levels           []any          `json:"levels,omitempty"`
	TrueDescription  any            `json:"true_description,omitempty"`
	FalseDescription any            `json:"false_description,omitempty"`
}

type Answer struct {
	Kind            Kind     `json:"kind"`
	BooleanValue    *bool    `json:"boolean_value,omitempty"`
	ProbabilityTrue *float64 `json:"probability_true,omitempty"`
	Selected        string   `json:"selected,omitempty"`
	// ScoreValue uses the zero-based ordinal scale in Question.Levels.
	ScoreValue *float64 `json:"score_value,omitempty"`
	// Probabilities is optional. Score keys are decimal level indices.
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
}

type Result struct {
	Provider string `json:"provider"`
	// Model is the upstream-reported identifier, which may still be an alias.
	Model            string                     `json:"model,omitempty"`
	Emulated         bool                       `json:"emulated"`
	Answers          map[string]Answer          `json:"answers"`
	Usage            *Usage                     `json:"usage,omitempty"`
	ProviderMetadata map[string]json.RawMessage `json:"provider_metadata,omitempty"`
	Raw              json.RawMessage            `json:"-"`
}

type Usage struct {
	InputTokens  *int            `json:"input_tokens,omitempty"`
	OutputTokens *int            `json:"output_tokens,omitempty"`
	TotalTokens  *int            `json:"total_tokens,omitempty"`
	Cost         *chat.UsageCost `json:"cost,omitempty"`
}
