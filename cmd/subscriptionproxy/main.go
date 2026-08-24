package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/quailyquaily/uniai"
	"github.com/quailyquaily/uniai/subscription"
	codexauth "github.com/quailyquaily/uniai/subscription/codex"
	xaiauth "github.com/quailyquaily/uniai/subscription/xai"
)

const (
	backendCodex = "codex"
	backendXAI   = "xai"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return usageError()
	}
	switch args[0] {
	case "login":
		return runLogin(ctx, args[1:], stdout, stderr)
	case "status":
		return runStatus(args[1:], stdout, stderr)
	case "logout":
		return runLogout(ctx, args[1:], stdout, stderr)
	case "serve":
		return runServe(ctx, args[1:], stdout, stderr)
	case "help", "-h", "--help":
		_, _ = fmt.Fprintln(stdout, usageText())
		return nil
	default:
		return usageError()
	}
}

func runLogin(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(stderr)
	backendFlag := fs.String("backend", "", "backend: codex|grok")
	tokenFileFlag := fs.String("token-file", "", "credential file path (required)")
	clientID := fs.String("client-id", "", "OAuth client ID (optional)")
	scope := fs.String("scope", "", "xAI OAuth scopes (optional)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("login accepts no positional arguments")
	}
	backend, err := normalizeBackend(*backendFlag)
	if err != nil {
		return err
	}
	tokenPath, err := resolveTokenPath(*tokenFileFlag)
	if err != nil {
		return err
	}
	switch backend {
	case backendCodex:
		return loginCodex(ctx, tokenPath, codexauth.OAuthConfig{
			ClientID: strings.TrimSpace(*clientID),
		}, stdout)
	case backendXAI:
		return loginXAI(ctx, tokenPath, xaiauth.OAuthConfig{
			ClientID: strings.TrimSpace(*clientID),
			Scope:    strings.TrimSpace(*scope),
		}, stdout)
	default:
		return fmt.Errorf("unsupported backend %q", backend)
	}
}

func loginCodex(ctx context.Context, tokenPath string, cfg codexauth.OAuthConfig, stdout io.Writer) error {
	code, err := codexauth.RequestDeviceCode(ctx, cfg)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "Open %s and enter code %s\n", code.VerificationURL, code.UserCode)
	interval := code.Interval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	for {
		if err := waitContext(ctx, interval); err != nil {
			return err
		}
		token, err := codexauth.PollDeviceCode(ctx, cfg, code)
		if errors.Is(err, codexauth.ErrAuthorizationPending) {
			continue
		}
		if err != nil {
			return err
		}
		if err := writeTokenFile(tokenPath, token); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(stdout, "Codex login saved.")
		return nil
	}
}

func loginXAI(ctx context.Context, tokenPath string, cfg xaiauth.OAuthConfig, stdout io.Writer) error {
	code, err := xaiauth.RequestDeviceCode(ctx, cfg)
	if err != nil {
		return err
	}
	verificationURL := code.VerificationURLComplete
	if verificationURL == "" {
		verificationURL = code.VerificationURL
	}
	_, _ = fmt.Fprintf(stdout, "Open %s and enter code %s\n", verificationURL, code.UserCode)
	interval := code.Interval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	for {
		if err := waitContext(ctx, interval); err != nil {
			return err
		}
		token, err := xaiauth.PollDeviceCode(ctx, cfg, code)
		switch {
		case err == nil:
			if err := writeTokenFile(tokenPath, token); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(stdout, "xAI login saved.")
			return nil
		case errors.Is(err, xaiauth.ErrAuthorizationPending):
			continue
		case errors.Is(err, xaiauth.ErrSlowDown):
			interval += 5 * time.Second
			continue
		default:
			return err
		}
	}
}

