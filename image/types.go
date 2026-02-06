package image

import "github.com/lyricat/goutils/structs"

type Options struct {
	OpenAI     structs.JSONMap `json:"openai_options,omitempty"`
	Gemini     structs.JSONMap `json:"gemini_options,omitempty"`
	Cloudflare structs.JSONMap `json:"cloudflare_options,omitempty"`
}

type Request struct {
	Provider string  `json:"provider,omitempty"`
	Model    string  `json:"model,omitempty"`
	Prompt   string  `json:"prompt,omitempty"`
	Count    int     `json:"count,omitempty"`
	Options  Options `json:"options,omitempty"`
}

type Result struct {
	Created int `json:"created"`
	Data    []struct {
		B64JSON string `json:"b64_json"`
	} `json:"data"`
	MimeType string           `json:"mime_type"`
	Usage    CreateImageUsage `json:"usage"`
}

type CreateImageUsage struct {
	Size    string `json:"size"`
	Quality string `json:"quality"`

	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
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

func Image(model, prompt string) Option {
	return func(r *Request) {
		r.Model = model
		r.Prompt = prompt
	}
}

func WithProvider(provider string) Option {
	return func(r *Request) { r.Provider = provider }
}

func WithCount(count int) Option {
	return func(r *Request) { r.Count = count }
}

func WithOptions(opts Options) Option {
	return func(r *Request) { r.Options = opts }
}
