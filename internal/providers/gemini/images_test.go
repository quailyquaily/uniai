package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/quailyquaily/uniai/internal/httputil"
)

func TestGeminiCreateImagesInputVerify_SupportedModels(t *testing.T) {
	t.Run("nano banana", func(t *testing.T) {
		in := &GeminiCreateImagesInput{
			Model:              GeminiModelNanoBanana,
			Prompt:             "p",
			NumberOfImages:     1,
			AspectRatio:        AspectRatioLandscape169,
			ResponseModalities: []string{"IMAGE"},
			ImageSize:          "2K",
		}
		if err := in.Verify(); err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
	})

	t.Run("nano banana pro", func(t *testing.T) {
		in := &GeminiCreateImagesInput{
			Model:              "gemini-3-pro-image",
			Prompt:             "p",
			NumberOfImages:     1,
			AspectRatio:        AspectRatioLandscape219,
			ResponseModalities: []string{"TEXT", "IMAGE"},
			ImageSize:          "4K",
		}
		if err := in.Verify(); err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
	})

	t.Run("nano banana 2", func(t *testing.T) {
		in := &GeminiCreateImagesInput{
			Model:              "gemini-3.1-flash-image",
			Prompt:             "p",
			NumberOfImages:     1,
			AspectRatio:        AspectRatioLandscape169,
			ResponseModalities: []string{"TEXT", "IMAGE"},
			ImageSize:          "2K",
		}
		if err := in.Verify(); err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
	})

	t.Run("nano banana 2 lite", func(t *testing.T) {
		in := &GeminiCreateImagesInput{
			Model:              "gemini-3.1-flash-lite-image",
			Prompt:             "p",
			NumberOfImages:     1,
			AspectRatio:        AspectRatioSquare,
			ResponseModalities: []string{"TEXT", "IMAGE"},
			ImageSize:          "1K",
		}
		if err := in.Verify(); err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
	})

	for _, model := range []string{"gemini-3-pro-image-preview", "gemini-3.1-flash-image-preview"} {
		t.Run("retired "+model, func(t *testing.T) {
			in := &GeminiCreateImagesInput{Model: model, Prompt: "p", NumberOfImages: 1}
			if err := in.Verify(); !errors.Is(err, errInvalidGeminiModel) {
				t.Fatalf("expected retired model to be rejected, got %v", err)
			}
		})
	}

	t.Run("imagen", func(t *testing.T) {
		in := &GeminiCreateImagesInput{
			Model:             GeminiModelImagen3,
			Prompt:            "p",
			NumberOfImages:    1,
			AspectRatio:       AspectRatioSquare,
			SafetyFilterLevel: BlockOnlyHigh,
			PersonGeneration:  Allow,
		}
		if err := in.Verify(); err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
	})

	t.Run("unknown model", func(t *testing.T) {
		in := &GeminiCreateImagesInput{
			Model:          "not-a-model",
			Prompt:         "p",
			NumberOfImages: 1,
		}
		err := in.Verify()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, errInvalidGeminiModel) {
			t.Fatalf("expected errInvalidGeminiModel, got %v", err)
		}
	})
}

