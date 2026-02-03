package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/lyricat/goutils/structs"
)

type openAICreateImagesInput struct {
	Model             string `json:"model"`
	Prompt            string `json:"prompt"`
	Background        string `json:"background,omitempty"`
	Moderation        string `json:"moderation,omitempty"`
	N                 int    `json:"n"`
	OutputCompression string `json:"output_compression,omitempty"`
	OutputFormat      string `json:"output_format,omitempty"`
	Quality           string `json:"quality,omitempty"`
	Size              string `json:"size"`
}

type openAICreateImagesUsage struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	TotalTokens        int `json:"total_tokens"`
	InputTokensDetails struct {
		ImageTokens int `json:"image_tokens"`
		TextTokens  int `json:"text_tokens"`
	} `json:"input_tokens_details"`
}

type openAICreateImagesOutput struct {
	Created int `json:"created"`
	Data    []struct {
		B64JSON string `json:"b64_json"`
	} `json:"data"`
	OpenAICreateImagesUsage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

type createImagesOutput struct {
	Created int `json:"created"`
	Data    []struct {
		B64JSON string `json:"b64_json"`
	} `json:"data"`
	MimeType string           `json:"mime_type"`
	Usage    createImageUsage `json:"usage"`
}

type createImageUsage struct {
	Size    string `json:"size"`
	Quality string `json:"quality"`

	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

func CreateImages(ctx context.Context, token, base, model, prompt string, count int, options structs.JSONMap) ([]byte, error) {
	payload := &openAICreateImagesInput{
		Model:  model,
		Prompt: prompt,
		N:      count,
	}
	if payload.N <= 0 {
		payload.N = 1
	}

	payload.Quality = options.GetString("quality")
	if payload.Quality == "" {
		payload.Quality = "low"
	}
	payload.Size = options.GetString("size")
	if payload.Size == "" {
		payload.Size = "1024x1024"
	}
	payload.Background = options.GetString("background")
	if payload.Background == "" {
		payload.Background = "auto"
	}
	payload.OutputFormat = options.GetString("output_format")
	if payload.OutputFormat == "" {
		payload.OutputFormat = "webp"
	}

	if err := verifyOpenAIImagesInput(payload); err != nil {
		return nil, err
	}

	reqData, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	respData, err := doRequest(ctx, token, base, "POST", "/images/generations", reqData)
	if err != nil {
		return nil, err
	}

	var resp openAICreateImagesOutput
	if err := json.Unmarshal(respData, &resp); err != nil {
		return nil, err
	}

	out := &createImagesOutput{
		Created: resp.Created,
		Data:    resp.Data,
		Usage: createImageUsage{
			Size:         payload.Size,
			Quality:      payload.Quality,
			InputTokens:  resp.OpenAICreateImagesUsage.InputTokens,
			OutputTokens: resp.OpenAICreateImagesUsage.OutputTokens,
			TotalTokens:  resp.OpenAICreateImagesUsage.TotalTokens,
		},
		MimeType: getMimeType(payload.OutputFormat),
	}

	return json.Marshal(out)
}

func verifyOpenAIImagesInput(input *openAICreateImagesInput) error {
	quality := []string{"high", "medium", "low"}
	size := []string{"1024x1024", "1024x1536", "1536x1024"}
	outputFormat := []string{"webp", "png", "jpg"}
	if !slices.Contains(quality, input.Quality) {
		return fmt.Errorf("quality must be one of %v", quality)
	}
	if !slices.Contains(size, input.Size) {
		return fmt.Errorf("size must be one of %v", size)
	}
	if !slices.Contains(outputFormat, input.OutputFormat) {
		return fmt.Errorf("output format must be one of %v", outputFormat)
	}
	return nil
}

func getMimeType(format string) string {
	switch format {
	case "webp":
		return "image/webp"
	case "png":
		return "image/png"
	case "jpg":
		return "image/jpeg"
	case "jpeg":
		return "image/jpeg"
	default:
		return "image/webp"
	}
}
