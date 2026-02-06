package audio

import "github.com/lyricat/goutils/structs"

type Options struct {
	Cloudflare structs.JSONMap `json:"cloudflare_options,omitempty"`
}

type Request struct {
	Provider string  `json:"provider,omitempty"`
	Model    string  `json:"model,omitempty"`
	Audio    string  `json:"audio,omitempty"`
	Options  Options `json:"options,omitempty"`
}

type Segment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text,omitempty"`
}

type Result struct {
	Text     string    `json:"text,omitempty"`
	Language string    `json:"language,omitempty"`
	Segments []Segment `json:"segments,omitempty"`
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

func WithProvider(provider string) Option {
	return func(r *Request) { r.Provider = provider }
}

func WithAudio(audioBase64 string) Option {
	return func(r *Request) { r.Audio = audioBase64 }
}

func WithOptions(opts Options) Option {
	return func(r *Request) { r.Options = opts }
}
