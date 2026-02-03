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

func TestNormalizeResponseModalities(t *testing.T) {
	got := normalizeResponseModalities([]string{"Text", "Image", "IMAGE", " text "})
	if len(got) != 2 || got[0] != "TEXT" || got[1] != "IMAGE" {
		t.Fatalf("unexpected normalized modalities: %#v", got)
	}
}
