package audio

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/quailyquaily/uniai/internal/providers/cloudflare"
	"github.com/quailyquaily/uniai/internal/providers/openai"
)

// ErrUnsupportedAudioFormat is returned when the input format cannot be determined.
var ErrUnsupportedAudioFormat = openai.ErrUnsupportedAudioFormat

type Config struct {
	OpenAIAPIKey        string
	OpenAIAPIBase       string
	CloudflareAccountID string
	CloudflareAPIToken  string
	CloudflareAPIBase   string
}

type Client struct {
	cfg Config
}

func New(cfg Config) *Client {
	return &Client{cfg: cfg}
}

func (c *Client) Create(ctx context.Context, opts ...Option) (*Result, error) {
	req := BuildRequest(opts...)
	provider := req.Provider
	if provider == "" {
		provider = pickProviderByModel(req.Model)
	}
	if provider == "" {
		return nil, fmt.Errorf("provider not set")
	}

	var (
		respData []byte
		err      error
	)
	switch provider {
	case "openai":
		input := openai.InputAudio{
			Filename: req.Filename,
			MIMEType: req.MIMEType,
		}
		if len(req.Data) == 0 && req.Reader != nil {
			input.Reader = req.Reader
		} else {
			data, decodeErr := requestAudioBytes(req)
			if decodeErr != nil {
				return nil, decodeErr
			}
			input.Data = data
		}
		respData, err = openai.Transcribe(ctx, c.cfg.OpenAIAPIKey, c.cfg.OpenAIAPIBase, req.Model, input, req.Options.OpenAI)
	case "cloudflare":
		audio := req.Audio
		data := req.Data
		if len(data) == 0 && req.Reader != nil {
			if data, err = io.ReadAll(req.Reader); err != nil {
				return nil, fmt.Errorf("read audio: %w", err)
			}
		}
		if len(data) > 0 {
			audio = base64.StdEncoding.EncodeToString(data)
		}
		respData, err = cloudflare.Transcribe(ctx, c.cfg.CloudflareAPIToken, c.cfg.CloudflareAPIBase, c.cfg.CloudflareAccountID, req.Model, audio, req.Options.Cloudflare)
	default:
		return nil, fmt.Errorf("unknown provider: %s", provider)
	}
	if err != nil {
		return nil, err
	}

	var out Result
	if err := json.Unmarshal(respData, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func requestAudioBytes(req *Request) ([]byte, error) {
	if len(req.Data) > 0 {
		return req.Data, nil
	}
	if req.Audio == "" {
		return nil, fmt.Errorf("audio is required")
	}
	data, err := decodeBase64Audio(req.Audio)
	if err != nil {
		return nil, fmt.Errorf("decode base64 audio: %w", err)
	}
	return data, nil
}

// decodeBase64Audio accepts standard or URL-safe base64, padded or not, and
// strips a data URL prefix ("data:audio/ogg;base64,...").
func decodeBase64Audio(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "data:") {
		if idx := strings.Index(s, ","); idx >= 0 {
			s = s[idx+1:]
		}
	}
	encodings := []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding}
	var lastErr error
	for _, enc := range encodings {
		data, err := enc.DecodeString(s)
		if err == nil {
			return data, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func pickProviderByModel(model string) string {
	if strings.HasPrefix(model, "@cf/") {
		return "cloudflare"
	}
	return "openai"
}
