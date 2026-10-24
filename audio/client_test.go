package audio

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientCreateOpenAITranscription(t *testing.T) {
	audioData := append([]byte("OggS\x00\x02"), make([]byte, 32)...)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/transcriptions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("authorization = %q", got)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse form: %v", err)
		}
		if got := r.FormValue("model"); got != "gpt-4o-mini-transcribe" {
			t.Errorf("model = %q", got)
		}
		if got := r.FormValue("language"); got != "en" {
			t.Errorf("language = %q", got)
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Errorf("form file: %v", err)
		} else {
			data, _ := io.ReadAll(file)
			if header.Filename != "voice.ogg" || string(data) != string(audioData) {
				t.Errorf("file = %q (%d bytes)", header.Filename, len(data))
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"text":  "hello there",
			"usage": map[string]any{"type": "tokens", "input_tokens": 10, "output_tokens": 3, "total_tokens": 13},
		})
	}))
	defer server.Close()

	client := New(Config{OpenAIAPIKey: "test-key", OpenAIAPIBase: server.URL})
	for name, opt := range map[string]Option{
		"raw bytes": WithAudioFile(audioData, "voice.ogg", "audio/ogg"),
		"base64": func(r *Request) {
			r.Audio = base64.StdEncoding.EncodeToString(audioData)
			r.Filename = "voice.ogg"
		},
	} {
		t.Run(name, func(t *testing.T) {
			result, err := client.Create(context.Background(),
				WithModel("gpt-4o-mini-transcribe"),
				opt,
				WithOptions(Options{OpenAI: map[string]any{"language": "en"}}),
			)
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			if result.Text != "hello there" || result.Usage == nil || result.Usage.TotalTokens != 13 {
				t.Fatalf("unexpected result: %+v", result)
			}
		})
	}
}

func TestPickProviderByModel(t *testing.T) {
	cases := map[string]string{
		"@cf/openai/whisper-large-v3-turbo": "cloudflare",
		"gpt-4o-mini-transcribe":            "openai",
		"whisper-large-v3-turbo":            "openai",
	}
	for model, want := range cases {
		if got := pickProviderByModel(model); got != want {
			t.Errorf("pickProviderByModel(%q) = %q, want %q", model, got, want)
		}
	}
}

func TestDecodeBase64Audio(t *testing.T) {
	want := []byte{0xff, 0xfb, 0x90, 0x00}
	for _, input := range []string{
		base64.StdEncoding.EncodeToString(want),
		base64.RawURLEncoding.EncodeToString(want),
		"data:audio/mpeg;base64," + base64.StdEncoding.EncodeToString(want),
	} {
		got, err := decodeBase64Audio(input)
		if err != nil || string(got) != string(want) {
			t.Errorf("decode(%q) = %v, %v", input, got, err)
		}
	}
}

func TestClientCreateOpenAIReader(t *testing.T) {
	audioData := append([]byte("OggS\x00\x02"), make([]byte, 32)...)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Errorf("form file: %v", err)
		} else {
			data, _ := io.ReadAll(file)
			if header.Filename != "voice.ogg" || string(data) != string(audioData) {
				t.Errorf("file = %q (%d bytes)", header.Filename, len(data))
			}
		}
		_, _ = w.Write([]byte(`{"text":"from reader"}`))
	}))
	defer server.Close()

	client := New(Config{OpenAIAPIKey: "test-key", OpenAIAPIBase: server.URL})
	result, err := client.Create(context.Background(),
		WithModel("whisper-1"),
		WithAudioReader(bytes.NewReader(audioData), "voice.ogg", ""),
	)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if result.Text != "from reader" {
		t.Fatalf("unexpected result: %+v", result)
	}
}
