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
	claudeauth "github.com/quailyquaily/uniai/subscription/claude"
	"github.com/quailyquaily/uniai/subscription/claude/claudecode"
	codexauth "github.com/quailyquaily/uniai/subscription/codex"
	xaiauth "github.com/quailyquaily/uniai/subscription/xai"
)

const (
	backendCodex  = "codex"
	backendXAI    = "xai"
	backendClaude = "claude"
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
	backendFlag := fs.String("backend", "", "backend: codex|grok|claude")
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
	case backendClaude:
		return loginClaude(ctx, tokenPath, claudeauth.OAuthConfig{ClientID: strings.TrimSpace(*clientID)}, os.Stdin, stdout)
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
	backendFlag := fs.String("backend", "", "backend: codex|grok|claude")
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
	case backendClaude:
		token, err := readTokenFile[claudeauth.Token](tokenPath)
		if errors.Is(err, os.ErrNotExist) {
			_, _ = fmt.Fprintln(stdout, "backend=claude logged_in=false")
			return nil
		}
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(stdout, "backend=claude logged_in=%t access_usable=%t expires_at=%s account_id=%s\n",
			strings.TrimSpace(token.AccessToken) != "" || strings.TrimSpace(token.RefreshToken) != "",
			token.IsAccessTokenUsable(now), formatTime(token.ExpiresAt), token.AccountID)
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
	backendFlag := fs.String("backend", "", "backend: codex|grok|claude")
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
	backendFlag := fs.String("backend", "", "backend: codex|grok|claude")
	tokenFileFlag := fs.String("token-file", "", "credential file path (required)")
	clientID := fs.String("client-id", "", "OAuth client ID used during login (optional)")
	codexTokenFileFlag := fs.String("codex-token-file", "", "Codex credential file path (multi-backend mode)")
	grokTokenFileFlag := fs.String("grok-token-file", "", "Grok credential file path (multi-backend mode)")
	claudeTokenFileFlag := fs.String("claude-token-file", "", "Claude credential file path (multi-backend mode)")
	claudeClientID := fs.String("claude-client-id", "", "Claude OAuth client ID used during login (optional)")
	claudeCodeVersion := fs.String("claude-code-version", claudecode.DefaultVersion, "Claude Code HTTP compatibility profile version")
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

	type backendSpec struct{ name, path, clientID string }
	type configuredBackend struct {
		name   string
		source subscription.CredentialSource
	}
	var specs []backendSpec
	var configured []configuredBackend
	var handler http.Handler
	var serverDescription string
	defaultModel := strings.TrimSpace(*model)
	multiMode := strings.TrimSpace(*codexTokenFileFlag) != "" || strings.TrimSpace(*grokTokenFileFlag) != "" ||
		strings.TrimSpace(*claudeTokenFileFlag) != "" || strings.TrimSpace(*codexClientID) != "" ||
		strings.TrimSpace(*grokClientID) != "" || strings.TrimSpace(*claudeClientID) != ""
	if multiMode {
		if strings.TrimSpace(*backendFlag) != "" || strings.TrimSpace(*tokenFileFlag) != "" || strings.TrimSpace(*clientID) != "" {
			return fmt.Errorf("multi-backend options cannot be combined with --backend, --token-file, or --client-id")
		}
		for _, spec := range []backendSpec{
			{backendCodex, *codexTokenFileFlag, *codexClientID},
			{backendXAI, *grokTokenFileFlag, *grokClientID},
			{backendClaude, *claudeTokenFileFlag, *claudeClientID},
		} {
			spec.path, spec.clientID = strings.TrimSpace(spec.path), strings.TrimSpace(spec.clientID)
			if spec.path == "" {
				if spec.clientID != "" {
					return fmt.Errorf("--%s-token-file is required with --%s-client-id", displayBackend(spec.name), displayBackend(spec.name))
				}
				continue
			}
			specs = append(specs, spec)
		}
		if len(specs) < 2 {
			return fmt.Errorf("multi-backend mode requires at least two token files; use --backend and --token-file for one backend")
		}
	} else {
		backend, err := normalizeBackend(*backendFlag)
		if err != nil {
			return err
		}
		if defaultModel == "" {
			return fmt.Errorf("--model is required")
		}
		path, err := resolveTokenPath(*tokenFileFlag)
		if err != nil {
			return err
		}
		specs = []backendSpec{{backend, path, strings.TrimSpace(*clientID)}}
	}
	runners := make(map[string]chatRunner)
	paths := make(map[string]bool)
	var names []string
	for _, spec := range specs {
		path, err := filepath.Abs(spec.path)
		if err != nil {
			return err
		}
		if paths[path] {
			return fmt.Errorf("backends require distinct token files")
		}
		paths[path] = true
		var source subscription.CredentialSource
		cfg := uniai.Config{OpenAIModel: defaultModel, AnthropicModel: defaultModel}
		switch spec.name {
		case backendCodex:
			source = newCodexCredentialSource(path, codexauth.OAuthConfig{ClientID: spec.clientID})
			cfg.Provider = "openai_codex"
			cfg.CodexSubscription = source
		case backendXAI:
			source = newXAICredentialSource(path, xaiauth.OAuthConfig{ClientID: spec.clientID})
			cfg.Provider = "xai_oauth"
			cfg.XAISubscription = source
		case backendClaude:
			source = newClaudeCredentialSource(path, claudeauth.OAuthConfig{ClientID: spec.clientID})
			cfg.Provider = "claude_oauth"
			cfg.ClaudeSubscription = source
			cfg.ClaudeCode = claudecode.Profile{Version: strings.TrimSpace(*claudeCodeVersion)}
		}
		configured = append(configured, configuredBackend{spec.name, source})
		runners[spec.name] = uniai.New(cfg)
		names = append(names, displayBackend(spec.name))
	}
	if multiMode {
		handler = newRoutedAPIHandler(runners, defaultModel)
		serverDescription = "backends=" + strings.Join(names, ",")
		if defaultModel != "" {
			serverDescription += " default_model=" + defaultModel
		}
	} else {
		handler = newAPIHandler(runners[specs[0].name], specs[0].name, defaultModel)
		serverDescription = fmt.Sprintf("backend=%s model=%s", displayBackend(specs[0].name), defaultModel)
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
	case backendClaude:
		return backendClaude, nil
	case "":
		return "", fmt.Errorf("--backend is required (codex, grok, or claude)")
	default:
		return "", fmt.Errorf("unsupported backend %q; use codex, grok, or claude", value)
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
  login   --backend codex|grok|claude --token-file path
  status  --backend codex|grok|claude --token-file path
  logout  --backend codex|grok|claude --token-file path
  serve   --backend codex|grok|claude --token-file path --model model [--listen 127.0.0.1:8080]
  serve   --codex-token-file path --grok-token-file path [--claude-token-file path] [--model default-model]

Multi-backend serve accepts any two or all three backend token-file options.
Claude login uses browser authorization with a pasted code#state, not a device code.
Claude serves /v1/chat/completions; /v1/responses is supported only by Codex and Grok.`
}
