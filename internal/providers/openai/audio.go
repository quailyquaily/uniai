package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/lyricat/goutils/structs"
)

// ErrUnsupportedAudioFormat is returned when the audio format cannot be
// determined from the filename, MIME type, or content.
var ErrUnsupportedAudioFormat = errors.New("unsupported audio format")

// maxOpenAIAudioBytes is the upload cap of api.openai.com.
const maxOpenAIAudioBytes = 25 << 20

// audioSniffLen is how much of a stream is buffered to detect its format.
const audioSniffLen = 512

var errAudioTooLarge = errors.New("audio exceeds the 25 MB OpenAI upload limit")

type InputAudio struct {
	Filename string
	MIMEType string
	Data     []byte
	// Reader streams the audio when Data is empty, so the upload is never
	// held in memory.
	Reader io.Reader
}

type audioSegment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text,omitempty"`
}

type audioUsage struct {
	Type             string  `json:"type,omitempty"`
	InputTokens      int     `json:"input_tokens,omitempty"`
	InputTextTokens  int     `json:"input_text_tokens,omitempty"`
	InputAudioTokens int     `json:"input_audio_tokens,omitempty"`
	OutputTokens     int     `json:"output_tokens,omitempty"`
	TotalTokens      int     `json:"total_tokens,omitempty"`
	Seconds          float64 `json:"seconds,omitempty"`
}

type audioResult struct {
	Text     string         `json:"text,omitempty"`
	Language string         `json:"language,omitempty"`
	Duration float64        `json:"duration,omitempty"`
	Segments []audioSegment `json:"segments,omitempty"`
	Usage    *audioUsage    `json:"usage,omitempty"`
	Raw      any            `json:"raw,omitempty"`
}

type openAITranscriptionOutput struct {
	Text     string         `json:"text"`
	Language string         `json:"language"`
	Duration float64        `json:"duration"`
	Segments []audioSegment `json:"segments"`
	Usage    *struct {
		Type              string `json:"type"`
		InputTokens       int    `json:"input_tokens"`
		InputTokenDetails struct {
			TextTokens  int `json:"text_tokens"`
			AudioTokens int `json:"audio_tokens"`
		} `json:"input_token_details"`
		OutputTokens int     `json:"output_tokens"`
		TotalTokens  int     `json:"total_tokens"`
		Seconds      float64 `json:"seconds"`
	} `json:"usage"`
}

// audioExtensions maps MIME types to the file extensions OpenAI uses to
// detect the input format.
var audioExtensions = map[string]string{
	"audio/flac":      "flac",
	"audio/x-flac":    "flac",
	"audio/mpeg":      "mp3",
	"audio/mp3":       "mp3",
	"audio/mpga":      "mpga",
	"audio/mp4":       "m4a",
	"audio/m4a":       "m4a",
	"audio/x-m4a":     "m4a",
	"audio/aac":       "m4a",
	"video/mp4":       "mp4",
	"audio/ogg":       "ogg",
	"audio/opus":      "ogg",
	"application/ogg": "ogg",
	"audio/wav":       "wav",
	"audio/wave":      "wav",
	"audio/x-wav":     "wav",
	"audio/vnd.wave":  "wav",
	"audio/webm":      "webm",
	"video/webm":      "webm",
}

func Transcribe(ctx context.Context, token, base, model string, input InputAudio, options structs.JSONMap) ([]byte, error) {
	if strings.TrimSpace(model) == "" {
		return nil, fmt.Errorf("model is required")
	}

	var (
		respData []byte
		err      error
	)
	if len(input.Data) == 0 && input.Reader != nil {
		respData, err = transcribeStream(ctx, token, base, model, input, options)
	} else {
		if len(input.Data) == 0 {
			return nil, fmt.Errorf("audio is required")
		}
		if isOfficialOpenAIBase(base) && len(input.Data) > maxOpenAIAudioBytes {
			return nil, errAudioTooLarge
		}
		var (
			contentType string
			body        []byte
		)
		contentType, body, err = buildOpenAITranscriptionMultipart(model, input, options)
		if err != nil {
			return nil, err
		}
		respData, err = doRequestWithContentType(ctx, token, base, "POST", "/audio/transcriptions", contentType, body)
	}
	if err != nil {
		return nil, err
	}

	out, err := mapOpenAITranscriptionOutput(respData)
	if err != nil {
		return nil, err
	}
	return json.Marshal(out)
}

// transcribeStream pipes the multipart body to the request as the audio is
// read. The first audioSniffLen bytes are buffered to detect the format.
func transcribeStream(ctx context.Context, token, base, model string, input InputAudio, options structs.JSONMap) ([]byte, error) {
	src := bufio.NewReaderSize(input.Reader, audioSniffLen)
	head, err := src.Peek(audioSniffLen)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("read audio: %w", err)
	}
	if len(head) == 0 {
		return nil, fmt.Errorf("audio is required")
	}
	filename, mimeType, err := resolveAudioFile(input.Filename, input.MIMEType, head)
	if err != nil {
		return nil, err
	}
	var audio io.Reader = src
	if isOfficialOpenAIBase(base) {
		audio = &maxBytesReader{r: src, remaining: maxOpenAIAudioBytes}
	}

	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)
	go func() {
		pw.CloseWithError(writeOpenAITranscriptionMultipart(writer, model, filename, mimeType, audio, options))
	}()
	respData, err := doRequestWithBody(ctx, token, base, "POST", "/audio/transcriptions", writer.FormDataContentType(), pr)
	// Unblock the writer if the request ended before the body was consumed.
	_ = pr.Close()
	return respData, err
}

type maxBytesReader struct {
	r         io.Reader
	remaining int64
}

