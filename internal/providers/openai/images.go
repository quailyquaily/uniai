package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"slices"
	"strconv"
	"strings"

	"github.com/lyricat/goutils/structs"
)

type openAICreateImagesInput struct {
	Model             string `json:"model"`
	Prompt            string `json:"prompt"`
	Background        string `json:"background,omitempty"`
	Complexity        string `json:"complexity,omitempty"`
	FrameCount        *int   `json:"frame_count,omitempty"`
	FrameDurationMS   *int   `json:"frame_duration_ms,omitempty"`
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
		B64JSON       string `json:"b64_json"`
		URL           string `json:"url"`
		RevisedPrompt string `json:"revised_prompt"`
	} `json:"data"`
	Usage openAICreateImagesUsage `json:"usage"`
}

type InputImage struct {
	Filename string
	MIMEType string
	Data     []byte
}

type imageAsset struct {
	DataBase64    string `json:"data_base64,omitempty"`
	URL           string `json:"url,omitempty"`
	MIMEType      string `json:"mime_type,omitempty"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

type imageData struct {
	B64JSON string `json:"b64_json"`
}

type createImagesOutput struct {
	Created  int              `json:"created"`
	Images   []imageAsset     `json:"images,omitempty"`
	Data     []imageData      `json:"data"`
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

func CreateImages(ctx context.Context, token, base, model, prompt string, count int, options structs.JSONMap) ([]byte, []byte, error) {
	payload := buildOpenAIImagesInput(model, prompt, count, options)
	if err := verifyOpenAIImagesInput(payload); err != nil {
		return nil, nil, err
	}

	reqData, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, err
	}

	respData, err := doRequest(ctx, token, base, "POST", "/images/generations", reqData)
	if err != nil {
		return nil, respData, err
	}

	var resp openAICreateImagesOutput
	if err := json.Unmarshal(respData, &resp); err != nil {
		return nil, respData, err
	}

	out, err := json.Marshal(mapOpenAIImagesOutput(payload, &resp))
	return out, respData, err
}

func EditImages(ctx context.Context, token, base, model, prompt string, images []InputImage, count int, options structs.JSONMap) ([]byte, []byte, error) {
	payload := buildOpenAIImagesInput(model, prompt, count, options)
	if err := verifyOpenAIImagesInput(payload); err != nil {
		return nil, nil, err
	}
	if payload.OutputFormat == "svg" {
		return nil, nil, fmt.Errorf("svg output is supported only for image generation, not edits")
	}
	if err := verifyOpenAIEditImages(images); err != nil {
		return nil, nil, err
	}

	contentType, body, err := buildOpenAIEditImagesMultipart(payload, images)
	if err != nil {
		return nil, nil, err
	}

	respData, err := doRequestWithContentType(ctx, token, base, "POST", "/images/edits", contentType, body)
	if err != nil {
		return nil, respData, err
	}

	var resp openAICreateImagesOutput
	if err := json.Unmarshal(respData, &resp); err != nil {
		return nil, respData, err
	}

	out, err := json.Marshal(mapOpenAIImagesOutput(payload, &resp))
	return out, respData, err
}

func buildOpenAIImagesInput(model, prompt string, count int, options structs.JSONMap) *openAICreateImagesInput {
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
		payload.Quality = "medium"
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
	if options.HasKey("output_compression") {
		compression := int(options.GetInt64("output_compression"))
		payload.OutputCompression = &compression
	}
	payload.Complexity = options.GetString("complexity")
	if options.HasKey("frame_count") {
		frameCount := int(options.GetInt64("frame_count"))
		payload.FrameCount = &frameCount
	}
	if options.HasKey("frame_duration_ms") {
		frameDuration := int(options.GetInt64("frame_duration_ms"))
		payload.FrameDurationMS = &frameDuration
	}
	payload.Moderation = options.GetString("moderation")
	payload.User = options.GetString("user")
	return payload
}

func mapOpenAIImagesOutput(payload *openAICreateImagesInput, resp *openAICreateImagesOutput) *createImagesOutput {
	mimeType := getMimeType(payload.OutputFormat)
	return &createImagesOutput{
		Created: resp.Created,
		Images:  toImageAssets(resp.Data, mimeType),
		Data:    toImageData(resp.Data),
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
		MimeType: mimeType,
	}
}

func verifyOpenAIEditImages(images []InputImage) error {
	if len(images) == 0 {
		return fmt.Errorf("at least one input image is required")
	}
	if len(images) > 16 {
		return fmt.Errorf("at most 16 input images are supported")
	}
	for i, image := range images {
		if len(image.Data) == 0 {
			return fmt.Errorf("input image %d data is required", i)
		}
		mimeType := normalizeImageMIMEType(image.MIMEType, image.Data)
		switch mimeType {
		case "image/png", "image/webp", "image/jpeg":
		default:
			return fmt.Errorf("input image %d mime type must be image/png, image/webp, or image/jpeg", i)
		}
	}
	return nil
}

func buildOpenAIEditImagesMultipart(payload *openAICreateImagesInput, images []InputImage) (string, []byte, error) {
	buf := &bytes.Buffer{}
	writer := multipart.NewWriter(buf)

	fields := map[string]string{
		"model":      payload.Model,
		"prompt":     payload.Prompt,
		"n":          strconv.Itoa(payload.N),
		"size":       payload.Size,
		"quality":    payload.Quality,
		"background": payload.Background,
	}
	if payload.OutputFormat != "" {
		fields["output_format"] = payload.OutputFormat
	}
	if payload.Moderation != "" {
		fields["moderation"] = payload.Moderation
	}
	if payload.Complexity != "" {
		fields["complexity"] = payload.Complexity
	}
	if payload.FrameCount != nil {
		fields["frame_count"] = strconv.Itoa(*payload.FrameCount)
	}
	if payload.FrameDurationMS != nil {
		fields["frame_duration_ms"] = strconv.Itoa(*payload.FrameDurationMS)
	}
	if payload.User != "" {
		fields["user"] = payload.User
	}
	if payload.OutputCompression != nil {
		fields["output_compression"] = strconv.Itoa(*payload.OutputCompression)
	}
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			_ = writer.Close()
			return "", nil, err
		}
	}
	for i, image := range images {
		if err := writeImagePart(writer, "image[]", image, i); err != nil {
			_ = writer.Close()
			return "", nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return "", nil, err
	}
	return writer.FormDataContentType(), buf.Bytes(), nil
}

func writeImagePart(writer *multipart.Writer, field string, image InputImage, index int) error {
	mimeType := normalizeImageMIMEType(image.MIMEType, image.Data)
	filename := strings.TrimSpace(image.Filename)
	if filename == "" {
		filename = defaultImageFilename(mimeType, index)
	}
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, field, escapeMultipartFilename(filename)))
	header.Set("Content-Type", mimeType)
	part, err := writer.CreatePart(header)
	if err != nil {
		return err
	}
	_, err = part.Write(image.Data)
	return err
}

func normalizeImageMIMEType(mimeType string, data []byte) string {
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	if mimeType == "image/jpg" {
		return "image/jpeg"
	}
	if mimeType != "" {
		return mimeType
	}
	return http.DetectContentType(data)
}

func defaultImageFilename(mimeType string, index int) string {
	switch mimeType {
	case "image/png":
		return fmt.Sprintf("image-%d.png", index+1)
	case "image/webp":
		return fmt.Sprintf("image-%d.webp", index+1)
	case "image/jpeg":
		return fmt.Sprintf("image-%d.jpg", index+1)
	default:
		return fmt.Sprintf("image-%d", index+1)
	}
}

func escapeMultipartFilename(filename string) string {
	return strings.NewReplacer("\\", "\\\\", `"`, "\\\"").Replace(filename)
}

