package openai

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/openai/openai-go/v3/option"
	"github.com/quailyquaily/uniai/internal/diag"
)

const maxStreamRawErrorBytes = 64 * 1024

type streamRawErrorDebug struct {
	enabled      bool
	debugFn      func(label, payload string)
	label        string
	attempts     int
	request      *streamRawRequestDebug
	response     *streamRawResponseDebug
	body         *streamRawBodyCapture
	transportErr string
}

type streamRawErrorPayload struct {
	Error        string                  `json:"error"`
	Attempts     int                     `json:"attempts"`
	TransportErr string                  `json:"transport_error,omitempty"`
	Request      *streamRawRequestDebug  `json:"request,omitempty"`
	Response     *streamRawResponseDebug `json:"response,omitempty"`
	Body         *streamRawBodyDebug     `json:"body,omitempty"`
}

type streamRawRequestDebug struct {
	Method        string              `json:"method"`
	URL           string              `json:"url"`
	Host          string              `json:"host,omitempty"`
	Path          string              `json:"path,omitempty"`
	QueryPresent  bool                `json:"query_present,omitempty"`
	Header        map[string][]string `json:"header,omitempty"`
	ContentLength int64               `json:"content_length"`
}

type streamRawResponseDebug struct {
	Status           string              `json:"status"`
	StatusCode       int                 `json:"status_code"`
	Header           map[string][]string `json:"header,omitempty"`
	ContentLength    int64               `json:"content_length"`
	TransferEncoding []string            `json:"transfer_encoding,omitempty"`
}

type streamRawBodyDebug struct {
	BytesRead     int64  `json:"bytes_read"`
	CapturedBytes int    `json:"captured_bytes"`
	Truncated     bool   `json:"truncated"`
	ReadError     string `json:"read_error,omitempty"`
	Preview       string `json:"preview,omitempty"`
	PreviewBase64 string `json:"preview_base64,omitempty"`
}

func newStreamRawErrorDebug(enabled bool, debugFn func(label, payload string), label string) *streamRawErrorDebug {
	if !enabled && debugFn == nil {
		return nil
	}
	return &streamRawErrorDebug{
		enabled: enabled,
		debugFn: debugFn,
		label:   strings.TrimSpace(label),
	}
}

func (d *streamRawErrorDebug) Option() option.RequestOption {
	if d == nil {
		return nil
	}
	return option.WithMiddleware(func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		d.attempts++
		d.request = summarizeStreamRawRequest(req)
		d.response = nil
		d.body = nil
		d.transportErr = ""

		resp, err := next(req)
		if err != nil {
			d.transportErr = err.Error()
			return resp, err
		}
		d.response = summarizeStreamRawResponse(resp)
		if resp != nil && resp.Body != nil {
			body := &streamRawBodyCapture{maxBytes: maxStreamRawErrorBytes}
			d.body = body
			resp.Body = &streamRawBodyReadCloser{
				body:    resp.Body,
				capture: body,
			}
		}
		return resp, nil
	})
}

func (d *streamRawErrorDebug) Emit(err error) {
	if d == nil || err == nil {
		return
	}
	payload := streamRawErrorPayload{
		Error:        err.Error(),
		Attempts:     d.attempts,
		TransportErr: d.transportErr,
		Request:      d.request,
		Response:     d.response,
	}
	if d.body != nil {
		payload.Body = d.body.Snapshot()
	}
	label := d.label
	if label == "" {
		label = "openai.chat.stream.raw_error"
	}
	diag.LogJSON(d.enabled, d.debugFn, label, payload)
}

type streamRawBodyCapture struct {
	maxBytes int
	total    int64
	data     []byte
	readErr  string
}

func (c *streamRawBodyCapture) Write(data []byte) {
	if c == nil || len(data) == 0 {
		return
	}
	c.total += int64(len(data))
	if c.maxBytes <= 0 || len(c.data) >= c.maxBytes {
		return
	}
	remaining := c.maxBytes - len(c.data)
	if remaining > len(data) {
		remaining = len(data)
	}
	c.data = append(c.data, data[:remaining]...)
}

func (c *streamRawBodyCapture) SetReadError(err error) {
	if c == nil || err == nil || err == io.EOF {
		return
	}
	c.readErr = err.Error()
}

func (c *streamRawBodyCapture) Snapshot() *streamRawBodyDebug {
	if c == nil {
		return nil
	}
	out := &streamRawBodyDebug{
		BytesRead:     c.total,
		CapturedBytes: len(c.data),
		Truncated:     c.total > int64(len(c.data)),
		ReadError:     c.readErr,
	}
	if len(c.data) > 0 {
		out.PreviewBase64 = base64.StdEncoding.EncodeToString(c.data)
		if utf8.Valid(c.data) {
			out.Preview = string(c.data)
		}
	}
	return out
}

type streamRawBodyReadCloser struct {
	body    io.ReadCloser
	capture *streamRawBodyCapture
}

func (r *streamRawBodyReadCloser) Read(data []byte) (int, error) {
	n, err := r.body.Read(data)
	if n > 0 {
		r.capture.Write(data[:n])
	}
	r.capture.SetReadError(err)
	return n, err
}

func (r *streamRawBodyReadCloser) Close() error {
	return r.body.Close()
}

func summarizeStreamRawRequest(req *http.Request) *streamRawRequestDebug {
	if req == nil {
		return nil
	}
	out := &streamRawRequestDebug{
		Method:        req.Method,
		URL:           sanitizedStreamRawURL(req.URL),
		Header:        redactedStreamRawHeaders(req.Header),
		ContentLength: req.ContentLength,
	}
	if req.URL != nil {
		out.Host = req.URL.Host
		out.Path = req.URL.Path
		out.QueryPresent = req.URL.RawQuery != ""
	}
	return out
}

func summarizeStreamRawResponse(resp *http.Response) *streamRawResponseDebug {
	if resp == nil {
		return nil
	}
	return &streamRawResponseDebug{
		Status:           resp.Status,
		StatusCode:       resp.StatusCode,
		Header:           redactedStreamRawHeaders(resp.Header),
		ContentLength:    resp.ContentLength,
		TransferEncoding: append([]string(nil), resp.TransferEncoding...),
	}
}

func sanitizedStreamRawURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	clone := *u
	clone.User = nil
	clone.RawQuery = ""
	clone.Fragment = ""
	return clone.String()
}

func redactedStreamRawHeaders(headers http.Header) map[string][]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string][]string, len(headers))
	for key, values := range headers {
		if isSensitiveStreamRawHeader(key) {
			out[key] = []string{"<redacted>"}
			continue
		}
		out[key] = append([]string(nil), values...)
	}
	return out
}

func isSensitiveStreamRawHeader(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	return strings.Contains(key, "authorization") ||
		strings.Contains(key, "api-key") ||
		strings.Contains(key, "apikey") ||
		strings.Contains(key, "token") ||
		strings.Contains(key, "secret") ||
		strings.Contains(key, "cookie")
}
