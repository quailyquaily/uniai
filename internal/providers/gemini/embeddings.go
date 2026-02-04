package gemini

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
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

type geminiBatchEmbedRequest struct {
	Requests []geminiCreateEmbeddingsInput `json:"requests"`
}

type geminiBatchEmbedResponse struct {
	Embeddings []geminiEmbedding `json:"embeddings"`
}

type geminiEmbedding struct {
	Values []float64 `json:"values"`
}

const defaultGeminiEmbeddingModel = "models/gemini-embedding-001"

func CreateEmbeddings(ctx context.Context, token, base, model string, inputs []string, options structs.JSONMap) ([]byte, error) {
	if model == "" {
		model = defaultGeminiEmbeddingModel
	}

	cfg := geminiEmbeddingConfig{}
	if taskType := options.GetString("task_type"); taskType != "" {
		cfg.TaskType = taskType
	}
	if dim := int(options.GetInt64("output_dimensionality")); dim > 0 {
		cfg.OutputDimensionality = dim
	}

	// Build one request per input for batchEmbedContents.
	requests := make([]geminiCreateEmbeddingsInput, len(inputs))
	for i, text := range inputs {
		requests[i] = geminiCreateEmbeddingsInput{
			Model:                 model,
			Content:               geminiContent{Parts: []geminiPart{{Text: text}}},
			geminiEmbeddingConfig: cfg,
		}
	}

	payload := geminiBatchEmbedRequest{Requests: requests}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	base = normalizeGeminiBase(base)
	modelName := strings.TrimPrefix(model, "models/")
	url := fmt.Sprintf("%s/v1beta/models/%s:batchEmbedContents", base, modelName)

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

	var batchOutput geminiBatchEmbedResponse
	if err := json.Unmarshal(respData, &batchOutput); err != nil {
		return nil, err
	}

	items := make([]embeddingData, 0, len(batchOutput.Embeddings))
	for i, emb := range batchOutput.Embeddings {
		encoded, err := encodeFloat64sToBase64(emb.Values)
		if err != nil {
			return nil, err
		}
		items = append(items, embeddingData{
			Object:    "embedding",
			Embedding: encoded,
			Index:     i,
		})
	}

	output := &createEmbeddingsOutput{
		Model:  model,
		Object: "list",
		Data:   items,
	}

	return json.Marshal(output)
}

// encodeFloat64sToBase64 converts float64 values to a base64-encoded
// little-endian float32 array to match the OpenAI base64 embedding format.
func encodeFloat64sToBase64(values []float64) (string, error) {
	buf := new(bytes.Buffer)
	for _, v := range values {
		if err := binary.Write(buf, binary.LittleEndian, float32(v)); err != nil {
			return "", fmt.Errorf("encoding embedding: %w", err)
		}
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
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