func (m *maxBytesReader) Read(p []byte) (int, error) {
	n, err := m.r.Read(p)
	m.remaining -= int64(n)
	if m.remaining < 0 {
		return n, errAudioTooLarge
	}
	return n, err
}

func buildOpenAITranscriptionMultipart(model string, input InputAudio, options structs.JSONMap) (string, []byte, error) {
	filename, mimeType, err := resolveAudioFile(input.Filename, input.MIMEType, input.Data)
	if err != nil {
		return "", nil, err
	}

	buf := &bytes.Buffer{}
	writer := multipart.NewWriter(buf)
	if err := writeOpenAITranscriptionMultipart(writer, model, filename, mimeType, bytes.NewReader(input.Data), options); err != nil {
		return "", nil, err
	}
	return writer.FormDataContentType(), buf.Bytes(), nil
}

// writeOpenAITranscriptionMultipart writes the form and closes writer.
func writeOpenAITranscriptionMultipart(writer *multipart.Writer, model, filename, mimeType string, audio io.Reader, options structs.JSONMap) error {
	if err := writer.WriteField("model", model); err != nil {
		_ = writer.Close()
		return err
	}
	keys := make([]string, 0, len(options))
	for key := range options {
		if key == "model" || key == "file" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := writeAudioOptionField(writer, key, options[key]); err != nil {
			_ = writer.Close()
			return err
		}
	}

	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, escapeMultipartFilename(filename)))
	header.Set("Content-Type", mimeType)
	part, err := writer.CreatePart(header)
	if err != nil {
		_ = writer.Close()
		return err
	}
	if _, err := io.Copy(part, audio); err != nil {
		_ = writer.Close()
		return err
	}
	return writer.Close()
}

// writeAudioOptionField writes one option as form fields. Arrays use the
// `key[]` convention (e.g. timestamp_granularities[]), objects are sent as JSON.
func writeAudioOptionField(writer *multipart.Writer, key string, value any) error {
	if value == nil {
		return nil
	}
	switch v := value.(type) {
	case []any:
		for _, item := range v {
			if err := writer.WriteField(arrayFieldName(key), formValue(item)); err != nil {
				return err
			}
		}
		return nil
	case []string:
		for _, item := range v {
			if err := writer.WriteField(arrayFieldName(key), item); err != nil {
				return err
			}
		}
		return nil
	}
	return writer.WriteField(key, formValue(value))
}

func arrayFieldName(key string) string {
	if strings.HasSuffix(key, "[]") {
		return key
	}
	return key + "[]"
}

func formValue(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(v), 'f', -1, 32)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case fmt.Stringer:
		return v.String()
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(data)
}

// resolveAudioFile returns the multipart filename and content type. An explicit
// filename is trusted; otherwise the extension comes from the MIME type or,
// failing that, from sniffing head (the start of the content).
func resolveAudioFile(filename, mimeType string, head []byte) (string, string, error) {
	mimeType = normalizeAudioMIMEType(mimeType)
	filename = strings.TrimSpace(filename)
	// CDNs often label media as octet-stream; treat it as unknown.
	if mimeType == "application/octet-stream" {
		mimeType = ""
	}

	if mimeType == "" && filename != "" {
		mimeType = normalizeAudioMIMEType(mime.TypeByExtension(path.Ext(filename)))
	}
	if mimeType == "" {
		sniffed := normalizeAudioMIMEType(http.DetectContentType(head))
		if _, ok := audioExtensions[sniffed]; ok {
			mimeType = sniffed
		}
	}
	if filename != "" {
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		return filename, mimeType, nil
	}

	ext, ok := audioExtensions[mimeType]
	if !ok {
		return "", "", fmt.Errorf("%w: cannot determine format (mime type %q); pass a filename or a supported mime type", ErrUnsupportedAudioFormat, mimeType)
	}
	return "audio." + ext, mimeType, nil
}

func normalizeAudioMIMEType(mimeType string) string {
	mimeType = strings.TrimSpace(mimeType)
	if mimeType == "" {
		return ""
	}
	if parsed, _, err := mime.ParseMediaType(mimeType); err == nil {
		mimeType = parsed
	}
	return strings.ToLower(mimeType)
}

func isOfficialOpenAIBase(base string) bool {
	return normalizeBase(base) == openAIAPIV1Base
}

func mapOpenAITranscriptionOutput(respData []byte) (*audioResult, error) {
	trimmed := bytes.TrimSpace(respData)
	// text, srt, and vtt response formats return plain text.
	if len(trimmed) == 0 || trimmed[0] != '{' {
		text := string(trimmed)
		return &audioResult{Text: text, Raw: text}, nil
	}

	var resp openAITranscriptionOutput
	if err := json.Unmarshal(trimmed, &resp); err != nil {
		return nil, err
	}
	var raw any
	if err := json.Unmarshal(trimmed, &raw); err != nil {
		return nil, err
	}

	out := &audioResult{
		Text:     resp.Text,
		Language: resp.Language,
		Duration: resp.Duration,
		Segments: resp.Segments,
		Raw:      raw,
	}
	if resp.Usage != nil {
		out.Usage = &audioUsage{
			Type:             resp.Usage.Type,
			InputTokens:      resp.Usage.InputTokens,
			InputTextTokens:  resp.Usage.InputTokenDetails.TextTokens,
			InputAudioTokens: resp.Usage.InputTokenDetails.AudioTokens,
			OutputTokens:     resp.Usage.OutputTokens,
			TotalTokens:      resp.Usage.TotalTokens,
			Seconds:          resp.Usage.Seconds,
		}
	}
	return out, nil
}
