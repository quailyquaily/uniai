package openai

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/lyricat/goutils/structs"
)

var oggHeader = append([]byte("OggS\x00\x02"), make([]byte, 32)...)

type parsedForm struct {
	fields   map[string][]string
	filename string
	fileType string
	fileData []byte
}

func parseMultipart(t *testing.T, contentType string, body []byte) parsedForm {
	t.Helper()
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		t.Fatalf("parse content type: %v", err)
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	out := parsedForm{fields: map[string][]string{}}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("next part: %v", err)
		}
		data, _ := io.ReadAll(part)
		if part.FormName() == "file" {
			out.filename = part.FileName()
			out.fileType = part.Header.Get("Content-Type")
			out.fileData = data
			continue
		}
		out.fields[part.FormName()] = append(out.fields[part.FormName()], string(data))
	}
	return out
}

func TestBuildOpenAITranscriptionMultipart(t *testing.T) {
	options := structs.JSONMap{
		"language":                "en",
		"temperature":             0.2,
		"response_format":         "verbose_json",
		"timestamp_granularities": []any{"segment", "word"},
		"chunking_strategy":       map[string]any{"type": "server_vad"},
	}
	contentType, body, err := buildOpenAITranscriptionMultipart("whisper-1", InputAudio{Data: oggHeader, MIMEType: "audio/ogg; codecs=opus"}, options)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	form := parseMultipart(t, contentType, body)

	if got := form.fields["model"]; len(got) != 1 || got[0] != "whisper-1" {
		t.Fatalf("model = %v", got)
	}
	if got := form.fields["temperature"]; len(got) != 1 || got[0] != "0.2" {
		t.Fatalf("temperature = %v", got)
	}
	if got := form.fields["timestamp_granularities[]"]; len(got) != 2 || got[0] != "segment" || got[1] != "word" {
		t.Fatalf("timestamp_granularities[] = %v", got)
	}
	if got := form.fields["chunking_strategy"]; len(got) != 1 || got[0] != `{"type":"server_vad"}` {
		t.Fatalf("chunking_strategy = %v", got)
	}
	if form.filename != "audio.ogg" || form.fileType != "audio/ogg" {
		t.Fatalf("file = %q %q", form.filename, form.fileType)
	}
	if !bytes.Equal(form.fileData, oggHeader) {
		t.Fatalf("file data mismatch")
	}
}

