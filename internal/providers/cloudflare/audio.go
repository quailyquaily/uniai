package cloudflare

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/lyricat/goutils/structs"
)

type audioSegment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text,omitempty"`
}

type audioResult struct {
	Text     string         `json:"text,omitempty"`
	Language string         `json:"language,omitempty"`
	Segments []audioSegment `json:"segments,omitempty"`
	Raw      any            `json:"raw,omitempty"`
}

func Transcribe(ctx context.Context, token, base, accountID, model, audio string, options structs.JSONMap) ([]byte, error) {
	payload := structs.NewJSONMap()
	payload.Merge(options)
	if audio != "" && !payload.HasKey("audio") {
		payload["audio"] = audio
	}
	if !payload.HasKey("audio") {
		return nil, fmt.Errorf("audio is required")
	}

	resultRaw, err := RunJSON(ctx, token, base, accountID, model, payload)
	if err != nil {
		return nil, err
	}

	out := audioResult{}
	var raw any
	if err := json.Unmarshal(resultRaw, &raw); err != nil {
		return nil, err
	}
	out.Raw = raw
	if m, ok := raw.(map[string]any); ok {
		if text := toString(m["text"]); text != "" {
			out.Text = text
		}
		if lang := toString(m["language"]); lang != "" {
			out.Language = lang
		}
		if segments, ok := m["segments"]; ok {
			out.Segments = parseSegments(segments)
		}
	} else if text, ok := raw.(string); ok {
		out.Text = text
	}

	return json.Marshal(out)
}

func parseSegments(val any) []audioSegment {
	arr, ok := val.([]any)
	if !ok {
		return nil
	}
	out := make([]audioSegment, 0, len(arr))
	for _, item := range arr {
		seg, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, audioSegment{
			Start: toFloat64(seg["start"]),
			End:   toFloat64(seg["end"]),
			Text:  toString(seg["text"]),
		})
	}
	return out
}

func toFloat64(val any) float64 {
	switch v := val.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	}
	return 0
}
