package audio

import (
	"io"

	"github.com/lyricat/goutils/structs"
)

type Options struct {
	OpenAI     structs.JSONMap `json:"openai_options,omitempty"`
	Cloudflare structs.JSONMap `json:"cloudflare_options,omitempty"`
}

type Request struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	// Audio is the base64-encoded input. Data takes precedence when both are set.
	Audio string `json:"audio,omitempty"`
	// Data is the raw audio input.
	Data []byte `json:"-"`
	// Reader streams the audio input when Data is empty. OpenAI uploads it
	// without buffering; Cloudflare reads it fully to base64-encode it.
	Reader io.Reader `json:"-"`
	// Filename and MIMEType describe the input format (e.g. "voice.ogg",
	// "audio/ogg"). OpenAI needs one of them, or sniffable content.
	Filename string  `json:"filename,omitempty"`
	MIMEType string  `json:"mime_type,omitempty"`
	Options  Options `json:"options,omitempty"`
}

type Segment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text,omitempty"`
}

// Usage is reported by OpenAI transcription models. Type is "tokens"
// (gpt-4o-transcribe family) or "duration" (whisper-1, billed by Seconds).
type Usage struct {
	Type             string  `json:"type,omitempty"`
	InputTokens      int     `json:"input_tokens,omitempty"`
	InputTextTokens  int     `json:"input_text_tokens,omitempty"`
	InputAudioTokens int     `json:"input_audio_tokens,omitempty"`
	OutputTokens     int     `json:"output_tokens,omitempty"`
	TotalTokens      int     `json:"total_tokens,omitempty"`
	Seconds          float64 `json:"seconds,omitempty"`
}

type Result struct {
	Text     string    `json:"text,omitempty"`
	Language string    `json:"language,omitempty"`
	Duration float64   `json:"duration,omitempty"`
	Segments []Segment `json:"segments,omitempty"`
	Usage    *Usage    `json:"usage,omitempty"`
	Raw      any       `json:"raw,omitempty"`
}

type Option func(*Request)

func BuildRequest(opts ...Option) *Request {
	req := &Request{}
	for _, opt := range opts {
		if opt != nil {
			opt(req)
		}
	}
	return req
}

func Audio(model, audioBase64 string) Option {
	return func(r *Request) {
		r.Model = model
		r.Audio = audioBase64
	}
}

func WithModel(model string) Option {
	return func(r *Request) { r.Model = model }
}

func WithProvider(provider string) Option {
	return func(r *Request) { r.Provider = provider }
}

func WithAudio(audioBase64 string) Option {
	return func(r *Request) { r.Audio = audioBase64 }
}

// WithAudioFile sets raw audio bytes with an optional filename and MIME type,
// avoiding a base64 round trip.
func WithAudioFile(data []byte, filename, mimeType string) Option {
	return func(r *Request) {
		r.Data = data
		r.Filename = filename
		r.MIMEType = mimeType
	}
}

// WithAudioReader streams audio from r (e.g. an HTTP response body) with an
// optional filename and MIME type. The caller owns r and closes it.
func WithAudioReader(reader io.Reader, filename, mimeType string) Option {
	return func(r *Request) {
		r.Reader = reader
		r.Filename = filename
		r.MIMEType = mimeType
	}
}

func WithOptions(opts Options) Option {
	return func(r *Request) { r.Options = opts }
}