func TestGeminiCreateImagesInputVerify_ModelSpecificImageOptions(t *testing.T) {
	t.Run("3.1 flash models accept extended aspect ratios", func(t *testing.T) {
		for _, model := range []string{GeminiModelNanoBanana2, GeminiModelNanoBanana2Lite} {
			for _, aspectRatio := range []string{"1:4", "4:1", "1:8", "8:1"} {
				in := &GeminiCreateImagesInput{
					Model:              model,
					Prompt:             "p",
					NumberOfImages:     1,
					AspectRatio:        aspectRatio,
					ResponseModalities: []string{"IMAGE"},
					ImageSize:          "1K",
				}
				if err := in.Verify(); err != nil {
					t.Fatalf("model %q aspect ratio %q: expected nil error, got %v", model, aspectRatio, err)
				}
			}
		}
	})

	t.Run("nano banana 2 accepts 512 image size", func(t *testing.T) {
		in := &GeminiCreateImagesInput{
			Model:              GeminiModelNanoBanana2,
			Prompt:             "p",
			NumberOfImages:     1,
			AspectRatio:        AspectRatioSquare,
			ResponseModalities: []string{"IMAGE"},
			ImageSize:          "512",
		}
		if err := in.Verify(); err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
	})

	t.Run("nano banana 2 lite rejects unsupported image sizes", func(t *testing.T) {
		for _, imageSize := range []string{"512", "2K", "4K"} {
			in := &GeminiCreateImagesInput{
				Model:              GeminiModelNanoBanana2Lite,
				Prompt:             "p",
				NumberOfImages:     1,
				AspectRatio:        AspectRatioSquare,
				ResponseModalities: []string{"IMAGE"},
				ImageSize:          imageSize,
			}
			if err := in.Verify(); err == nil {
				t.Fatalf("image size %q: expected error, got nil", imageSize)
			}
		}
	})
}

func TestGeminiCreateImagesInputVerify_EditRejectsMultipleReturnedImages(t *testing.T) {
	in := &GeminiCreateImagesInput{
		Model:          GeminiModelNanoBanana2,
		Prompt:         "p",
		NumberOfImages: 2,
		InputImages: []InputImage{
			{MIMEType: "image/png", Data: []byte("image-data")},
		},
	}
	if err := in.Verify(); err == nil {
		t.Fatalf("expected error")
	}
}

func TestBuildGeminiGenerateContentRequestBodyWithInputImages(t *testing.T) {
	body := buildGeminiGenerateContentRequestBody(
		"redraw",
		[]InputImage{{MIMEType: "image/png", Data: []byte("ABC")}},
		[]string{"TEXT", "IMAGE"},
		AspectRatioLandscape169,
		"2K",
	)

	contents, ok := body["contents"].([]map[string]any)
	if !ok || len(contents) != 1 {
		t.Fatalf("unexpected contents: %#v", body["contents"])
	}
	parts, ok := contents[0]["parts"].([]map[string]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("unexpected parts: %#v", contents[0]["parts"])
	}
	if parts[0]["text"] != "redraw" {
		t.Fatalf("unexpected text part: %#v", parts[0])
	}
	inlineData, ok := parts[1]["inlineData"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected inlineData: %#v", parts[1]["inlineData"])
	}
	if inlineData["mimeType"] != "image/png" || inlineData["data"] != "QUJD" {
		t.Fatalf("unexpected inlineData: %#v", inlineData)
	}

	config, ok := body["generationConfig"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected generation config: %#v", body["generationConfig"])
	}
	modalities, ok := config["responseModalities"].([]string)
	if !ok || len(modalities) != 2 || modalities[0] != "TEXT" || modalities[1] != "IMAGE" {
		t.Fatalf("unexpected modalities: %#v", config["responseModalities"])
	}
	imageConfig, ok := config["imageConfig"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected image config: %#v", config["imageConfig"])
	}
	if imageConfig["aspectRatio"] != AspectRatioLandscape169 {
		t.Fatalf("unexpected aspect ratio: %#v", imageConfig["aspectRatio"])
	}
	if imageConfig["imageSize"] != "2K" {
		t.Fatalf("unexpected image size: %#v", imageConfig["imageSize"])
	}
	if _, ok := config["aspectRatio"]; ok {
		t.Fatalf("aspectRatio should be nested in imageConfig: %#v", config)
	}
	if _, ok := config["imageSize"]; ok {
		t.Fatalf("imageSize should be nested in imageConfig: %#v", config)
	}
}

