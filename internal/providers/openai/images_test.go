package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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
				"input_tokens_details": map[string]any{
					"text_tokens":         7,
					"image_tokens":        3,
					"cached_text_tokens":  2,
					"cached_image_tokens": 1,
				},
			},
		})
	}))
	defer server.Close()

	respData, rawData, err := CreateImages(
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
	if !strings.Contains(string(rawData), `"created":1`) {
		t.Fatalf("unexpected raw response: %s", string(rawData))
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
	if out.Usage.InputTextTokens != 7 || out.Usage.InputImageTokens != 3 {
		t.Fatalf("unexpected usage details: %#v", out.Usage)
	}
	if out.Usage.CachedTextTokens != 2 || out.Usage.CachedImageTokens != 1 {
		t.Fatalf("unexpected cached usage details: %#v", out.Usage)
	}
}

func TestCreateImagesOpenAIDefaultQualityAndFormat(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"created": 1,
			"data": []map[string]any{
				{"b64_json": "QUJD"},
			},
		})
	}))
	defer server.Close()

	_, _, err := CreateImages(
		context.Background(),
		"test-key",
		server.URL,
		"gpt-image-2",
		"draw a cat",
		1,
		nil,
	)
	if err != nil {
		t.Fatalf("CreateImages: %v", err)
	}
	if got["quality"] != "medium" {
		t.Fatalf("unexpected default quality: %#v", got["quality"])
	}
	if got["output_format"] != "webp" {
		t.Fatalf("unexpected default output_format: %#v", got["output_format"])
	}
}

func TestEditImagesMultipartPayload(t *testing.T) {
	var sawRequest bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawRequest = true
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if r.URL.Path != "/v1/images/edits" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data;") {
			t.Fatalf("expected multipart content type, got %q", r.Header.Get("Content-Type"))
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			t.Fatalf("parse multipart: %v", err)
		}

		fields := r.MultipartForm.Value
		assertFormValue(t, fields, "model", "gpt-image-2")
		assertFormValue(t, fields, "prompt", "edit this")
		assertFormValue(t, fields, "n", "1")
		assertFormValue(t, fields, "size", "2048x1152")
		assertFormValue(t, fields, "quality", "auto")
		assertFormValue(t, fields, "background", "opaque")
		assertFormValue(t, fields, "moderation", "low")
		assertFormValue(t, fields, "output_format", "jpeg")
		assertFormValue(t, fields, "output_compression", "80")
		assertFormValue(t, fields, "user", "user-123")

		files := r.MultipartForm.File["image[]"]
		if len(files) != 2 {
			t.Fatalf("expected 2 image[] files, got %d", len(files))
		}
		if files[0].Filename != "source.png" {
			t.Fatalf("unexpected first filename: %q", files[0].Filename)
		}
		if files[0].Header.Get("Content-Type") != "image/png" {
			t.Fatalf("unexpected first content type: %q", files[0].Header.Get("Content-Type"))
		}
		if files[1].Filename != "image-2.jpg" {
			t.Fatalf("unexpected second filename: %q", files[1].Filename)
		}
		if files[1].Header.Get("Content-Type") != "image/jpeg" {
			t.Fatalf("unexpected second content type: %q", files[1].Header.Get("Content-Type"))
		}

		first, err := files[0].Open()
		if err != nil {
			t.Fatalf("open first file: %v", err)
		}
		defer first.Close()
		firstData, err := io.ReadAll(first)
		if err != nil {
			t.Fatalf("read first file: %v", err)
		}
		if !bytes.Equal(firstData, []byte("png-data")) {
			t.Fatalf("unexpected first file data: %q", firstData)
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"created": 2,
			"data": []map[string]any{
				{
					"b64_json":       "QUJD",
					"revised_prompt": "edited",
				},
			},
			"usage": map[string]any{
				"input_tokens":  11,
				"output_tokens": 22,
				"total_tokens":  33,
				"input_tokens_details": map[string]any{
					"text_tokens":         5,
					"image_tokens":        6,
					"cached_text_tokens":  1,
					"cached_image_tokens": 2,
				},
			},
		})
	}))
	defer server.Close()

	respData, rawData, err := EditImages(
		context.Background(),
		"test-key",
		server.URL,
		"gpt-image-2",
		"edit this",
		[]InputImage{
			{Filename: "source.png", MIMEType: "image/png", Data: []byte("png-data")},
			{MIMEType: "image/jpg", Data: []byte("jpg-data")},
		},
		1,
		structs.JSONMap{
			"background":         "opaque",
			"moderation":         "low",
			"output_compression": 80,
			"output_format":      "jpg",
			"quality":            "auto",
			"size":               "2048x1152",
			"user":               "user-123",
		},
	)
	if err != nil {
		t.Fatalf("EditImages: %v", err)
	}
	if !strings.Contains(string(rawData), `"created":2`) {
		t.Fatalf("unexpected raw response: %s", string(rawData))
	}
	if !sawRequest {
		t.Fatalf("expected request")
	}

	var out createImagesOutput
	if err := json.Unmarshal(respData, &out); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if out.MimeType != "image/jpeg" {
		t.Fatalf("unexpected mime type: %q", out.MimeType)
	}
	if len(out.Images) != 1 || out.Images[0].DataBase64 != "QUJD" || out.Images[0].RevisedPrompt != "edited" {
		t.Fatalf("unexpected images: %#v", out.Images)
	}
	if len(out.Data) != 1 || out.Data[0].B64JSON != "QUJD" {
		t.Fatalf("unexpected compatibility data: %#v", out.Data)
	}
	if out.Usage.InputTextTokens != 5 || out.Usage.InputImageTokens != 6 {
		t.Fatalf("unexpected usage details: %#v", out.Usage)
	}
	if out.Usage.CachedTextTokens != 1 || out.Usage.CachedImageTokens != 2 {
		t.Fatalf("unexpected cached usage details: %#v", out.Usage)
	}
}

func TestEditImagesRejectsMissingInputImages(t *testing.T) {
	_, _, err := EditImages(
		context.Background(),
		"test-key",
		"https://example.invalid",
		"gpt-image-2",
		"edit this",
		nil,
		1,
		structs.JSONMap{"size": "1024x1024"},
	)
	if err == nil {
		t.Fatalf("expected missing input image error")
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

func assertFormValue(t *testing.T, fields map[string][]string, key, want string) {
	t.Helper()
	got := fields[key]
	if len(got) != 1 || got[0] != want {
		t.Fatalf("unexpected form value %s: got %#v, want %q", key, got, want)
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
