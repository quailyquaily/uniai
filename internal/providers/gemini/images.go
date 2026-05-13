package gemini

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/quailyquaily/uniai/internal/httputil"

	"github.com/lyricat/goutils/structs"
)

type createImagesOutput struct {
	Created  int              `json:"created"`
	Images   []imageAsset     `json:"images,omitempty"`
	Text     string           `json:"text,omitempty"`
	Data     []imageData      `json:"data"`
	MimeType string           `json:"mime_type"`
	Usage    createImageUsage `json:"usage"`
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

type createImageUsage struct {
	Size    string `json:"size"`
	Quality string `json:"quality"`

	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

type (
	GeminiCreateImagesInput struct {
		Model  string
		Prompt string

		NumberOfImages    int
		AspectRatio       string
		SafetyFilterLevel string
		PersonGeneration  string

		// Native Gemini image generation (Nano Banana)
		ResponseModalities []string
		ImageSize          string
		InputImages        []InputImage
	}

	GeminiCreateImagesOutput struct {
		Images []image.Image
		Text   string
	}
)

type InputImage struct {
	Filename string
	MIMEType string
	Data     []byte
}

const (
	AspectRatioSquare       = "1:1"
	AspectRatioPortrait23   = "2:3"
	AspectRatioLandscape32  = "3:2"
	AspectRatioPortrait34   = "3:4"
	AspectRatioLandscape43  = "4:3"
	AspectRatioPortrait45   = "4:5"
	AspectRatioLandscape54  = "5:4"
	AspectRatioPortrait916  = "9:16"
	AspectRatioLandscape169 = "16:9"
	AspectRatioLandscape219 = "21:9"
)

const (
	BlockLowAndAbove    = "BLOCK_LOW_AND_ABOVE"
	BlockMediumAndAbove = "BLOCK_MEDIUM_AND_ABOVE"
	BlockOnlyHigh       = "BLOCK_ONLY_HIGH"
)

const (
	DontAllow = "DONT_ALLOW"
	Allow     = "ALLOW_ADULT"
)

const (
	ResponseModalityText        = "TEXT"
	ResponseModalityImage       = "IMAGE"
	ResponseModalityTextLegacy  = "Text"
	ResponseModalityImageLegacy = "Image"
)

const (
	// Imagen (predict endpoint)
	GeminiModelImagen3 = "imagen-3.0-generate-002"

	// Nano Banana native image generation (generateContent endpoint)
	GeminiModelNanoBanana    = "gemini-2.5-flash-image"
	GeminiModelNanoBananaPro = "gemini-3-pro-image-preview"
	GeminiModelNanoBanana2   = "gemini-3.1-flash-image-preview"
)

var (
	errInvalidGeminiModel = errors.New("invalid gemini image model")
)

func CreateImages(ctx context.Context, token, model, prompt string, count int, options structs.JSONMap) ([]byte, []byte, error) {
	if model == "" {
		model = GeminiModelImagen3
	}
	geminiInput := &GeminiCreateImagesInput{}
	geminiInput.LoadFrom(model, prompt, count, options)
	if err := geminiInput.Verify(); err != nil {
		return nil, nil, err
	}

	var (
		result *createImagesOutput
		raw    []byte
		err    error
	)
	switch geminiInput.Model {
	case GeminiModelImagen3:
		result, raw, err = geminiPredictImagen(ctx, token, geminiInput)
	case GeminiModelNanoBanana, GeminiModelNanoBananaPro, GeminiModelNanoBanana2:
		result, raw, err = geminiGenerateContentImages(ctx, token, geminiInput)
	default:
		err = fmt.Errorf("%w: %s", errInvalidGeminiModel, geminiInput.Model)
	}
	if err != nil {
		return nil, raw, err
	}
	out, err := json.Marshal(result)
	return out, raw, err
}

func EditImages(ctx context.Context, token, model, prompt string, images []InputImage, count int, options structs.JSONMap) ([]byte, []byte, error) {
	if model == "" {
		model = GeminiModelNanoBanana2
	}
	geminiInput := &GeminiCreateImagesInput{}
	geminiInput.LoadFrom(model, prompt, count, options)
	geminiInput.InputImages = append([]InputImage{}, images...)
	if err := geminiInput.Verify(); err != nil {
		return nil, nil, err
	}
	if len(geminiInput.InputImages) == 0 {
		return nil, nil, fmt.Errorf("at least one input image is required")
	}

	switch geminiInput.Model {
	case GeminiModelNanoBananaPro, GeminiModelNanoBanana2:
		result, raw, err := geminiGenerateContentImages(ctx, token, geminiInput)
		if err != nil {
			return nil, raw, err
		}
		out, err := json.Marshal(result)
		return out, raw, err
	default:
		return nil, nil, fmt.Errorf("%w: %s (image edit supported: %s, %s)", errInvalidGeminiModel, geminiInput.Model, GeminiModelNanoBananaPro, GeminiModelNanoBanana2)
	}
}

func (i2 *GeminiCreateImagesInput) LoadFrom(model, prompt string, count int, options structs.JSONMap) {
	i2.Model = model
	i2.Prompt = prompt
	i2.NumberOfImages = count
	if i2.NumberOfImages <= 0 {
		i2.NumberOfImages = 1
	}
	if i2.NumberOfImages > 4 {
		i2.NumberOfImages = 4
	}

	i2.AspectRatio = options.GetString("aspect_ratio")
	if i2.AspectRatio == "" {
		i2.AspectRatio = options.GetString("aspectRatio")
	}
	if i2.AspectRatio == "" {
		i2.AspectRatio = AspectRatioSquare
	}

	i2.SafetyFilterLevel = options.GetString("safety_filter_level")
	if i2.SafetyFilterLevel == "" {
		i2.SafetyFilterLevel = BlockOnlyHigh
	}
	i2.PersonGeneration = options.GetString("person_generation")
	if i2.PersonGeneration == "" {
		i2.PersonGeneration = Allow
	}

	i2.ResponseModalities = options.GetStringArray("response_modalities")
	if len(i2.ResponseModalities) == 0 {
		i2.ResponseModalities = options.GetStringArray("responseModalities")
	}
	i2.ImageSize = options.GetString("image_size")
	if i2.ImageSize == "" {
		i2.ImageSize = options.GetString("imageSize")
	}
}

func (i *GeminiCreateImagesInput) Verify() error {
	if strings.TrimSpace(i.Prompt) == "" {
		return fmt.Errorf("prompt is required")
	}

	switch i.Model {
	case GeminiModelImagen3:
		aspectRatio := []string{
			AspectRatioSquare,
			AspectRatioPortrait34,
			AspectRatioLandscape43,
			AspectRatioPortrait916,
			AspectRatioLandscape169,
		}
		personGeneration := []string{DontAllow, Allow}
		safetyFilterLevel := []string{BlockLowAndAbove, BlockMediumAndAbove, BlockOnlyHigh}
		if !slices.Contains(aspectRatio, i.AspectRatio) {
			return fmt.Errorf("aspect ratio must be one of %v", aspectRatio)
		}
		if !slices.Contains(personGeneration, i.PersonGeneration) {
			return fmt.Errorf("person generation must be one of %v", personGeneration)
		}
		if !slices.Contains(safetyFilterLevel, i.SafetyFilterLevel) {
			return fmt.Errorf("safety filter level must be one of %v", safetyFilterLevel)
		}
		return nil

	case GeminiModelNanoBanana, GeminiModelNanoBananaPro, GeminiModelNanoBanana2:
		aspectRatio := []string{
			AspectRatioSquare,
			AspectRatioPortrait23,
			AspectRatioLandscape32,
			AspectRatioPortrait34,
			AspectRatioLandscape43,
			AspectRatioPortrait45,
			AspectRatioLandscape54,
			AspectRatioPortrait916,
			AspectRatioLandscape169,
			AspectRatioLandscape219,
		}
		if i.AspectRatio != "" && !slices.Contains(aspectRatio, i.AspectRatio) {
			return fmt.Errorf("aspect ratio must be one of %v", aspectRatio)
		}

		if len(i.ResponseModalities) == 0 {
			i.ResponseModalities = []string{ResponseModalityText, ResponseModalityImage}
		}
		if err := verifyResponseModalities(i.ResponseModalities); err != nil {
			return err
		}

		if i.ImageSize != "" {
			allowed := []string{"1K", "2K", "4K"}
			if !slices.Contains(allowed, i.ImageSize) {
				return fmt.Errorf("image size must be one of %v", allowed)
			}
		}
		if len(i.InputImages) > 0 {
			if i.NumberOfImages > 1 {
				return fmt.Errorf("gemini image edit supports one returned image")
			}
			for idx, image := range i.InputImages {
				if len(image.Data) == 0 {
					return fmt.Errorf("input image %d data is required", idx)
				}
			}
		}
		return nil
	default:
		return fmt.Errorf("%w: %s (supported: %s, %s, %s, %s)", errInvalidGeminiModel, i.Model, GeminiModelImagen3, GeminiModelNanoBanana, GeminiModelNanoBananaPro, GeminiModelNanoBanana2)
	}
}

func verifyResponseModalities(modalities []string) error {
	for _, m := range modalities {
		switch strings.ToUpper(strings.TrimSpace(m)) {
		case "TEXT", "IMAGE":
			// ok
		default:
			return fmt.Errorf("response modalities must be TEXT and/or IMAGE, got %q", m)
		}
	}
	return nil
}

func normalizeResponseModalities(modalities []string) []string {
	out := make([]string, 0, len(modalities))
	seen := map[string]bool{}
	for _, m := range modalities {
		m = strings.TrimSpace(m)
		switch strings.ToUpper(m) {
		case "TEXT":
			m = ResponseModalityText
		case "IMAGE":
			m = ResponseModalityImage
		default:
			// Pass through unknown strings (Verify() should have rejected).
		}
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	return out
}

func geminiPredictImagen(ctx context.Context, token string, geminiInput *GeminiCreateImagesInput) (*createImagesOutput, []byte, error) {
	reqBody := map[string]any{
		"instances": []map[string]any{{
			"prompt": geminiInput.Prompt,
		}},
		"parameters": map[string]any{
			"sampleCount":       geminiInput.NumberOfImages,
			"aspectRatio":       geminiInput.AspectRatio,
			"safetyFilterLevel": geminiInput.SafetyFilterLevel,
			"personGeneration":  geminiInput.PersonGeneration,
		},
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:predict", geminiInput.Model)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", token)

	resp, err := httputil.ClientForContext(ctx).Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, readErr := httputil.ReadBody(resp.Body)
	if readErr != nil {
		return nil, nil, readErr
	}
	if resp.StatusCode != http.StatusOK {
		return nil, body, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var data struct {
		Predictions []struct {
			BytesBase64Encoded string `json:"bytesBase64Encoded"`
			MimeType           string `json:"mimeType"`
		} `json:"predictions"`
	}

	if err := json.Unmarshal(body, &data); err != nil {
		return nil, body, fmt.Errorf("failed to decode response: %w", err)
	}

	now := time.Now()
	result := &createImagesOutput{
		Created: int(now.Unix()),
	}

	for _, item := range data.Predictions {
		result.Images = append(result.Images, imageAsset{
			DataBase64: item.BytesBase64Encoded,
			MIMEType:   item.MimeType,
		})
		result.Data = append(result.Data, imageData{B64JSON: item.BytesBase64Encoded})
	}

	if len(data.Predictions) > 0 {
		result.MimeType = data.Predictions[0].MimeType
	}

	return result, body, nil
}

func geminiGenerateContentImages(ctx context.Context, token string, geminiInput *GeminiCreateImagesInput) (*createImagesOutput, []byte, error) {
	now := time.Now()
	result := &createImagesOutput{
		Created: int(now.Unix()),
	}
	rawResponses := make([]json.RawMessage, 0, geminiInput.NumberOfImages)

	modalities := normalizeResponseModalities(geminiInput.ResponseModalities)
	if len(modalities) == 0 {
		modalities = []string{ResponseModalityText, ResponseModalityImage}
	}

	for i := 0; i < geminiInput.NumberOfImages; i++ {
		resp, raw, err := geminiGenerateContentOnce(ctx, token, geminiInput.Model, geminiInput.Prompt, geminiInput.InputImages, modalities, geminiInput.AspectRatio, geminiInput.ImageSize)
		if err != nil {
			return nil, raw, err
		}
		rawResponses = append(rawResponses, json.RawMessage(append([]byte(nil), raw...)))
		result.Text += resp.text
		for _, item := range resp.images {
			result.Images = append(result.Images, imageAsset{
				DataBase64: item.data,
				MIMEType:   item.mimeType,
			})
			result.Data = append(result.Data, imageData{B64JSON: item.data})
			if result.MimeType == "" && item.mimeType != "" {
				result.MimeType = item.mimeType
			}
		}
	}

	if len(result.Data) == 0 {
		raw, _ := json.Marshal(rawResponses)
		return nil, raw, fmt.Errorf("no images returned from model %s", geminiInput.Model)
	}
	raw := []byte(nil)
	if len(rawResponses) == 1 {
		raw = rawResponses[0]
	} else if len(rawResponses) > 1 {
		var err error
		raw, err = json.Marshal(rawResponses)
		if err != nil {
			return nil, nil, err
		}
	}
	return result, raw, nil
}

type geminiGeneratedImage struct {
	data     string
	mimeType string
}

type geminiGenerateContentParsed struct {
	text   string
	images []geminiGeneratedImage
}

func geminiGenerateContentOnce(ctx context.Context, token, model, prompt string, inputImages []InputImage, responseModalities []string, aspectRatio, imageSize string) (*geminiGenerateContentParsed, []byte, error) {
	reqBody := buildGeminiGenerateContentRequestBody(prompt, inputImages, responseModalities, aspectRatio, imageSize)
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent", model)
	if strings.HasPrefix(model, "imagen-") {
		url = fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateImage", model)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", token)

	resp, err := httputil.ClientForContext(ctx).Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, readErr := httputil.ReadBody(resp.Body)
	if readErr != nil {
		return nil, nil, readErr
	}
	if resp.StatusCode != http.StatusOK {
		return nil, body, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var data struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text       string `json:"text"`
					InlineData struct {
						MimeType string `json:"mimeType"`
						Data     string `json:"data"`
					} `json:"inlineData"`
					FileData struct {
						MimeType string `json:"mimeType"`
						FileURI  string `json:"fileUri"`
					} `json:"fileData"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}

	if err := json.Unmarshal(body, &data); err != nil {
		return nil, body, fmt.Errorf("failed to decode response: %w", err)
	}

	parsed := &geminiGenerateContentParsed{}
	for _, candidate := range data.Candidates {
		for _, part := range candidate.Content.Parts {
			if part.Text != "" {
				parsed.text += part.Text
			}
			if part.InlineData.Data != "" {
				parsed.images = append(parsed.images, geminiGeneratedImage{data: part.InlineData.Data, mimeType: part.InlineData.MimeType})
			}
			if part.FileData.FileURI != "" {
				img, mimeType, err := geminiDownloadImage(ctx, part.FileData.FileURI)
				if err != nil {
					return nil, body, err
				}
				parsed.images = append(parsed.images, geminiGeneratedImage{data: img, mimeType: mimeType})
			}
		}
	}

	return parsed, body, nil
}

func buildGeminiGenerateContentRequestBody(prompt string, inputImages []InputImage, responseModalities []string, aspectRatio, imageSize string) map[string]any {
	parts := []map[string]any{{
		"text": prompt,
	}}
	for _, image := range inputImages {
		mimeType := strings.TrimSpace(image.MIMEType)
		if mimeType == "" {
			mimeType = http.DetectContentType(image.Data)
		}
		parts = append(parts, map[string]any{
			"inlineData": map[string]any{
				"mimeType": mimeType,
				"data":     base64.StdEncoding.EncodeToString(image.Data),
			},
		})
	}
	generationConfig := map[string]any{
		"responseModalities": responseModalities,
	}
	imageConfig := map[string]any{}
	if aspectRatio != "" {
		imageConfig["aspectRatio"] = aspectRatio
	}
	if imageSize != "" {
		imageConfig["imageSize"] = imageSize
	}
	if len(imageConfig) > 0 {
		generationConfig["imageConfig"] = imageConfig
	}

	return map[string]any{
		"contents": []map[string]any{{
			"parts": parts,
		}},
		"generationConfig": generationConfig,
	}
}

func geminiDownloadImage(ctx context.Context, uri string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", uri, nil)
	if err != nil {
		return "", "", err
	}

	resp, err := httputil.ClientForContext(ctx).Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	data, err := httputil.ReadBody(resp.Body)
	if err != nil {
		return "", "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("image download failed with status %d: %s", resp.StatusCode, string(data))
	}

	encoded := base64.StdEncoding.EncodeToString(data)
	return encoded, resp.Header.Get("Content-Type"), nil
}
