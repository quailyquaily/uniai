package image

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"strings"

	"github.com/lyricat/goutils/structs"
	"github.com/quailyquaily/uniai/chat"
)

type Options struct {
	OpenAI     structs.JSONMap `json:"openai_options,omitempty"`
	Gemini     structs.JSONMap `json:"gemini_options,omitempty"`
	Cloudflare structs.JSONMap `json:"cloudflare_options,omitempty"`
}

type Request struct {
	Provider string  `json:"provider,omitempty"`
	Model    string  `json:"model,omitempty"`
	Prompt   string  `json:"prompt,omitempty"`
	Count    int     `json:"count,omitempty"`
	Options  Options `json:"options,omitempty"`
}

type InputImage struct {
	Filename string `json:"filename,omitempty"`
	MIMEType string `json:"mime_type,omitempty"`
	Data     []byte `json:"-"`
}

type EditRequest struct {
	Provider string       `json:"provider,omitempty"`
	Model    string       `json:"model,omitempty"`
	Prompt   string       `json:"prompt,omitempty"`
	Images   []InputImage `json:"-"`
	Count    int          `json:"count,omitempty"`
	Options  Options      `json:"options,omitempty"`
}

type Result struct {
	Created int          `json:"created"`
	Images  []ImageAsset `json:"images,omitempty"`
	Text    string       `json:"text,omitempty"`

	// Deprecated compatibility fields. Prefer Images.
	Data     []ImageData      `json:"data,omitempty"`
	MimeType string           `json:"mime_type,omitempty"`
	Usage    CreateImageUsage `json:"usage"`
	Raw      json.RawMessage  `json:"-"`
}

type ImageAsset struct {
	DataBase64    string `json:"data_base64,omitempty"`
	URL           string `json:"url,omitempty"`
	MIMEType      string `json:"mime_type,omitempty"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

type ImageData struct {
	B64JSON string `json:"b64_json"`
}

type CreateImageUsage struct {
	Size    string `json:"size"`
	Quality string `json:"quality"`

	InputTokens       int `json:"input_tokens"`
	InputTextTokens   int `json:"input_text_tokens,omitempty"`
	InputImageTokens  int `json:"input_image_tokens,omitempty"`
	CachedTextTokens  int `json:"cached_text_tokens,omitempty"`
	CachedImageTokens int `json:"cached_image_tokens,omitempty"`
	OutputTokens      int `json:"output_tokens"`
	TotalTokens       int `json:"total_tokens"`

	Cost *chat.UsageCost `json:"cost,omitempty"`
}

type Option func(*Request)

type ImageEditOption func(*EditRequest)

func BuildRequest(opts ...Option) *Request {
	req := &Request{}
	for _, opt := range opts {
		if opt != nil {
			opt(req)
		}
	}
	req.Model = NormalizeModelAlias(req.Model)
	return req
}

func BuildEditRequest(opts ...ImageEditOption) *EditRequest {
	req := &EditRequest{}
	for _, opt := range opts {
		if opt != nil {
			opt(req)
		}
	}
	req.Model = NormalizeModelAlias(req.Model)
	return req
}

func Image(model, prompt string) Option {
	return func(r *Request) {
		r.Model = model
		r.Prompt = prompt
	}
}

func WithProvider(provider string) Option {
	return func(r *Request) { r.Provider = provider }
}

func WithCount(count int) Option {
	return func(r *Request) { r.Count = count }
}

func WithOptions(opts Options) Option {
	return func(r *Request) { r.Options = opts }
}

func ImageEdit(model, prompt string, images ...InputImage) ImageEditOption {
	return func(r *EditRequest) {
		r.Model = model
		r.Prompt = prompt
		r.Images = append([]InputImage{}, images...)
	}
}

func WithEditProvider(provider string) ImageEditOption {
	return func(r *EditRequest) { r.Provider = provider }
}

func WithEditCount(count int) ImageEditOption {
	return func(r *EditRequest) { r.Count = count }
}

func WithEditOptions(opts Options) ImageEditOption {
	return func(r *EditRequest) { r.Options = opts }
}

func NormalizeModelAlias(model string) string {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "nano-banana-pro":
		return "gemini-3-pro-image-preview"
	case "nano-banana-2":
		return "gemini-3.1-flash-image-preview"
	default:
		return strings.TrimSpace(model)
	}
}

func (a ImageAsset) AsInputImage() (InputImage, error) {
	dataBase64 := strings.TrimSpace(a.DataBase64)
	if dataBase64 == "" {
		return InputImage{}, fmt.Errorf("image asset has no inline base64 data")
	}
	data, err := base64.StdEncoding.DecodeString(stripDataURLPrefix(dataBase64))
	if err != nil {
		return InputImage{}, err
	}
	mimeType := strings.TrimSpace(a.MIMEType)
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	filename := defaultFilenameForMIMEType(mimeType)
	return InputImage{
		Filename: filename,
		MIMEType: mimeType,
		Data:     data,
	}, nil
}

func NormalizeResult(result *Result) {
	if result == nil {
		return
	}
	if len(result.Images) == 0 && len(result.Data) > 0 {
		result.Images = make([]ImageAsset, 0, len(result.Data))
		for _, item := range result.Data {
			if strings.TrimSpace(item.B64JSON) == "" {
				continue
			}
			result.Images = append(result.Images, ImageAsset{
				DataBase64: item.B64JSON,
				MIMEType:   result.MimeType,
			})
		}
	}
	if len(result.Data) == 0 && len(result.Images) > 0 {
		for _, item := range result.Images {
			if strings.TrimSpace(item.DataBase64) == "" {
				continue
			}
			result.Data = append(result.Data, ImageData{B64JSON: item.DataBase64})
		}
	}
	if result.MimeType == "" && len(result.Images) > 0 {
		result.MimeType = commonMIMEType(result.Images)
	}
}

func stripDataURLPrefix(data string) string {
	if idx := strings.Index(data, ","); idx >= 0 && strings.HasPrefix(strings.ToLower(data[:idx]), "data:") {
		return strings.TrimSpace(data[idx+1:])
	}
	return data
}

func defaultFilenameForMIMEType(mimeType string) string {
	exts, err := mime.ExtensionsByType(mimeType)
	if err == nil && len(exts) > 0 {
		return "image" + exts[0]
	}
	return "image"
}

func commonMIMEType(images []ImageAsset) string {
	common := ""
	for _, item := range images {
		mimeType := strings.TrimSpace(item.MIMEType)
		if mimeType == "" {
			return ""
		}
		if common == "" {
			common = mimeType
			continue
		}
		if common != mimeType {
			return ""
		}
	}
	return common
}
