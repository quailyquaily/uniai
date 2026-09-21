package openai

import "testing"

func TestGPTImage25QualityAndSize(t *testing.T) {
	for _, model := range []string{"gpt-image-2.5-sunburst", "gpt-image-2.5-flare", "gpt-image-2.5-sunburst-2026-09-08", "gpt-image-2.5-flare-2026-09-08"} {
		for _, quality := range []string{"low", "medium", "high", "xhigh", "max", "auto"} {
			in := validOpenAIImageInput(model, "1536x864")
			in.Quality, in.Background, in.OutputFormat = quality, "transparent", "png"
			if err := verifyOpenAIImagesInput(in); err != nil {
				t.Errorf("%s %s: %v", model, quality, err)
			}
		}
		for _, size := range []string{"512x512", "1537x864", "4096x2048", "3840x3840", "3840x1024"} {
			if err := verifyOpenAIImagesInput(validOpenAIImageInput(model, size)); err == nil {
				t.Errorf("%s: accepted invalid size %s", model, size)
			}
		}
	}
	for _, model := range []string{"gpt-image-2", "gpt-image-1.5"} {
		for _, quality := range []string{"xhigh", "max"} {
			in := validOpenAIImageInput(model, "1024x1024")
			in.Quality = quality
			if err := verifyOpenAIImagesInput(in); err == nil {
				t.Errorf("%s: accepted unsupported quality %s", model, quality)
			}
		}
	}
}
