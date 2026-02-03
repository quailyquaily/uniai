package jina

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type ClassifyInput struct {
	Text  string
	Image string
}

type jinaClassifyInputText struct {
	Model  string   `json:"model"`
	Input  []string `json:"input"`
	Labels []string `json:"labels"`
}

type jinaClassifyInput struct {
	Model  string          `json:"model"`
	Input  []ClassifyInput `json:"input"`
	Labels []string        `json:"labels"`
}

func Classify(ctx context.Context, token, base, model string, labels []string, inputs []ClassifyInput) ([]byte, error) {
	var (
		data []byte
		err  error
	)
	if model == "jina-embeddings-v3" {
		textInput := &jinaClassifyInputText{
			Model:  model,
			Labels: append([]string{}, labels...),
		}
		for _, item := range inputs {
			textInput.Input = append(textInput.Input, item.Text)
		}
		data, err = json.Marshal(textInput)
	} else {
		newInput := &jinaClassifyInput{
			Model:  model,
			Input:  append([]ClassifyInput{}, inputs...),
			Labels: append([]string{}, labels...),
		}
		data, err = json.Marshal(newInput)
	}
	if err != nil {
		return nil, err
	}

	if base == "" {
		base = APIBase
	}
	url := fmt.Sprintf("%s/v1/classify", base)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(data))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jina API request failed with status %d: %s", resp.StatusCode, string(respData))
	}

	return respData, nil
}
