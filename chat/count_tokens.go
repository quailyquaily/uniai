package chat

import "errors"

// ErrTokenCountUnsupported means uniai or the upstream service cannot count this
// provider, model, or request mode. Authentication and network errors are distinct.
var ErrTokenCountUnsupported = errors.New("token counting is not supported")

// TokenCount is an upstream input-token estimate for one request. It is separate
// from Usage: counting does not generate a response or record inference costs.
// Counts may differ from billed usage and do not predict cache hits or writes.
type TokenCount struct {
	InputTokens int    `json:"input_tokens"`
	Model       string `json:"model"`
}
