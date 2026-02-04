package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/lyricat/goutils/structs"
	"github.com/quailyquaily/uniai/internal/httputil"
)

const (
	geminiAPIBase = "https://generativelanguage.googleapis.com"
)

type createEmbeddingsOutput struct {
	Model  string `json:"model"`
	Object string `json:"object"`
	Data   []struct {
		Object    string `json:"object"`
		Embedding string `json:"embedding"`
		Index     int    `json:"index"`
	} `json:"data"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

type geminiCreateEmbeddingsInput struct {
	Model   string        `json:"model"`
	Content geminiContent `json:"content"`
	geminiEmbeddingConfig
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiEmbeddingConfig struct {
	TaskType             string `json:"task_type,omitempty"`
	OutputDimensionality int    `json:"output_dimensionality,omitempty"`
}

type geminiCreateEmbeddingsOutput struct {
	Embedding geminiEmbedding `json:"embedding"`
}

type geminiEmbedding struct {
	Values []float64 `json:"values"`
}

func CreateEmbeddings(ctx context.Context, token, base string, inputs []string, options structs.JSONMap) ([]byte, error) {
	payload := &geminiCreateEmbeddingsInput{}
	loadGeminiEmbeddingsInput(payload, inputs, options)

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	base = normalizeGeminiBase(base)
	url := fmt.Sprintf("%s/v1beta/models/gemini-embedding-001:embedContent", base)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(data))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", token)

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
		return nil, fmt.Errorf("gemini API request failed with status %d: %s", resp.StatusCode, string(respData))
	}

	geminiOutput := &geminiCreateEmbeddingsOutput{}
	if err := json.Unmarshal(respData, geminiOutput); err != nil {
		return nil, err
	}

	output := &createEmbeddingsOutput{
		Model:  "models/gemini-embedding-001",
		Object: "list",
		Data: make([]struct {
			Object    string `json:"object"`
			Embedding string `json:"embedding"`
			Index     int    `json:"index"`
		}, len(geminiOutput.Embedding.Values)),
	}

	return json.Marshal(output)
}

func normalizeGeminiBase(base string) string {
	if base == "" {
		return geminiAPIBase
	}
	trimmed := strings.TrimRight(base, "/")
	if idx := strings.Index(trimmed, "/openai"); idx >= 0 {
		trimmed = trimmed[:idx]
		trimmed = strings.TrimRight(trimmed, "/")
	}
	trimmed = strings.TrimSuffix(trimmed, "/v1beta")
	if trimmed == "" {
		return geminiAPIBase
	}
	return trimmed
}

func loadGeminiEmbeddingsInput(dst *geminiCreateEmbeddingsInput, inputs []string, options structs.JSONMap) {
	for _, item := range inputs {
		dst.Content.Parts = append(dst.Content.Parts, geminiPart{Text: item})
	}

	taskType := options.GetString("task_type")
	if taskType != "" {
		dst.TaskType = taskType
	}

	dimensions := int(options.GetInt64("output_dimensionality"))
	if dimensions > 0 {
		dst.OutputDimensionality = dimensions
	}

	dst.Model = "models/gemini-embedding-001"
}
