package image

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestNormalizeModelAlias(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "nano banana pro", in: " nano-banana-pro ", want: "gemini-3-pro-image-preview"},
		{name: "nano banana 2", in: "nano-banana-2", want: "gemini-3.1-flash-image-preview"},
		{name: "unknown", in: "gpt-image-2", want: "gpt-image-2"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeModelAlias(tc.in); got != tc.want {
				t.Fatalf("NormalizeModelAlias(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizeResultCompatibilityFields(t *testing.T) {
	t.Run("fills images from data", func(t *testing.T) {
		result := &Result{
			Data:     []ImageData{{B64JSON: "QUJD"}},
			MimeType: "image/png",
		}

		NormalizeResult(result)

		if len(result.Images) != 1 || result.Images[0].DataBase64 != "QUJD" {
			t.Fatalf("unexpected images: %#v", result.Images)
		}
		if result.Images[0].MIMEType != "image/png" {
			t.Fatalf("unexpected image mime type: %q", result.Images[0].MIMEType)
		}
	})

	t.Run("fills data and common mime type from images", func(t *testing.T) {
		result := &Result{
			Images: []ImageAsset{
				{DataBase64: "QUJD", MIMEType: "image/webp"},
				{DataBase64: "REVG", MIMEType: "image/webp"},
			},
		}

		NormalizeResult(result)

		if len(result.Data) != 2 || result.Data[0].B64JSON != "QUJD" || result.Data[1].B64JSON != "REVG" {
			t.Fatalf("unexpected compatibility data: %#v", result.Data)
		}
		if result.MimeType != "image/webp" {
			t.Fatalf("unexpected common mime type: %q", result.MimeType)
		}
	})

	t.Run("does not infer mixed mime type", func(t *testing.T) {
		result := &Result{
			Images: []ImageAsset{
				{DataBase64: "QUJD", MIMEType: "image/png"},
				{DataBase64: "REVG", MIMEType: "image/webp"},
			},
		}

		NormalizeResult(result)

		if result.MimeType != "" {
			t.Fatalf("unexpected mime type: %q", result.MimeType)
		}
	})

	t.Run("preserves url without compatibility data", func(t *testing.T) {
		result := &Result{
			Images: []ImageAsset{
				{URL: "https://example.com/image.png", MIMEType: "image/png"},
			},
		}

		NormalizeResult(result)

		if len(result.Data) != 0 {
			t.Fatalf("unexpected compatibility data: %#v", result.Data)
		}
		if result.Images[0].URL != "https://example.com/image.png" {
			t.Fatalf("unexpected image asset: %#v", result.Images[0])
		}
		if result.MimeType != "image/png" {
			t.Fatalf("unexpected mime type: %q", result.MimeType)
		}
	})
}

func TestImageAssetAsInputImage(t *testing.T) {
	data := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0}
	asset := ImageAsset{
		DataBase64: "data:image/png;base64," + base64.StdEncoding.EncodeToString(data),
	}

	input, err := asset.AsInputImage()
	if err != nil {
		t.Fatalf("AsInputImage: %v", err)
	}
	if input.MIMEType != "image/png" {
		t.Fatalf("unexpected mime type: %q", input.MIMEType)
	}
	if input.Filename != "image.png" {
		t.Fatalf("unexpected filename: %q", input.Filename)
	}
	if !bytes.Equal(input.Data, data) {
		t.Fatalf("unexpected decoded data: %#v", input.Data)
	}
}

func TestImageAssetAsInputImageRejectsMissingInlineData(t *testing.T) {
	_, err := (ImageAsset{URL: "https://example.com/image.png"}).AsInputImage()
	if err == nil {
		t.Fatalf("expected error")
	}
}
