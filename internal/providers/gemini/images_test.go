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
			Model:              GeminiModelNanoBananaPro,
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
			Model:              GeminiModelNanoBanana2,
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
		totalTokens := inputTokens + outputTokens
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
				"promptTokenCount":     inputTokens,
				"candidatesTokenCount": outputTokens,
				"totalTokenCount":      totalTokens,
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
	if out.Usage.InputTokens != 23 || out.Usage.OutputTokens != 43 || out.Usage.TotalTokens != 66 {
		t.Fatalf("unexpected usage: %#v", out.Usage)
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
