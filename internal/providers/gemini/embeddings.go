package gemini

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/lyricat/goutils/structs"
	"github.com/quailyquaily/uniai/internal/httputil"
)

const (
	geminiAPIBase                = "https://generativelanguage.googleapis.com"
	defaultGeminiEmbeddingModel  = "gemini-embedding-001"
)

type embeddingData struct {
	Object    string `json:"object"`
	Embedding string `json:"embedding"`
	Index     int    `json:"index"`
}

type createEmbeddingsOutput struct {
	Model  string          `json:"model"`
	Object string          `json:"object"`
	Data   []embeddingData `json:"data"`
	Usage  struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
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

type geminiBatchRequest struct {
	Requests []geminiBatchEmbedRequest `json:"requests"`
}

type geminiBatchEmbedRequest struct {
	Model   string        `json:"model"`
	Content geminiContent `json:"content"`
	geminiEmbeddingConfig
}

type geminiBatchResponse struct {
	Embeddings []geminiEmbedding `json:"embeddings"`
}

type geminiEmbedding struct {
	Values []float64 `json:"values"`
}

func CreateEmbeddings(ctx context.Context, token, base, model string, inputs []string, options structs.JSONMap) ([]byte, error) {
	if model == "" {
		model = defaultGeminiEmbeddingModel
	}
	fullModel := model
	if !strings.HasPrefix(model, "models/") {
		fullModel = "models/" + model
	}

	taskType := options.GetString("task_type")
	dimensions := int(options.GetInt64("output_dimensionality"))

	requests := make([]geminiBatchEmbedRequest, 0, len(inputs))
	for _, text := range inputs {
		req := geminiBatchEmbedRequest{
			Model:   fullModel,
			Content: geminiContent{Parts: []geminiPart{{Text: text}}},
		}
		if taskType != "" {
			req.TaskType = taskType
		}
		if dimensions > 0 {
			req.OutputDimensionality = dimensions
		}
		requests = append(requests, req)
	}

	payload := geminiBatchRequest{Requests: requests}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	base = normalizeGeminiBase(base)
	url := fmt.Sprintf("%s/v1beta/%s:batchEmbedContents", base, fullModel)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(data))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", token)

	resp, err := httputil.DefaultClient.Do(httpReq)
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

	var batchResp geminiBatchResponse
	if err := json.Unmarshal(respData, &batchResp); err != nil {
		return nil, err
	}

	output := &createEmbeddingsOutput{
		Model:  fullModel,
		Object: "list",
		Data:   make([]embeddingData, 0, len(batchResp.Embeddings)),
	}
	for i, emb := range batchResp.Embeddings {
		output.Data = append(output.Data, embeddingData{
			Object:    "embedding",
			Embedding: encodeFloat64sToBase64(emb.Values),
			Index:     i,
		})
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

func encodeFloat64sToBase64(vals []float64) string {
	buf := make([]byte, len(vals)*4)
	for i, v := range vals {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(float32(v)))
	}
	return base64.StdEncoding.EncodeToString(buf)
}
