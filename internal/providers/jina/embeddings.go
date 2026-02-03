package jina

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/lyricat/goutils/structs"
)

type EmbeddingInput struct {
	Text  string
	Image string
}

type jinaCreateEmbeddingsInput struct {
	Model         string `json:"model"`
	EmbeddingType string `json:"embedding_type,omitempty"`
	Dimensions    int    `json:"dimensions,omitempty"`
	Task          string `json:"task,omitempty"`

	Input        []string `json:"input"`
	Truncate     bool     `json:"truncate,omitempty"`
	LateChunking bool     `json:"late_chunking,omitempty"`
}

type jinaCreateEmbeddingsClipInput struct {
	Model         string `json:"model"`
	EmbeddingType string `json:"embedding_type,omitempty"`
	Dimensions    int    `json:"dimensions,omitempty"`
	Task          string `json:"task,omitempty"`

	Input      []EmbeddingInput `json:"input"`
	Normalized bool             `json:"normalized,omitempty"`
}

func CreateEmbeddings(ctx context.Context, token, base, model string, inputs []EmbeddingInput, options structs.JSONMap) ([]byte, error) {
	var (
		data []byte
		err  error
	)

	if model == "jina-clip-v2" {
		clipInput := &jinaCreateEmbeddingsClipInput{}
		loadClipEmbeddingInput(clipInput, model, inputs, options)
		data, err = json.Marshal(clipInput)
	} else {
		textInput := &jinaCreateEmbeddingsInput{}
		loadTextEmbeddingInput(textInput, model, inputs, options)
		data, err = json.Marshal(textInput)
	}
	if err != nil {
		return nil, err
	}

	if base == "" {
		base = APIBase
	}
	url := fmt.Sprintf("%s/v1/embeddings", base)
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

func loadTextEmbeddingInput(dst *jinaCreateEmbeddingsInput, model string, inputs []EmbeddingInput, options structs.JSONMap) {
	dst.Model = model
	for _, item := range inputs {
		dst.Input = append(dst.Input, item.Text)
	}
	dst.Task = options.GetString("task")
	if dst.Task == "" {
		dst.Task = "text-matching"
	}
	dst.EmbeddingType = "base64"
	dst.Truncate = options.GetBool("truncate")
	dst.LateChunking = options.GetBool("late_chunking")
	dst.Dimensions = int(options.GetInt64("dimensions"))
	if dst.Dimensions == 0 {
		dst.Dimensions = 1024
	}
}

func loadClipEmbeddingInput(dst *jinaCreateEmbeddingsClipInput, model string, inputs []EmbeddingInput, options structs.JSONMap) {
	dst.Model = model
	dst.Input = append([]EmbeddingInput{}, inputs...)
	dst.Task = options.GetString("task")
	if dst.Task == "" {
		dst.Task = "text-matching"
	}
	dst.EmbeddingType = "base64"
	dst.Dimensions = int(options.GetInt64("dimensions"))
	if dst.Dimensions == 0 {
		dst.Dimensions = 1024
	}
	dst.Normalized = options.GetBool("normalized")
}
