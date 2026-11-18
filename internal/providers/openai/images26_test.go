package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lyricat/goutils/structs"
)

func intPtr(v int) *int { return &v }

func TestGPTImage26Validation(t *testing.T) {
	models := []string{"gpt-image-2.6-sunburst", "gpt-image-2.6-flare"}
	for _, model := range models {
		for _, quality := range []string{"low", "medium", "high", "xhigh", "max", "auto"} {
			in := validOpenAIImageInput(model, "1536x864")
			in.Quality, in.Background, in.OutputFormat, in.Complexity = quality, "transparent", "png", "low"
			if err := verifyOpenAIImagesInput(in); err != nil {
				t.Errorf("%s %s: %v", model, quality, err)
			}
		}
		for _, size := range []string{"512x512", "1537x864", "4096x2048", "3840x1024"} {
			if err := verifyOpenAIImagesInput(validOpenAIImageInput(model, size)); err == nil {
				t.Errorf("%s: accepted invalid size %s", model, size)
			}
		}

		valid := []func(*openAICreateImagesInput){
			func(in *openAICreateImagesInput) { in.Complexity = " HIGH " },
			func(in *openAICreateImagesInput) { in.OutputFormat = "svg" },
			func(in *openAICreateImagesInput) { in.OutputFormat = "gif" },
			// GIF frames only need multiples of 16; the request pixel budget is enforced upstream.
			func(in *openAICreateImagesInput) { in.OutputFormat, in.Size = "gif", "256x256" },
			func(in *openAICreateImagesInput) {
				in.OutputFormat, in.FrameCount, in.FrameDurationMS = "gif", intPtr(16), intPtr(2000)
			},
			func(in *openAICreateImagesInput) {
				in.OutputFormat, in.FrameCount, in.FrameDurationMS = "gif", intPtr(2), intPtr(20)
			},
		}
		for i, mutate := range valid {
			in := validOpenAIImageInput(model, "1024x1024")
			mutate(in)
			if err := verifyOpenAIImagesInput(in); err != nil {
				t.Errorf("%s valid case %d: %v", model, i, err)
			}
		}

		invalid := map[string]func(*openAICreateImagesInput){
			"complexity value":     func(in *openAICreateImagesInput) { in.Complexity = "max" },
			"svg n":                func(in *openAICreateImagesInput) { in.OutputFormat, in.N = "svg", 2 },
			"svg compression":      func(in *openAICreateImagesInput) { in.OutputFormat, in.OutputCompression = "svg", intPtr(50) },
			"svg transparent":      func(in *openAICreateImagesInput) { in.OutputFormat, in.Background = "svg", "transparent" },
			"gif compression":      func(in *openAICreateImagesInput) { in.OutputFormat, in.OutputCompression = "gif", intPtr(50) },
			"gif frame size":       func(in *openAICreateImagesInput) { in.OutputFormat, in.Size = "gif", "250x256" },
			"gif frame_count low":  func(in *openAICreateImagesInput) { in.OutputFormat, in.FrameCount = "gif", intPtr(1) },
			"gif frame_count high": func(in *openAICreateImagesInput) { in.OutputFormat, in.FrameCount = "gif", intPtr(17) },
			"gif duration low":     func(in *openAICreateImagesInput) { in.OutputFormat, in.FrameDurationMS = "gif", intPtr(10) },
			"gif duration high":    func(in *openAICreateImagesInput) { in.OutputFormat, in.FrameDurationMS = "gif", intPtr(2010) },
			"frames without gif":   func(in *openAICreateImagesInput) { in.FrameCount = intPtr(4) },
		}
		for name, mutate := range invalid {
			in := validOpenAIImageInput(model, "1024x1024")
			mutate(in)
			if err := verifyOpenAIImagesInput(in); err == nil {
				t.Errorf("%s: accepted %s", model, name)
			}
		}
	}

	// GPT-Image-2.5 and earlier do not get the 2.6-only options.
	for _, model := range []string{"gpt-image-2.5-sunburst", "gpt-image-2.5-flare", "gpt-image-2", "gpt-image-2.6-solar"} {
		for name, mutate := range map[string]func(*openAICreateImagesInput){
			"complexity": func(in *openAICreateImagesInput) { in.Complexity = "low" },
			"svg":        func(in *openAICreateImagesInput) { in.OutputFormat = "svg" },
			"gif":        func(in *openAICreateImagesInput) { in.OutputFormat = "gif" },
		} {
			in := validOpenAIImageInput(model, "1024x1024")
			mutate(in)
			if err := verifyOpenAIImagesInput(in); err == nil {
				t.Errorf("%s: accepted %s", model, name)
			}
		}
	}
}

