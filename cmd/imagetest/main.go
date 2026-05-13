package main

import (
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lyricat/goutils/structs"
	"github.com/quailyquaily/uniai"
)

type providerConfig struct {
	name      string
	model     string
	apiKey    string
	options   uniai.ImageOptions
	editCount int
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "imagetest: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	fs := flag.NewFlagSet("imagetest", flag.ExitOnError)
	provider := fs.String("provider", "all", "provider to run: openai, gemini, or all")
	mode := fs.String("mode", "all", "mode to run: generate, edit, or all")
	outputDir := fs.String("out", "cmd/imagetest/out", "directory for generated images")
	sample1 := fs.String("sample1", "cmd/imagetest/sample-1.jpg", "first target image for edit")
	sample2 := fs.String("sample2", "cmd/imagetest/sample-2.jpg", "second target image for edit")
	sample3 := fs.String("sample3", "cmd/imagetest/sample-3.png", "style reference image for edit")
	openAIModel := fs.String("openai-model", "gpt-image-2", "OpenAI image model")
	geminiModel := fs.String("gemini-model", "gemini-3.1-flash-image-preview", "Gemini image model")
	prompt := fs.String("prompt", "A compact desk lamp on a plain background, product photo, soft natural light", "generation prompt")
	editPrompt := fs.String("edit-prompt", "Input order: image 1 is sample-1, image 2 is sample-2, and image 3 is sample-3. Understand the visual style of image 3, then redraw the subjects from image 1 and image 2 in that same style. Return one image containing the two redrawn results side by side.", "edit prompt")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}

	if err := os.MkdirAll(*outputDir, 0o755); err != nil {
		return err
	}

	providers, err := selectedProviders(*provider, *openAIModel, *geminiModel)
	if err != nil {
		return err
	}
	if len(providers) == 0 {
		return fmt.Errorf("no runnable provider selected; set OPENAI_API_KEY or GEMINI_API_KEY")
	}

	runGenerate := *mode == "all" || *mode == "generate"
	runEdit := *mode == "all" || *mode == "edit"
	if !runGenerate && !runEdit {
		return fmt.Errorf("mode must be generate, edit, or all")
	}

	var editImages []uniai.InputImage
	if runEdit {
		editImages, err = readSampleImages(*sample1, *sample2, *sample3)
		if err != nil {
			if *mode == "edit" {
				return err
			}
			fmt.Printf("edit skipped: %v\n", err)
			runEdit = false
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	for _, cfg := range providers {
		clientConfig := uniai.Config{}
		switch cfg.name {
		case "openai":
			clientConfig.OpenAIAPIKey = cfg.apiKey
		case "gemini":
			clientConfig.GeminiAPIKey = cfg.apiKey
		}
		client := uniai.New(clientConfig)

		if runGenerate {
			fmt.Printf("generate: provider=%s model=%s\n", cfg.name, cfg.model)
			result, err := client.Image(ctx,
				uniai.Image(cfg.model, *prompt),
				uniai.WithImageProvider(cfg.name),
				uniai.WithCount(1),
				uniai.WithImageOptions(cfg.options),
			)
			if err != nil {
				return fmt.Errorf("%s generate: %w", cfg.name, err)
			}
			if err := writeResultImages(*outputDir, cfg.name+"-generate", result); err != nil {
				return err
			}
			printUsage(cfg.name+" generate", result)
		}

		if runEdit {
			fmt.Printf("edit: provider=%s model=%s\n", cfg.name, cfg.model)
			result, err := client.EditImage(ctx,
				uniai.ImageEdit(cfg.model, *editPrompt, editImages...),
				uniai.WithImageEditProvider(cfg.name),
				uniai.WithImageEditCount(cfg.editCount),
				uniai.WithImageEditOptions(cfg.options),
			)
			if err != nil {
				return fmt.Errorf("%s edit: %w", cfg.name, err)
			}
			if err := writeResultImages(*outputDir, cfg.name+"-edit", result); err != nil {
				return err
			}
			printUsage(cfg.name+" edit", result)
		}
	}

	return nil
}

func selectedProviders(provider, openAIModel, geminiModel string) ([]providerConfig, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		provider = "all"
	}
	if provider != "all" && provider != "openai" && provider != "gemini" {
		return nil, fmt.Errorf("provider must be openai, gemini, or all")
	}

	var out []providerConfig
	openAIKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if (provider == "all" || provider == "openai") && openAIKey != "" {
		out = append(out, providerConfig{
			name:      "openai",
			model:     openAIModel,
			apiKey:    openAIKey,
			editCount: 1,
			options: uniai.ImageOptions{OpenAI: structs.JSONMap{
				"size":          "1024x1024",
				"quality":       "auto",
				"output_format": "jpeg",
				"background":    "auto",
				"moderation":    "auto",
			}},
		})
	}
	geminiKey := strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))
	if (provider == "all" || provider == "gemini") && geminiKey != "" {
		out = append(out, providerConfig{
			name:      "gemini",
			model:     geminiModel,
			apiKey:    geminiKey,
			editCount: 1,
			options: uniai.ImageOptions{Gemini: structs.JSONMap{
				"aspect_ratio":        "1:1",
				"response_modalities": []string{"TEXT", "IMAGE"},
				"image_size":          "1K",
			}},
		})
	}
	if provider != "all" && len(out) == 0 {
		switch provider {
		case "openai":
			return nil, fmt.Errorf("missing required env OPENAI_API_KEY")
		case "gemini":
			return nil, fmt.Errorf("missing required env GEMINI_API_KEY")
		}
	}
	return out, nil
}

