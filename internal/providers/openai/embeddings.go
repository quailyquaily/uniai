package openai

import (
	"context"
	"encoding/json"

	"github.com/lyricat/goutils/structs"
)

type createEmbeddingsInput struct {
	Model          string   `json:"model"`
	Input          []string `json:"input"`
	EncodingFormat string   `json:"encoding_format,omitempty"`
	Dimensions     *int     `json:"dimensions,omitempty"`
	User           string   `json:"user,omitempty"`
}

func CreateEmbeddings(ctx context.Context, token, base, model string, inputs []string, options structs.JSONMap) ([]byte, error) {
	payload := &createEmbeddingsInput{
		Model:          model,
		Input:          append([]string{}, inputs...),
		EncodingFormat: "base64",
	}

	dimensions := int(options.GetInt64("dimensions"))
	if dimensions > 0 {
		payload.Dimensions = &dimensions
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	return doRequest(ctx, token, base, "POST", "/embeddings", data)
}