func TestCreateImagesGPTImage26GIFPayload(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = nil
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"created": 1, "data": []map[string]any{{"b64_json": "R0lG"}}})
	}))
	defer server.Close()

	respData, _, err := CreateImages(context.Background(), "test-key", server.URL, "gpt-image-2.6-flare", "a spinning top", 1,
		structs.JSONMap{"output_format": "gif", "frame_count": 12, "frame_duration_ms": 100, "complexity": "medium", "size": "512x512"})
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{"output_format": "gif", "frame_count": float64(12), "frame_duration_ms": float64(100), "complexity": "medium", "size": "512x512"} {
		if got[key] != want {
			t.Errorf("%s = %v, want %v", key, got[key], want)
		}
	}
	var out createImagesOutput
	if err := json.Unmarshal(respData, &out); err != nil {
		t.Fatal(err)
	}
	if out.MimeType != "image/gif" || len(out.Images) != 1 || out.Images[0].MIMEType != "image/gif" {
		t.Errorf("wrong mime type: %+v", out)
	}

	// Fields are omitted when not set.
	respData, _, err = CreateImages(context.Background(), "test-key", server.URL, "gpt-image-2.6-sunburst", "a logo", 1, structs.JSONMap{"output_format": "svg"})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"frame_count", "frame_duration_ms", "complexity"} {
		if _, ok := got[key]; ok {
			t.Errorf("unexpected %s in %v", key, got)
		}
	}
	if err := json.Unmarshal(respData, &out); err != nil || out.MimeType != "image/svg+xml" {
		t.Errorf("wrong svg mime type: %v %q", err, out.MimeType)
	}
}

func TestEditImagesGPTImage26Multipart(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			t.Fatalf("parse multipart: %v", err)
		}
		fields := r.MultipartForm.Value
		assertFormValue(t, fields, "model", "gpt-image-2.6-sunburst")
		assertFormValue(t, fields, "complexity", "low")
		assertFormValue(t, fields, "output_format", "gif")
		assertFormValue(t, fields, "frame_count", "4")
		assertFormValue(t, fields, "frame_duration_ms", "300")
		_ = json.NewEncoder(w).Encode(map[string]any{"created": 2, "data": []map[string]any{{"b64_json": "R0lG"}}})
	}))
	defer server.Close()

	images := []InputImage{{Filename: "source.png", MIMEType: "image/png", Data: []byte("png-data")}}
	if _, _, err := EditImages(context.Background(), "test-key", server.URL, "gpt-image-2.6-sunburst", "animate this", images, 1,
		structs.JSONMap{"output_format": "gif", "frame_count": 4, "frame_duration_ms": 300, "complexity": "low"}); err != nil {
		t.Fatal(err)
	}

	// The edit endpoint does not produce SVG; reject before sending.
	_, _, err := EditImages(context.Background(), "test-key", "http://127.0.0.1:1", "gpt-image-2.6-sunburst", "vectorize", images, 1, structs.JSONMap{"output_format": "svg"})
	if err == nil || !strings.Contains(err.Error(), "svg") {
		t.Fatalf("expected svg edit error, got %v", err)
	}
}