func TestGeminiGenerateContentImagesMapsUsageMetadata(t *testing.T) {
	originalTransport := httputil.DefaultClient.Transport
	defer func() {
		httputil.DefaultClient.Transport = originalTransport
	}()

	call := 0
	httputil.DefaultClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		call++
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/v1beta/models/"+GeminiModelNanoBanana2+":generateContent") {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("x-goog-api-key"); got != "test-key" {
			t.Fatalf("unexpected api key header: %q", got)
		}

		inputTokens := 10 + call
		outputTokens := 20 + call
		thoughtsTokens := 3 + call
		totalTokens := inputTokens + outputTokens + thoughtsTokens
		body, err := json.Marshal(map[string]any{
			"candidates": []map[string]any{
				{
					"content": map[string]any{
						"parts": []map[string]any{
							{
								"inlineData": map[string]any{
									"mimeType": "image/png",
									"data":     "QUJD",
								},
							},
						},
					},
				},
			},
			"usageMetadata": map[string]any{
				"promptTokenCount":        inputTokens,
				"cachedContentTokenCount": 2,
				"candidatesTokenCount":    outputTokens,
				"thoughtsTokenCount":      thoughtsTokens,
				"totalTokenCount":         totalTokens,
				"promptTokensDetails": []map[string]any{
					{"modality": "TEXT", "tokenCount": 4 + call},
					{"modality": "IMAGE", "tokenCount": inputTokens - 4 - call},
				},
				"cacheTokensDetails": []map[string]any{
					{"modality": "TEXT", "tokenCount": 1},
					{"modality": "IMAGE", "tokenCount": 1},
				},
				"candidatesTokensDetails": []map[string]any{
					{"modality": "TEXT", "tokenCount": 2 + call},
					{"modality": "IMAGE", "tokenCount": outputTokens - 2 - call},
				},
			},
		})
		if err != nil {
			t.Fatalf("marshal response: %v", err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(string(body))),
		}, nil
	})

	respData, _, err := CreateImages(
		context.Background(),
		"test-key",
		GeminiModelNanoBanana2,
		"draw a cat",
		2,
		nil,
	)
	if err != nil {
		t.Fatalf("CreateImages: %v", err)
	}
	if call != 2 {
		t.Fatalf("expected 2 requests, got %d", call)
	}

	var out createImagesOutput
	if err := json.Unmarshal(respData, &out); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if len(out.Images) != 2 {
		t.Fatalf("expected 2 images, got %d", len(out.Images))
	}
	if out.Usage.InputTokens != 23 || out.Usage.OutputTokens != 52 || out.Usage.TotalTokens != 75 {
		t.Fatalf("unexpected usage: %#v", out.Usage)
	}
	var wire struct {
		Usage struct {
			InputTextTokens   int `json:"input_text_tokens"`
			InputImageTokens  int `json:"input_image_tokens"`
			CachedTextTokens  int `json:"cached_text_tokens"`
			CachedImageTokens int `json:"cached_image_tokens"`
			OutputTextTokens  int `json:"output_text_tokens"`
			OutputImageTokens int `json:"output_image_tokens"`
			ThoughtsTokens    int `json:"thoughts_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(respData, &wire); err != nil {
		t.Fatalf("decode detailed usage: %v", err)
	}
	if wire.Usage.InputTextTokens != 11 || wire.Usage.InputImageTokens != 12 {
		t.Fatalf("unexpected input details: %#v", wire.Usage)
	}
	if wire.Usage.CachedTextTokens != 2 || wire.Usage.CachedImageTokens != 2 {
		t.Fatalf("unexpected cache details: %#v", wire.Usage)
	}
	if wire.Usage.OutputTextTokens != 7 || wire.Usage.OutputImageTokens != 36 || wire.Usage.ThoughtsTokens != 9 {
		t.Fatalf("unexpected output details: %#v", wire.Usage)
	}
}

func TestNormalizeResponseModalities(t *testing.T) {
	got := normalizeResponseModalities([]string{"Text", "Image", "IMAGE", " text "})
	if len(got) != 2 || got[0] != "TEXT" || got[1] != "IMAGE" {
		t.Fatalf("unexpected normalized modalities: %#v", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}
