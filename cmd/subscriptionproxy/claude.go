package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/quailyquaily/uniai/subscription"
	claudeauth "github.com/quailyquaily/uniai/subscription/claude"
)

func loginClaude(ctx context.Context, tokenPath string, cfg claudeauth.OAuthConfig, stdin io.Reader, stdout io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	auth, err := claudeauth.BeginAuthorization(cfg)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "Open %s\nPaste the returned code#state or full callback URL, then press Enter:\n", auth.URL)
	type inputResult struct {
		text string
		err  error
	}
	input := make(chan inputResult, 1)
	go func() {
		text, err := bufio.NewReader(io.LimitReader(stdin, 16<<10)).ReadString('\n')
		input <- inputResult{text, err}
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case result := <-input:
		if result.err != nil && !errors.Is(result.err, io.EOF) {
			return fmt.Errorf("cannot read Claude authorization code")
		}
		token, err := claudeauth.ExchangeCode(ctx, cfg, auth, result.text)
		if err != nil {
			return err
		}
		if token.AccountID == "" {
			return fmt.Errorf("Claude OAuth response has no account ID")
		}
		if err := writeTokenFile(tokenPath, token); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(stdout, "Claude login saved.")
		return nil
	}
}

type claudeCredentialSource struct {
	mu        sync.Mutex
	tokenPath string
	oauth     claudeauth.OAuthConfig
	token     claudeauth.Token
	loaded    bool
	dirty     bool
	now       func() time.Time
	refresh   func(context.Context, claudeauth.OAuthConfig, string) (claudeauth.Token, error)
}

func newClaudeCredentialSource(path string, cfg claudeauth.OAuthConfig) *claudeCredentialSource {
	return &claudeCredentialSource{tokenPath: path, oauth: cfg, now: time.Now, refresh: claudeauth.RefreshToken}
}

func (s *claudeCredentialSource) Credential(ctx context.Context) (subscription.Credential, error) {
	return s.credential(ctx, nil)
}

func (s *claudeCredentialSource) RefreshRejected(ctx context.Context, rejected string) (subscription.Credential, error) {
	rejected = strings.TrimSpace(rejected)
	return s.credential(ctx, &rejected)
}

func (s *claudeCredentialSource) credential(ctx context.Context, rejected *string) (subscription.Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return subscription.Credential{}, err
	}
	if !s.loaded {
		token, err := readTokenFile[claudeauth.Token](s.tokenPath)
		if err != nil {
			return subscription.Credential{}, tokenReadError("Claude", err)
		}
		s.token = token
		s.loaded = true
	}
	if s.dirty {
		if err := writeTokenFile(s.tokenPath, s.token); err != nil {
			return subscription.Credential{}, err
		}
		s.dirty = false
	}
	now := s.now().UTC()
	if !s.token.IsAccessTokenUsable(now) || (rejected != nil && strings.TrimSpace(s.token.AccessToken) == *rejected) {
		old := s.token
		if strings.TrimSpace(old.RefreshToken) == "" {
			return subscription.Credential{}, claudeauth.ErrNotLoggedIn
		}
		token, err := s.refresh(ctx, s.oauth, old.RefreshToken)
		if err != nil {
			return subscription.Credential{}, err
		}
		if !token.IsAccessTokenUsable(s.now().UTC()) {
			return subscription.Credential{}, fmt.Errorf("Claude refresh returned an unusable access token")
		}
		if token.AccountID == "" {
			token.AccountID = old.AccountID
		}
		if token.Scope == "" {
			token.Scope = old.Scope
		}
		if token.RefreshToken == "" {
			token.RefreshToken = old.RefreshToken
		}
		if !old.CreatedAt.IsZero() {
			token.CreatedAt = old.CreatedAt
		}
		if token.UpdatedAt.IsZero() {
			token.UpdatedAt = s.now().UTC()
		}
		// Keep a rotated token in memory even if disk persistence fails. The next
		// call retries the write instead of spending the already-used old token.
		s.token = token
		s.dirty = true
		if err := writeTokenFile(s.tokenPath, token); err != nil {
			return subscription.Credential{}, err
		}
		s.dirty = false
	}
	if strings.TrimSpace(s.token.AccountID) == "" {
		return subscription.Credential{}, fmt.Errorf("Claude token has no account ID; log in again")
	}
	return subscription.Credential{AccessToken: strings.TrimSpace(s.token.AccessToken), AccountID: strings.TrimSpace(s.token.AccountID)}, nil
}
