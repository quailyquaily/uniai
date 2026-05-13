package gemini

import (
	"errors"
	"testing"
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
	if config["aspectRatio"] != AspectRatioLandscape169 {
		t.Fatalf("unexpected aspect ratio: %#v", config["aspectRatio"])
	}
	if config["imageSize"] != "2K" {
		t.Fatalf("unexpected image size: %#v", config["imageSize"])
	}
}

func TestNormalizeResponseModalities(t *testing.T) {
	got := normalizeResponseModalities([]string{"Text", "Image", "IMAGE", " text "})
	if len(got) != 2 || got[0] != "TEXT" || got[1] != "IMAGE" {
		t.Fatalf("unexpected normalized modalities: %#v", got)
	}
}