func runStatus(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	backendFlag := fs.String("backend", "", "backend: codex|grok")
	tokenFileFlag := fs.String("token-file", "", "credential file path (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("status accepts no positional arguments")
	}
	backend, err := normalizeBackend(*backendFlag)
	if err != nil {
		return err
	}
	tokenPath, err := resolveTokenPath(*tokenFileFlag)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	switch backend {
	case backendCodex:
		token, err := readTokenFile[codexauth.Token](tokenPath)
		if errors.Is(err, os.ErrNotExist) {
			_, _ = fmt.Fprintln(stdout, "backend=codex logged_in=false")
			return nil
		}
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(stdout, "backend=codex logged_in=%t access_usable=%t expires_at=%s account_id=%s plan=%s\n",
			strings.TrimSpace(token.AccessToken) != "" || strings.TrimSpace(token.RefreshToken) != "",
			token.IsAccessTokenUsable(now), formatTime(token.ExpiresAt), token.AccountID, token.PlanType)
	case backendXAI:
		token, err := readTokenFile[xaiauth.Token](tokenPath)
		if errors.Is(err, os.ErrNotExist) {
			_, _ = fmt.Fprintln(stdout, "backend=grok logged_in=false")
			return nil
		}
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(stdout, "backend=grok logged_in=%t access_usable=%t expires_at=%s scope=%s\n",
			strings.TrimSpace(token.AccessToken) != "" || strings.TrimSpace(token.RefreshToken) != "",
			token.IsAccessTokenUsable(now), formatTime(token.ExpiresAt), token.Scope)
	}
	return nil
}

func runLogout(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("logout", flag.ContinueOnError)
	fs.SetOutput(stderr)
	backendFlag := fs.String("backend", "", "backend: codex|grok")
	tokenFileFlag := fs.String("token-file", "", "credential file path (required)")
	clientID := fs.String("client-id", "", "OAuth client ID (optional)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("logout accepts no positional arguments")
	}
	backend, err := normalizeBackend(*backendFlag)
	if err != nil {
		return err
	}
	tokenPath, err := resolveTokenPath(*tokenFileFlag)
	if err != nil {
		return err
	}
	if backend == backendXAI {
		token, readErr := readTokenFile[xaiauth.Token](tokenPath)
		if readErr == nil {
			if revokeErr := xaiauth.RevokeToken(ctx, xaiauth.OAuthConfig{ClientID: strings.TrimSpace(*clientID)}, token); revokeErr != nil {
				_, _ = fmt.Fprintf(stderr, "warning: xAI token revocation failed: %v\n", revokeErr)
			}
		} else if !errors.Is(readErr, os.ErrNotExist) {
			return readErr
		}
	}
	if err := deleteTokenFile(tokenPath); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "%s local login removed.\n", displayBackend(backend))
	return nil
}

