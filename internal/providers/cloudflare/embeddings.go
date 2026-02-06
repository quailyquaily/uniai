package cloudflare

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"

	"github.com/lyricat/goutils/structs"
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

func CreateEmbeddings(ctx context.Context, token, base, accountID, model string, inputs []string, options structs.JSONMap) ([]byte, error) {
	payload := structs.NewJSONMap()
	payload.Merge(options)

	if !payload.HasKey("text") {
		if len(inputs) == 1 {
			payload["text"] = inputs[0]
		} else {
			payload["text"] = append([]string{}, inputs...)
		}
	}

	resultRaw, err := RunJSON(ctx, token, base, accountID, model, payload)
	if err != nil {
		return nil, err
	}

	vectors, err := extractEmbeddingVectors(resultRaw)
	if err != nil {
		return nil, err
	}

	output := &createEmbeddingsOutput{
		Model:  model,
		Object: "list",
		Data:   make([]embeddingData, 0, len(vectors)),
	}
	for i, vec := range vectors {
		output.Data = append(output.Data, embeddingData{
			Object:    "embedding",
			Embedding: encodeFloat64sToBase64(vec),
			Index:     i,
		})
	}

	return json.Marshal(output)
}

func extractEmbeddingVectors(resultRaw []byte) ([][]float64, error) {
	var raw any
	if err := json.Unmarshal(resultRaw, &raw); err != nil {
		return nil, err
	}
	vectors := parseVectors(raw)
	if len(vectors) == 0 {
		return nil, fmt.Errorf("cloudflare embeddings response missing data vectors")
	}
	return vectors, nil
}

func parseVectors(raw any) [][]float64 {
	switch val := raw.(type) {
	case map[string]any:
		if data, ok := val["data"]; ok {
			return parseVectors(data)
		}
		if data, ok := val["embedding"]; ok {
			return parseVectors(data)
		}
	case []any:
		if len(val) == 0 {
			return nil
		}
		if _, ok := val[0].(float64); ok {
			return [][]float64{toFloatSlice(val)}
		}
		out := make([][]float64, 0, len(val))
		for _, item := range val {
			if vec, ok := item.([]any); ok {
				out = append(out, toFloatSlice(vec))
			}
		}
		return out
	}
	return nil
}

func toFloatSlice(vals []any) []float64 {
	out := make([]float64, 0, len(vals))
	for _, item := range vals {
		if f, ok := item.(float64); ok {
			out = append(out, f)
		}
	}
	return out
}

func encodeFloat64sToBase64(vals []float64) string {
	buf := make([]byte, len(vals)*4)
	for i, v := range vals {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(float32(v)))
	}
	return base64.StdEncoding.EncodeToString(buf)
}
