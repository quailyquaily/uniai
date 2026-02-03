package classify

type Input struct {
	Text  string `json:"text,omitempty"`
	Image string `json:"image,omitempty"`
}

type Request struct {
	Provider string   `json:"provider,omitempty"`
	Model    string   `json:"model,omitempty"`
	Input    []Input  `json:"input,omitempty"`
	Labels   []string `json:"labels,omitempty"`
}

type Result struct {
	Data []struct {
		Object      string  `json:"object"`
		Index       int     `json:"index"`
		Prediction  string  `json:"prediction"`
		Score       float64 `json:"score"`
		Predictions []struct {
			Label string  `json:"label"`
			Score float64 `json:"score"`
		} `json:"predictions"`
	} `json:"data"`
	Usage struct {
		TotalTokens int `json:"total_tokens"`
	} `json:"usage"`
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

func Classify(model string, labels []string, inputs ...Input) Option {
	return func(r *Request) {
		r.Model = model
		r.Labels = append([]string{}, labels...)
		r.Input = append(r.Input, inputs...)
	}
}

func WithProvider(provider string) Option {
	return func(r *Request) { r.Provider = provider }
}
