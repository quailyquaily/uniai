package jina

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/quailyquaily/uniai/internal/httputil"
)

type RerankInput struct {
	Text  string
	Image string
}

type jinaRerankInputText struct {
	Model           string   `json:"model"`
	Query           string   `json:"query"`
	Documents       []string `json:"documents"`
	TopN            int      `json:"top_n"`
	ReturnDocuments bool     `json:"return_documents"`
}

type jinaRerankInput struct {
	Model           string        `json:"model"`
	Query           string        `json:"query"`
	Documents       []RerankInput `json:"documents"`
	TopN            int           `json:"top_n"`
	ReturnDocuments bool          `json:"return_documents"`
}

func Rerank(ctx context.Context, token, base, model, query string, docs []RerankInput, topN int, returnDocs bool) ([]byte, error) {
	var (
		data []byte
		err  error
	)
	if model == "jina-reranker-v2-base-multilingual" {
		textInput := &jinaRerankInputText{
			Model:           model,
			Query:           query,
			TopN:            topN,
			ReturnDocuments: returnDocs,
		}
		for _, item := range docs {
			textInput.Documents = append(textInput.Documents, item.Text)
		}
		data, err = json.Marshal(textInput)
	} else {
		newInput := &jinaRerankInput{
			Model:           model,
			Query:           query,
			Documents:       append([]RerankInput{}, docs...),
			TopN:            topN,
			ReturnDocuments: returnDocs,
		}
		data, err = json.Marshal(newInput)
	}
	if err != nil {
		return nil, err
	}

	if base == "" {
		base = APIBase
	}
	url := fmt.Sprintf("%s/v1/rerank", base)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(data))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))

	resp, err := httputil.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respData, err := httputil.ReadBody(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jina API request failed with status %d: %s", resp.StatusCode, string(respData))
	}

	return respData, nil
}
