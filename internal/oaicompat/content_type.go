package oaicompat

import (
	"mime"
	"strings"
)

func IsEventStreamContentType(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = strings.TrimSpace(strings.Split(contentType, ";")[0])
	}
	return strings.EqualFold(mediaType, "text/event-stream")
}