func runServe(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	backendFlag := fs.String("backend", "", "backend: codex|grok")
	tokenFileFlag := fs.String("token-file", "", "credential file path (required)")
	clientID := fs.String("client-id", "", "OAuth client ID used during login (optional)")
	codexTokenFileFlag := fs.String("codex-token-file", "", "Codex credential file path (dual-backend mode)")
	grokTokenFileFlag := fs.String("grok-token-file", "", "Grok credential file path (dual-backend mode)")
	codexClientID := fs.String("codex-client-id", "", "Codex OAuth client ID used during login (optional)")
	grokClientID := fs.String("grok-client-id", "", "Grok OAuth client ID used during login (optional)")
	model := fs.String("model", "", "default upstream model")
	listen := fs.String("listen", "127.0.0.1:8080", "HTTP listen address")
	refreshInterval := fs.Duration("refresh-interval", 30*time.Second, "periodic token check interval")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("serve accepts no positional arguments")
	}
	if strings.TrimSpace(*listen) == "" {
		return fmt.Errorf("--listen is required")
	}
	if *refreshInterval <= 0 {
		return fmt.Errorf("--refresh-interval must be greater than zero")
	}

	type configuredBackend struct {
		name   string
		source subscription.CredentialSource
	}
	var configured []configuredBackend
	var handler http.Handler
	var serverDescription string
	defaultModel := strings.TrimSpace(*model)
	dualMode := strings.TrimSpace(*codexTokenFileFlag) != "" ||
		strings.TrimSpace(*grokTokenFileFlag) != "" ||
		strings.TrimSpace(*codexClientID) != "" ||
		strings.TrimSpace(*grokClientID) != ""

	if dualMode {
		if strings.TrimSpace(*backendFlag) != "" || strings.TrimSpace(*tokenFileFlag) != "" || strings.TrimSpace(*clientID) != "" {
			return fmt.Errorf("dual-backend options cannot be combined with --backend, --token-file, or --client-id")
		}
		codexTokenPath := strings.TrimSpace(*codexTokenFileFlag)
		if codexTokenPath == "" {
			return fmt.Errorf("--codex-token-file is required in dual-backend mode")
		}
		grokTokenPath := strings.TrimSpace(*grokTokenFileFlag)
		if grokTokenPath == "" {
			return fmt.Errorf("--grok-token-file is required in dual-backend mode")
		}
		codexSource := newCodexCredentialSource(filepath.Clean(codexTokenPath), codexauth.OAuthConfig{
			ClientID: strings.TrimSpace(*codexClientID),
		})
		grokSource := newXAICredentialSource(filepath.Clean(grokTokenPath), xaiauth.OAuthConfig{
			ClientID: strings.TrimSpace(*grokClientID),
		})
		configured = []configuredBackend{
			{name: backendCodex, source: codexSource},
			{name: backendXAI, source: grokSource},
		}
		codexClient := uniai.New(uniai.Config{
			Provider:          "openai_codex",
			OpenAIModel:       defaultModel,
			CodexSubscription: codexSource,
		})
		grokClient := uniai.New(uniai.Config{
			Provider:        "xai_oauth",
			OpenAIModel:     defaultModel,
			XAISubscription: grokSource,
		})
		handler = newRoutedAPIHandler(codexClient, grokClient, defaultModel)
		serverDescription = "backends=codex,grok"
		if defaultModel != "" {
			serverDescription += " default_model=" + defaultModel
		}
	} else {
		backend, err := normalizeBackend(*backendFlag)
		if err != nil {
			return err
		}
		if defaultModel == "" {
			return fmt.Errorf("--model is required")
		}
		tokenPath, err := resolveTokenPath(*tokenFileFlag)
		if err != nil {
			return err
		}
		var source subscription.CredentialSource
		cfg := uniai.Config{OpenAIModel: defaultModel}
		switch backend {
		case backendCodex:
			source = newCodexCredentialSource(tokenPath, codexauth.OAuthConfig{ClientID: strings.TrimSpace(*clientID)})
			cfg.Provider = "openai_codex"
			cfg.CodexSubscription = source
		case backendXAI:
			source = newXAICredentialSource(tokenPath, xaiauth.OAuthConfig{ClientID: strings.TrimSpace(*clientID)})
			cfg.Provider = "xai_oauth"
			cfg.XAISubscription = source
		}
		configured = []configuredBackend{{name: backend, source: source}}
		handler = newAPIHandler(uniai.New(cfg), backend, defaultModel)
		serverDescription = fmt.Sprintf("backend=%s model=%s", displayBackend(backend), defaultModel)
	}

	for _, backend := range configured {
		if _, err := backend.source.Credential(ctx); err != nil {
			return fmt.Errorf("load or refresh %s credentials: %w", displayBackend(backend.name), err)
		}
	}

	refreshCtx, stopRefresh := context.WithCancel(ctx)
	defer stopRefresh()
	for _, backend := range configured {
		go runRefreshLoop(refreshCtx, *refreshInterval, backend.source, func(err error) {
			_, _ = fmt.Fprintf(stderr, "%s token refresh check failed: %v\n", displayBackend(backend.name), err)
		})
	}

	server := &http.Server{
		Addr:              strings.TrimSpace(*listen),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.ListenAndServe()
	}()
	_, _ = fmt.Fprintf(stdout, "listening=http://%s %s\n", server.Addr, serverDescription)

	select {
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return nil
	}
}

func normalizeBackend(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case backendCodex:
		return backendCodex, nil
	case "grok", backendXAI:
		return backendXAI, nil
	case "":
		return "", fmt.Errorf("--backend is required (codex or grok)")
	default:
		return "", fmt.Errorf("unsupported backend %q; use codex or grok", value)
	}
}

func resolveTokenPath(explicit string) (string, error) {
	path := strings.TrimSpace(explicit)
	if path == "" {
		return "", fmt.Errorf("--token-file is required")
	}
	return filepath.Clean(path), nil
}

func waitContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return "unknown"
	}
	return value.UTC().Format(time.RFC3339)
}

func displayBackend(backend string) string {
	if backend == backendXAI {
		return "grok"
	}
	return backend
}

func usageError() error {
	return errors.New(usageText())
}

func usageText() string {
	return `usage: subscriptionproxy <command> [options]

commands:
  login   --backend codex|grok --token-file path
  status  --backend codex|grok --token-file path
  logout  --backend codex|grok --token-file path
  serve   --backend codex|grok --token-file path --model model [--listen 127.0.0.1:8080]
  serve   --codex-token-file path --grok-token-file path [--model default-model] [--listen 127.0.0.1:8080]`
}
