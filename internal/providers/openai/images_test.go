package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lyricat/goutils/structs"
)

func TestCreateImagesGPTImage2Payload(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/generations" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"created": 1,
			"data": []map[string]any{
				{"b64_json": "QUJD"},
			},
			"usage": map[string]any{
				"input_tokens":  10,
				"output_tokens": 20,
				"total_tokens":  30,
			},
		})
	}))
	defer server.Close()

	respData, err := CreateImages(
		context.Background(),
		"test-key",
		server.URL,
		"gpt-image-2",
		"draw a cat",
		1,
		structs.JSONMap{
			"background":         "opaque",
			"moderation":         "low",
			"output_compression": 50,
			"output_format":      "jpg",
			"quality":            "auto",
			"size":               "2048x1152",
			"user":               "user-123",
		},
	)
	if err != nil {
		t.Fatalf("CreateImages: %v", err)
	}

	if got["model"] != "gpt-image-2" ||
		got["prompt"] != "draw a cat" ||
		got["background"] != "opaque" ||
		got["moderation"] != "low" ||
		got["output_format"] != "jpeg" ||
		got["quality"] != "auto" ||
		got["size"] != "2048x1152" ||
		got["user"] != "user-123" {
		t.Fatalf("unexpected request payload: %#v", got)
	}
	if got["output_compression"] != float64(50) {
		t.Fatalf("unexpected output_compression: %#v", got["output_compression"])
	}

	var out createImagesOutput
	if err := json.Unmarshal(respData, &out); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if out.MimeType != "image/jpeg" {
		t.Fatalf("unexpected mime type: %q", out.MimeType)
	}
	if out.Usage.Size != "2048x1152" || out.Usage.Quality != "auto" {
		t.Fatalf("unexpected usage metadata: %#v", out.Usage)
	}
	if out.Usage.InputTokens != 10 || out.Usage.OutputTokens != 20 || out.Usage.TotalTokens != 30 {
		t.Fatalf("unexpected usage: %#v", out.Usage)
	}
}

func TestVerifyOpenAIImagesInputGPTImage2Sizes(t *testing.T) {
	valid := []string{
		"auto",
		"1024x1024",
		"2048x1152",
		"3840x2160",
		"2160x3840",
	}
	for _, size := range valid {
		t.Run("valid_"+size, func(t *testing.T) {
			in := validOpenAIImageInput("gpt-image-2", size)
			if err := verifyOpenAIImagesInput(in); err != nil {
				t.Fatalf("expected valid size %q: %v", size, err)
			}
		})
	}

	invalid := []string{
		"bad",
		"1000x1000",
		"1024x320",
		"512x512",
		"4096x1024",
		"3840x3840",
	}
	for _, size := range invalid {
		t.Run("invalid_"+size, func(t *testing.T) {
			in := validOpenAIImageInput("gpt-image-2", size)
			if err := verifyOpenAIImagesInput(in); err == nil {
				t.Fatalf("expected invalid size %q", size)
			}
		})
	}
}

func TestVerifyOpenAIImagesInputGPTImage2RejectsTransparentBackground(t *testing.T) {
	in := validOpenAIImageInput("gpt-image-2", "1024x1024")
	in.Background = "transparent"
	in.OutputFormat = "png"

	if err := verifyOpenAIImagesInput(in); err == nil {
		t.Fatalf("expected transparent background error")
	}
}

func TestVerifyOpenAIImagesInputOutputCompressionValidation(t *testing.T) {
	compression := 50
	in := validOpenAIImageInput("gpt-image-2", "1024x1024")
	in.OutputCompression = &compression
	in.OutputFormat = "png"

	if err := verifyOpenAIImagesInput(in); err == nil {
		t.Fatalf("expected png compression error")
	}

	tooLarge := 101
	in = validOpenAIImageInput("gpt-image-2", "1024x1024")
	in.OutputCompression = &tooLarge

	if err := verifyOpenAIImagesInput(in); err == nil {
		t.Fatalf("expected compression range error")
	}
}

func validOpenAIImageInput(model, size string) *openAICreateImagesInput {
	return &openAICreateImagesInput{
		Model:        model,
		Prompt:       "draw a cat",
		Background:   "auto",
		N:            1,
		OutputFormat: "webp",
		Quality:      "auto",
		Size:         size,
	}
}