func toImageAssets(data []struct {
	B64JSON       string `json:"b64_json"`
	URL           string `json:"url"`
	RevisedPrompt string `json:"revised_prompt"`
}, mimeType string) []imageAsset {
	out := make([]imageAsset, 0, len(data))
	for _, item := range data {
		out = append(out, imageAsset{
			DataBase64:    item.B64JSON,
			URL:           item.URL,
			MIMEType:      mimeType,
			RevisedPrompt: item.RevisedPrompt,
		})
	}
	return out
}

func toImageData(data []struct {
	B64JSON       string `json:"b64_json"`
	URL           string `json:"url"`
	RevisedPrompt string `json:"revised_prompt"`
}) []imageData {
	out := make([]imageData, 0, len(data))
	for _, item := range data {
		if strings.TrimSpace(item.B64JSON) == "" {
			continue
		}
		out = append(out, imageData{B64JSON: item.B64JSON})
	}
	return out
}

func verifyOpenAIImagesInput(input *openAICreateImagesInput) error {
	input.Quality = strings.ToLower(strings.TrimSpace(input.Quality))
	input.Size = strings.ToLower(strings.TrimSpace(input.Size))
	input.Background = strings.ToLower(strings.TrimSpace(input.Background))
	input.Moderation = strings.ToLower(strings.TrimSpace(input.Moderation))
	input.Complexity = strings.ToLower(strings.TrimSpace(input.Complexity))
	input.OutputFormat = normalizeOutputFormat(input.OutputFormat)

	quality := []string{"high", "medium", "low", "auto"}
	if isGPTImage25Model(input.Model) || isGPTImage26Model(input.Model) {
		quality = append(quality, "xhigh", "max")
	}
	outputFormat := []string{"", "webp", "png", "jpeg"}
	if isGPTImage26Model(input.Model) {
		outputFormat = append(outputFormat, "svg", "gif")
	}
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
	if !slices.Contains(outputFormat, input.OutputFormat) {
		return fmt.Errorf("output format must be one of %v", outputFormat)
	}
	if input.OutputFormat == "gif" {
		if err := verifyOpenAIGIFFrameSize(input.Size); err != nil {
			return err
		}
	} else if err := verifyOpenAIImageSize(input.Model, input.Size); err != nil {
		return err
	}
	if err := verifyOpenAIImage26Options(input); err != nil {
		return err
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
	if input.Background == "transparent" && input.OutputFormat != "" && input.OutputFormat != "png" && input.OutputFormat != "webp" {
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

// verifyOpenAIImage26Options checks the GPT-Image-2.6 complexity, SVG, and
// animated GIF options. Rules were checked on 2026-11-18 against
// https://developers.openai.com/api/docs/guides/image-generation#gpt-image-26.
func verifyOpenAIImage26Options(input *openAICreateImagesInput) error {
	if input.Complexity != "" {
		if !isGPTImage26Model(input.Model) {
			return fmt.Errorf("complexity is supported only by GPT-Image-2.6 models")
		}
		if !slices.Contains([]string{"low", "medium", "high"}, input.Complexity) {
			return fmt.Errorf("complexity must be one of [low medium high]")
		}
	}
	if input.OutputFormat == "svg" && input.N != 1 {
		return fmt.Errorf("svg output requires n=1")
	}
	if input.OutputFormat != "gif" {
		if input.FrameCount != nil || input.FrameDurationMS != nil {
			return fmt.Errorf("frame_count and frame_duration_ms require gif output format")
		}
		return nil
	}
	if input.FrameCount != nil && (*input.FrameCount < 2 || *input.FrameCount > 16) {
		return fmt.Errorf("frame_count must be between 2 and 16")
	}
	if input.FrameDurationMS != nil && (*input.FrameDurationMS < 20 || *input.FrameDurationMS > 2000) {
		return fmt.Errorf("frame_duration_ms must be between 20 and 2000")
	}
	return nil
}

// verifyOpenAIGIFFrameSize checks the documented per-frame size rule. OpenAI
// also enforces model size limits and a total request pixel budget for GIFs
// but does not publish the budget, so it is left to the API.
func verifyOpenAIGIFFrameSize(size string) error {
	if size == "auto" {
		return nil
	}
	width, height, err := parseImageSize(size)
	if err != nil {
		return fmt.Errorf("size must be auto or <width>x<height>: %w", err)
	}
	if width%16 != 0 || height%16 != 0 {
		return fmt.Errorf("gif frame size edges must be multiples of 16")
	}
	return nil
}

func verifyOpenAIImageSize(model, size string) error {
	if size == "auto" {
		return nil
	}
	if isGPTImage2Model(model) || isGPTImage25Model(model) || isGPTImage26Model(model) {
		return verifyOpenAICustomImageSize(size)
	}

	allowed := []string{"1024x1024", "1024x1536", "1536x1024", "auto"}
	if !slices.Contains(allowed, size) {
		return fmt.Errorf("size must be one of %v", allowed)
	}
	return nil
}

func verifyOpenAICustomImageSize(size string) error {
	width, height, err := parseImageSize(size)
	if err != nil {
		return fmt.Errorf("size must be auto or <width>x<height>: %w", err)
	}
	if width%16 != 0 || height%16 != 0 {
		return fmt.Errorf("image size edges must be multiples of 16")
	}
	long, short := width, height
	if height > width {
		long, short = height, width
	}
	if long > 3840 {
		return fmt.Errorf("image maximum edge length is 3840")
	}
	if long > short*3 {
		return fmt.Errorf("image long edge to short edge ratio must not exceed 3:1")
	}
	pixels := width * height
	if pixels < 655360 || pixels > 8294400 {
		return fmt.Errorf("image total pixels must be between 655360 and 8294400")
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

func isGPTImage25Model(model string) bool {
	return model == "gpt-image-2.5-sunburst" || strings.HasPrefix(model, "gpt-image-2.5-sunburst-") ||
		model == "gpt-image-2.5-flare" || strings.HasPrefix(model, "gpt-image-2.5-flare-")
}

func isGPTImage26Model(model string) bool {
	return model == "gpt-image-2.6-sunburst" || strings.HasPrefix(model, "gpt-image-2.6-sunburst-") ||
		model == "gpt-image-2.6-flare" || strings.HasPrefix(model, "gpt-image-2.6-flare-")
}

func getMimeType(format string) string {
	switch normalizeOutputFormat(format) {
	case "svg":
		return "image/svg+xml"
	case "gif":
		return "image/gif"
	case "webp":
		return "image/webp"
	case "png":
		return "image/png"
	case "jpeg":
		return "image/jpeg"
	default:
		return "image/png"
	}
}
