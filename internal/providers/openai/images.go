package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/lyricat/goutils/structs"
)

type openAICreateImagesInput struct {
	Model             string `json:"model"`
	Prompt            string `json:"prompt"`
	Background        string `json:"background,omitempty"`
	Moderation        string `json:"moderation,omitempty"`
	N                 int    `json:"n"`
	OutputCompression *int   `json:"output_compression,omitempty"`
	OutputFormat      string `json:"output_format,omitempty"`
	Quality           string `json:"quality,omitempty"`
	Size              string `json:"size"`
	User              string `json:"user,omitempty"`
}

type openAICreateImagesUsage struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	TotalTokens        int `json:"total_tokens"`
	InputTokensDetails struct {
		ImageTokens       int `json:"image_tokens"`
		TextTokens        int `json:"text_tokens"`
		CachedImageTokens int `json:"cached_image_tokens"`
		CachedTextTokens  int `json:"cached_text_tokens"`
	} `json:"input_tokens_details"`
}

type openAICreateImagesOutput struct {
	Created int `json:"created"`
	Data    []struct {
		B64JSON string `json:"b64_json"`
	} `json:"data"`
	Usage openAICreateImagesUsage `json:"usage"`
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

	InputTokens       int `json:"input_tokens"`
	InputTextTokens   int `json:"input_text_tokens,omitempty"`
	InputImageTokens  int `json:"input_image_tokens,omitempty"`
	CachedTextTokens  int `json:"cached_text_tokens,omitempty"`
	CachedImageTokens int `json:"cached_image_tokens,omitempty"`
	OutputTokens      int `json:"output_tokens"`
	TotalTokens       int `json:"total_tokens"`
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
	if options.HasKey("output_compression") {
		compression := int(options.GetInt64("output_compression"))
		payload.OutputCompression = &compression
	}
	payload.Moderation = options.GetString("moderation")
	payload.User = options.GetString("user")

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
			Size:              payload.Size,
			Quality:           payload.Quality,
			InputTokens:       resp.Usage.InputTokens,
			InputTextTokens:   resp.Usage.InputTokensDetails.TextTokens,
			InputImageTokens:  resp.Usage.InputTokensDetails.ImageTokens,
			CachedTextTokens:  resp.Usage.InputTokensDetails.CachedTextTokens,
			CachedImageTokens: resp.Usage.InputTokensDetails.CachedImageTokens,
			OutputTokens:      resp.Usage.OutputTokens,
			TotalTokens:       resp.Usage.TotalTokens,
		},
		MimeType: getMimeType(payload.OutputFormat),
	}

	return json.Marshal(out)
}

func verifyOpenAIImagesInput(input *openAICreateImagesInput) error {
	input.Quality = strings.ToLower(strings.TrimSpace(input.Quality))
	input.Size = strings.ToLower(strings.TrimSpace(input.Size))
	input.Background = strings.ToLower(strings.TrimSpace(input.Background))
	input.Moderation = strings.ToLower(strings.TrimSpace(input.Moderation))
	input.OutputFormat = normalizeOutputFormat(input.OutputFormat)

	quality := []string{"high", "medium", "low", "auto"}
	outputFormat := []string{"webp", "png", "jpeg"}
	background := []string{"auto", "opaque", "transparent"}
	moderation := []string{"", "auto", "low"}

	if input.Model == "" {
		return fmt.Errorf("model is required")
	}
	if input.Prompt == "" {
		return fmt.Errorf("prompt is required")
	}
	if input.N < 1 || input.N > 10 {
		return fmt.Errorf("n must be between 1 and 10")
	}
	if !slices.Contains(quality, input.Quality) {
		return fmt.Errorf("quality must be one of %v", quality)
	}
	if err := verifyOpenAIImageSize(input.Model, input.Size); err != nil {
		return err
	}
	if !slices.Contains(outputFormat, input.OutputFormat) {
		return fmt.Errorf("output format must be one of %v", outputFormat)
	}
	if !slices.Contains(background, input.Background) {
		return fmt.Errorf("background must be one of %v", background)
	}
	if !slices.Contains(moderation, input.Moderation) {
		return fmt.Errorf("moderation must be one of [auto low]")
	}
	if isGPTImage2Model(input.Model) && input.Background == "transparent" {
		return fmt.Errorf("gpt-image-2 does not support transparent background")
	}
	if input.Background == "transparent" && input.OutputFormat == "jpeg" {
		return fmt.Errorf("transparent background requires png or webp output format")
	}
	if input.OutputCompression != nil {
		if *input.OutputCompression < 0 || *input.OutputCompression > 100 {
			return fmt.Errorf("output_compression must be between 0 and 100")
		}
		if input.OutputFormat != "jpeg" && input.OutputFormat != "webp" {
			return fmt.Errorf("output_compression requires jpeg or webp output format")
		}
	}
	return nil
}

func verifyOpenAIImageSize(model, size string) error {
	if size == "auto" {
		return nil
	}
	if isGPTImage2Model(model) {
		return verifyGPTImage2Size(size)
	}

	allowed := []string{"1024x1024", "1024x1536", "1536x1024", "auto"}
	if !slices.Contains(allowed, size) {
		return fmt.Errorf("size must be one of %v", allowed)
	}
	return nil
}

func verifyGPTImage2Size(size string) error {
	width, height, err := parseImageSize(size)
	if err != nil {
		return fmt.Errorf("size must be auto or <width>x<height>: %w", err)
	}
	if width%16 != 0 || height%16 != 0 {
		return fmt.Errorf("gpt-image-2 size edges must be multiples of 16")
	}
	long, short := width, height
	if height > width {
		long, short = height, width
	}
	if long > 3840 {
		return fmt.Errorf("gpt-image-2 maximum edge length is 3840")
	}
	if long > short*3 {
		return fmt.Errorf("gpt-image-2 long edge to short edge ratio must not exceed 3:1")
	}
	pixels := width * height
	if pixels < 655360 || pixels > 8294400 {
		return fmt.Errorf("gpt-image-2 total pixels must be between 655360 and 8294400")
	}
	return nil
}

func parseImageSize(size string) (int, int, error) {
	widthText, heightText, ok := strings.Cut(size, "x")
	if !ok || strings.Contains(heightText, "x") {
		return 0, 0, fmt.Errorf("invalid size %q", size)
	}
	width, err := strconv.Atoi(widthText)
	if err != nil {
		return 0, 0, err
	}
	height, err := strconv.Atoi(heightText)
	if err != nil {
		return 0, 0, err
	}
	if width <= 0 || height <= 0 {
		return 0, 0, fmt.Errorf("width and height must be positive")
	}
	return width, height, nil
}

func normalizeOutputFormat(format string) string {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "jpg" {
		return "jpeg"
	}
	return format
}

func isGPTImage2Model(model string) bool {
	return model == "gpt-image-2" || strings.HasPrefix(model, "gpt-image-2-")
}

func getMimeType(format string) string {
	switch normalizeOutputFormat(format) {
	case "webp":
		return "image/webp"
	case "png":
		return "image/png"
	case "jpeg":
		return "image/jpeg"
	default:
		return "image/webp"
	}
}
