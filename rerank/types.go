package rerank

type Input struct {
	Text  string `json:"text,omitempty"`
	Image string `json:"image,omitempty"`
}

type Request struct {
	Provider        string  `json:"provider,omitempty"`
	Model           string  `json:"model,omitempty"`
	Query           string  `json:"query,omitempty"`
	Documents       []Input `json:"documents,omitempty"`
	TopN            int     `json:"top_n,omitempty"`
	ReturnDocuments bool    `json:"return_documents,omitempty"`
}

type Result struct {
	Model   string `json:"model"`
	Results []struct {
		RelevanceScore float64 `json:"relevance_score"`
		Index          int     `json:"index"`
		Document       struct {
			Text string `json:"text,omitempty"`
			URL  string `json:"url,omitempty"`
		} `json:"document,omitempty"`
	} `json:"results"`
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

func Rerank(model, query string, docs ...Input) Option {
	return func(r *Request) {
		r.Model = model
		r.Query = query
		r.Documents = append(r.Documents, docs...)
	}
}

func WithProvider(provider string) Option {
	return func(r *Request) { r.Provider = provider }
}

func WithTopN(topN int) Option {
	return func(r *Request) { r.TopN = topN }
}

func WithReturnDocuments(returnDocs bool) Option {
	return func(r *Request) { r.ReturnDocuments = returnDocs }
}
