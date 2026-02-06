package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"strconv"
	"strings"

	"github.com/lyricat/goutils/structs"
)

type createImagesOutput struct {
	Created int `json:"created"`
	Data    []struct {
		B64JSON string `json:"b64_json"`
	} `json:"data"`
	MimeType string           `json:"mime_type"`
	Usage    createImageUsage `json:"usage"`
}

type createImageUsage struct {
	Size    string `json:"size"`
	Quality string `json:"quality"`

	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

func CreateImages(ctx context.Context, token, base, accountID, model, prompt string, count int, options structs.JSONMap) ([]byte, error) {
	payload := structs.NewJSONMap()
	payload.Merge(options)

	if !payload.HasKey("prompt") && prompt != "" {
		payload["prompt"] = prompt
	}
	if count > 0 && !payload.HasKey("num_images") {
		payload["num_images"] = count
	}

	useMultipart := shouldUseMultipart(model, payload)
	contentType := "application/json"
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if useMultipart {
		contentType, body, err = buildMultipartBody(payload)
		if err != nil {
			return nil, err
		}
	}

	resultRaw, err := Run(ctx, token, base, accountID, model, contentType, body)
	if err != nil {
		return nil, err
	}

	images, mimeType := extractImages(resultRaw)
	if len(images) == 0 {
		return nil, fmt.Errorf("cloudflare image response missing image data")
	}

	out := &createImagesOutput{
		Created: 0,
		Data: make([]struct {
			B64JSON string `json:"b64_json"`
		}, 0, len(images)),
		MimeType: mimeType,
	}
	for _, img := range images {
		out.Data = append(out.Data, struct {
			B64JSON string `json:"b64_json"`
		}{B64JSON: img})
	}

	return json.Marshal(out)
}

func shouldUseMultipart(model string, options structs.JSONMap) bool {
	if options.GetBool("multipart") {
		return true
	}
	model = strings.ToLower(model)
	return strings.Contains(model, "flux-2-")
}

func buildMultipartBody(payload structs.JSONMap) (string, []byte, error) {
	buf := &bytes.Buffer{}
	writer := multipart.NewWriter(buf)
	for key, value := range payload {
		if key == "multipart" {
			continue
		}
		if err := addMultipartValue(writer, key, value); err != nil {
			_ = writer.Close()
			return "", nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return "", nil, err
	}
	return writer.FormDataContentType(), buf.Bytes(), nil
}

func addMultipartValue(writer *multipart.Writer, key string, value any) error {
	if value == nil {
		return nil
	}
	switch v := value.(type) {
	case []byte:
		part, err := writer.CreateFormFile(key, key)
		if err != nil {
			return err
		}
		_, err = part.Write(v)
		return err
	case io.Reader:
		part, err := writer.CreateFormFile(key, key)
		if err != nil {
			return err
		}
		_, err = io.Copy(part, v)
		return err
	case string:
		return writer.WriteField(key, v)
	case fmt.Stringer:
		return writer.WriteField(key, v.String())
	case float64:
		return writer.WriteField(key, strconv.FormatFloat(v, 'f', -1, 64))
	case float32:
		return writer.WriteField(key, strconv.FormatFloat(float64(v), 'f', -1, 32))
	case int:
		return writer.WriteField(key, strconv.Itoa(v))
	case int64:
		return writer.WriteField(key, strconv.FormatInt(v, 10))
	case bool:
		return writer.WriteField(key, strconv.FormatBool(v))
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return err
		}
		return writer.WriteField(key, string(data))
	}
}

func extractImages(resultRaw []byte) ([]string, string) {
	var raw any
	if err := json.Unmarshal(resultRaw, &raw); err != nil {
		return nil, ""
	}
	images := parseImages(raw)
	mimeType := ""
	if m, ok := raw.(map[string]any); ok {
		mimeType = toString(m["mime_type"])
		if mimeType == "" {
			mimeType = toString(m["content_type"])
		}
	}
	return images, mimeType
}

func parseImages(raw any) []string {
	switch val := raw.(type) {
	case map[string]any:
		if img, ok := val["image"]; ok {
			if s := toString(img); s != "" {
				return []string{s}
			}
		}
		if imgs, ok := val["images"]; ok {
			return toStringSlice(imgs)
		}
		if data, ok := val["data"]; ok {
			return parseImages(data)
		}
	case []any:
		if len(val) == 0 {
			return nil
		}
		if _, ok := val[0].(string); ok {
			return toStringSlice(val)
		}
		out := make([]string, 0, len(val))
		for _, item := range val {
			if m, ok := item.(map[string]any); ok {
				if s := toString(m["image"]); s != "" {
					out = append(out, s)
				}
				if s := toString(m["b64_json"]); s != "" {
					out = append(out, s)
				}
			}
		}
		return out
	}
	return nil
}

func toStringSlice(val any) []string {
	switch v := val.(type) {
	case []string:
		return append([]string{}, v...)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s := toString(item); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func toString(val any) string {
	switch v := val.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	}
	return ""
}