func TestResolveAudioFile(t *testing.T) {
	tests := []struct {
		name         string
		input        InputAudio
		wantFilename string
		wantMIME     string
		wantErr      bool
	}{
		{"explicit filename", InputAudio{Filename: "voice.zzaudio", Data: []byte("x")}, "voice.zzaudio", "application/octet-stream", false},
		{"filename gives mime", InputAudio{Filename: "voice.mp3", Data: []byte("x")}, "voice.mp3", "audio/mpeg", false},
		{"mime type", InputAudio{MIMEType: "audio/x-m4a", Data: []byte("x")}, "audio.m4a", "audio/x-m4a", false},
		{"sniffed ogg", InputAudio{Data: oggHeader}, "audio.ogg", "application/ogg", false},
		{"octet-stream is sniffed", InputAudio{MIMEType: "application/octet-stream", Data: oggHeader}, "audio.ogg", "application/ogg", false},
		{"unknown", InputAudio{Data: []byte("hello world")}, "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filename, mimeType, err := resolveAudioFile(tt.input.Filename, tt.input.MIMEType, tt.input.Data)
			if tt.wantErr {
				if !errors.Is(err, ErrUnsupportedAudioFormat) {
					t.Fatalf("err = %v, want ErrUnsupportedAudioFormat", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if filename != tt.wantFilename || mimeType != tt.wantMIME {
				t.Fatalf("got %q %q, want %q %q", filename, mimeType, tt.wantFilename, tt.wantMIME)
			}
		})
	}
}

func TestMapOpenAITranscriptionOutput(t *testing.T) {
	out, err := mapOpenAITranscriptionOutput([]byte(`{
		"text": "hello",
		"usage": {"type": "tokens", "input_tokens": 14, "input_token_details": {"text_tokens": 4, "audio_tokens": 10}, "output_tokens": 2, "total_tokens": 16}
	}`))
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if out.Text != "hello" || out.Usage == nil || out.Usage.InputAudioTokens != 10 || out.Usage.InputTextTokens != 4 || out.Usage.TotalTokens != 16 {
		t.Fatalf("unexpected result: %+v %+v", out, out.Usage)
	}

	out, err = mapOpenAITranscriptionOutput([]byte(`{"text":"hi","language":"english","duration":1.5,"segments":[{"id":0,"start":0,"end":1.5,"text":"hi"}],"usage":{"type":"duration","seconds":2}}`))
	if err != nil {
		t.Fatalf("map verbose: %v", err)
	}
	if out.Language != "english" || out.Duration != 1.5 || len(out.Segments) != 1 || out.Segments[0].End != 1.5 || out.Usage.Seconds != 2 {
		t.Fatalf("unexpected verbose result: %+v", out)
	}

	out, err = mapOpenAITranscriptionOutput([]byte("1\n00:00:00,000 --> 00:00:01,500\nhi\n"))
	if err != nil {
		t.Fatalf("map srt: %v", err)
	}
	if !strings.HasPrefix(out.Text, "1\n00:00:00,000") {
		t.Fatalf("unexpected srt text: %q", out.Text)
	}
}

func TestTranscribeRejectsOversizedUploadOnOfficialBase(t *testing.T) {
	data := make([]byte, maxOpenAIAudioBytes+1)
	if _, err := Transcribe(t.Context(), "key", "", "whisper-1", InputAudio{Data: data, Filename: "a.wav"}, nil); err == nil || !strings.Contains(err.Error(), "25 MB") {
		t.Fatalf("err = %v, want size error", err)
	}
}

func TestTranscribeStreamsReader(t *testing.T) {
	audio := append(append([]byte{}, oggHeader...), bytes.Repeat([]byte{0x01}, 64<<10)...)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength != -1 {
			t.Errorf("content length = %d, want unknown (streamed)", r.ContentLength)
		}
		body, _ := io.ReadAll(r.Body)
		form := parseMultipart(t, r.Header.Get("Content-Type"), body)
		if form.filename != "audio.ogg" || !bytes.Equal(form.fileData, audio) {
			t.Errorf("file = %q (%d bytes)", form.filename, len(form.fileData))
		}
		if got := form.fields["model"]; len(got) != 1 || got[0] != "gpt-4o-mini-transcribe" {
			t.Errorf("model = %v", got)
		}
		_, _ = w.Write([]byte(`{"text":"streamed"}`))
	}))
	defer server.Close()

	// iotest.OneByteReader hides Len(), so the body really goes through the pipe.
	respData, err := Transcribe(t.Context(), "key", server.URL, "gpt-4o-mini-transcribe", InputAudio{Reader: iotest.OneByteReader(bytes.NewReader(audio))}, nil)
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if !strings.Contains(string(respData), `"text":"streamed"`) {
		t.Fatalf("unexpected response: %s", respData)
	}
}

func TestTranscribeStreamErrorResponseDoesNotHang(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
	}))
	defer server.Close()

	// An endless reader: the call must return once the server responds.
	endless := io.MultiReader(bytes.NewReader(oggHeader), neverEnding{})
	done := make(chan error, 1)
	go func() {
		_, err := Transcribe(t.Context(), "key", server.URL, "whisper-1", InputAudio{Reader: endless}, nil)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("expected error")
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("Transcribe hung after error response")
	}
}

func TestMaxBytesReader(t *testing.T) {
	r := &maxBytesReader{r: bytes.NewReader(make([]byte, 11)), remaining: 10}
	if _, err := io.ReadAll(r); !errors.Is(err, errAudioTooLarge) {
		t.Fatalf("err = %v, want errAudioTooLarge", err)
	}
	r = &maxBytesReader{r: bytes.NewReader(make([]byte, 10)), remaining: 10}
	if _, err := io.ReadAll(r); err != nil {
		t.Fatalf("err = %v, want nil at the limit", err)
	}
}

type neverEnding struct{}

func (neverEnding) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}
