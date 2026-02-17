package diag

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	stdhttputil "net/http/httputil"
	"strings"
)

func LogJSON(enabled bool, fn func(string, string), label string, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		if fn != nil {
			fn(label, fmt.Sprintf("<marshal error: %v>", err))
			return
		}
		if enabled {
			log.Printf("%s: <marshal error: %v>", label, err)
		}
		return
	}
	if fn != nil {
		fn(label, string(data))
		return
	}
	if !enabled {
		return
	}
	log.Printf("%s: %s", label, string(data))
}

func LogText(enabled bool, fn func(string, string), label string, text string) {
	if fn != nil {
		fn(label, text)
		return
	}
	if !enabled {
		return
	}
	log.Printf("%s: %s", label, text)
}

func LogError(enabled bool, fn func(string, string), label string, err error) {
	LogErrorWithRawText(enabled, fn, label, err, "")
}

func LogErrorWithRawText(enabled bool, fn func(string, string), label string, err error, rawText string) {
	if err == nil {
		return
	}
	payload := extractErrorPayload(err)
	LogText(enabled, fn, label, payload)

	finalRawText := strings.TrimSpace(rawText)
	if finalRawText == "" {
		finalRawText = extractErrorRawText(err)
	}
	if finalRawText == "" {
		return
	}
	if strings.TrimSpace(finalRawText) == strings.TrimSpace(payload) {
		return
	}
	LogText(enabled, fn, label+".raw_text", finalRawText)
}

func extractErrorPayload(err error) string {
	var withRawJSON interface {
		RawJSON() string
	}
	if errors.As(err, &withRawJSON) {
		if raw := strings.TrimSpace(withRawJSON.RawJSON()); raw != "" {
			return raw
		}
	}
	return err.Error()
}

func extractErrorRawText(err error) string {
	var withDumpResponse interface {
		DumpResponse(bool) []byte
	}
	if errors.As(err, &withDumpResponse) {
		raw := strings.TrimSpace(string(withDumpResponse.DumpResponse(true)))
		if raw != "" {
			return raw
		}
	}
	return ""
}

func HTTPResponseRawText(resp *http.Response) string {
	if resp == nil {
		return ""
	}
	raw, err := stdhttputil.DumpResponse(resp, true)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}