func readSampleImages(paths ...string) ([]uniai.InputImage, error) {
	out := make([]uniai.InputImage, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read sample image %s: %w", path, err)
		}
		out = append(out, uniai.InputImage{
			Filename: filepath.Base(path),
			MIMEType: detectMIMEType(path, data),
			Data:     data,
		})
	}
	return out, nil
}

func writeResultImages(outputDir, prefix string, result *uniai.ImageResult) error {
	if result == nil {
		return fmt.Errorf("%s: nil image result", prefix)
	}
	if strings.TrimSpace(result.Text) != "" {
		fmt.Printf("%s text: %s\n", prefix, result.Text)
	}
	written := 0
	for i, item := range result.Images {
		data, mimeType, err := decodeImageAsset(item)
		if err != nil {
			if item.URL != "" {
				fmt.Printf("%s image %d url: %s\n", prefix, i+1, item.URL)
				continue
			}
			return fmt.Errorf("%s image %d: %w", prefix, i+1, err)
		}
		name := fmt.Sprintf("%s-%d%s", prefix, i+1, extensionForMIMEType(mimeType))
		path := filepath.Join(outputDir, name)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return err
		}
		written++
		fmt.Printf("wrote %s (%s, %d bytes)\n", path, mimeType, len(data))
	}
	if written == 0 && len(result.Images) == 0 {
		return fmt.Errorf("%s: no images returned", prefix)
	}
	return nil
}

func decodeImageAsset(item uniai.ImageAsset) ([]byte, string, error) {
	dataBase64 := strings.TrimSpace(item.DataBase64)
	if dataBase64 == "" {
		return nil, "", fmt.Errorf("image has no inline base64 data")
	}
	if idx := strings.Index(dataBase64, ","); idx >= 0 && strings.HasPrefix(strings.ToLower(dataBase64[:idx]), "data:") {
		dataBase64 = strings.TrimSpace(dataBase64[idx+1:])
	}
	data, err := base64.StdEncoding.DecodeString(dataBase64)
	if err != nil {
		return nil, "", err
	}
	mimeType := strings.TrimSpace(item.MIMEType)
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	return data, mimeType, nil
}

func detectMIMEType(path string, data []byte) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	default:
		return http.DetectContentType(data)
	}
}

func extensionForMIMEType(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	default:
		return ".img"
	}
}

func printUsage(label string, result *uniai.ImageResult) {
	if result == nil {
		return
	}
	usage := result.Usage
	fmt.Printf("%s usage: input=%d text=%d image=%d output=%d total=%d\n",
		label,
		usage.InputTokens,
		usage.InputTextTokens,
		usage.InputImageTokens,
		usage.OutputTokens,
		usage.TotalTokens,
	)
	if usage.Cost != nil {
		fmt.Printf("%s cost: total=$%.8f input=$%.8f output=$%.8f\n",
			label,
			usage.Cost.Total,
			usage.Cost.Input,
			usage.Cost.Output,
		)
	}
}
