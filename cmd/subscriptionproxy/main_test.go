package main

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestNormalizeBackend(t *testing.T) {
	tests := map[string]string{
		"codex":  backendCodex,
		"grok":   backendXAI,
		"xai":    backendXAI,
		"claude": backendClaude,
	}
	for input, want := range tests {
		got, err := normalizeBackend(input)
		if err != nil || got != want {
			t.Fatalf("normalizeBackend(%q) = %q, %v", input, got, err)
		}
	}
	if _, err := normalizeBackend("openai"); err == nil {
		t.Fatal("unknown backend should fail")
	}
}

func TestResolveTokenPathRequiresExplicitPath(t *testing.T) {
	if _, err := resolveTokenPath(""); err == nil || !strings.Contains(err.Error(), "--token-file is required") {
		t.Fatalf("missing token file error = %v", err)
	}

	got, err := resolveTokenPath(" ./auth/codex.json ")
	if err != nil {
		t.Fatalf("resolve explicit token path: %v", err)
	}
	if got != "auth/codex.json" {
		t.Fatalf("token path = %q", got)
	}
}

func TestCredentialCommandsRequireTokenFile(t *testing.T) {
	tests := [][]string{
		{"login", "--backend", "codex"},
		{"status", "--backend", "codex"},
		{"logout", "--backend", "codex"},
		{"serve", "--backend", "codex", "--model", "gpt-5.4"},
	}

	for _, args := range tests {
		t.Run(args[0], func(t *testing.T) {
			err := run(context.Background(), args, io.Discard, io.Discard)
			if err == nil || !strings.Contains(err.Error(), "--token-file is required") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestMultiBackendServeRequiresAtLeastTwoTokenFiles(t *testing.T) {
	err := run(context.Background(), []string{
		"serve",
		"--codex-token-file", "codex.json",
	}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "at least two") {
		t.Fatalf("error = %v", err)
	}
}

func TestDualBackendServeAcceptsTwoExplicitTokenFiles(t *testing.T) {
	dir := t.TempDir()
	err := run(context.Background(), []string{
		"serve",
		"--codex-token-file", dir + "/codex.json",
		"--grok-token-file", dir + "/grok.json",
	}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "load or refresh codex credentials") {
		t.Fatalf("error = %v", err)
	}
}
