package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/quailyquaily/uniai/subscription"
	codexauth "github.com/quailyquaily/uniai/subscription/codex"
	xaiauth "github.com/quailyquaily/uniai/subscription/xai"
)

const maxTokenFileSize = 1 << 20

type codexCredentialSource struct {
	mu        sync.RWMutex
	tokenPath string
	oauth     codexauth.OAuthConfig
	token     codexauth.Token
	loaded    bool
	now       func() time.Time
	refresh   func(context.Context, codexauth.OAuthConfig, string) (codexauth.Token, error)
}

func newCodexCredentialSource(tokenPath string, oauth codexauth.OAuthConfig) *codexCredentialSource {
	return &codexCredentialSource{
		tokenPath: tokenPath,
		oauth:     oauth,
		now:       func() time.Time { return time.Now().UTC() },
		refresh:   codexauth.RefreshToken,
	}
}

func (s *codexCredentialSource) Credential(ctx context.Context) (subscription.Credential, error) {
	now := s.currentTime()
	s.mu.RLock()
	if s.loaded && s.token.IsAccessTokenUsable(now) {
		token := s.token
		s.mu.RUnlock()
		return codexCredential(token), nil
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return subscription.Credential{}, err
	}
	if s.token.IsAccessTokenUsable(s.currentTime()) {
		return codexCredential(s.token), nil
	}
	return s.refreshLocked(ctx)
}

func (s *codexCredentialSource) RefreshRejected(ctx context.Context, rejectedAccessToken string) (subscription.Credential, error) {
	rejectedAccessToken = strings.TrimSpace(rejectedAccessToken)
	now := s.currentTime()
	s.mu.RLock()
	if s.loaded && s.token.AccessToken != rejectedAccessToken && s.token.IsAccessTokenUsable(now) {
		token := s.token
		s.mu.RUnlock()
		return codexCredential(token), nil
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return subscription.Credential{}, err
	}
	if s.token.AccessToken != rejectedAccessToken && s.token.IsAccessTokenUsable(s.currentTime()) {
		return codexCredential(s.token), nil
	}
	return s.refreshLocked(ctx)
}

func (s *codexCredentialSource) loadLocked() error {
	if s.loaded {
		return nil
	}
	token, err := readTokenFile[codexauth.Token](s.tokenPath)
	if err != nil {
		return tokenReadError("codex", err)
	}
	s.token = token
	s.loaded = true
	return nil
}

func (s *codexCredentialSource) refreshLocked(ctx context.Context) (subscription.Credential, error) {
	old := s.token
	if strings.TrimSpace(old.RefreshToken) == "" {
		return subscription.Credential{}, codexauth.ErrNotLoggedIn
	}
	refreshed, err := s.refresh(ctx, s.oauth, old.RefreshToken)
	if err != nil {
		if errors.Is(err, codexauth.ErrNotLoggedIn) {
			_ = deleteTokenFile(s.tokenPath)
			s.token = codexauth.Token{}
			s.loaded = false
		}
		return subscription.Credential{}, err
	}
	if refreshed.IDToken == "" {
		refreshed.IDToken = old.IDToken
	}
	if refreshed.AccountID == "" {
		refreshed.AccountID = old.AccountID
	}
	if refreshed.PlanType == "" {
		refreshed.PlanType = old.PlanType
	}
	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = old.RefreshToken
	}
	if !old.CreatedAt.IsZero() {
		refreshed.CreatedAt = old.CreatedAt
	}
	if refreshed.UpdatedAt.IsZero() {
		refreshed.UpdatedAt = s.currentTime()
	}
	if err := writeTokenFile(s.tokenPath, refreshed); err != nil {
		return subscription.Credential{}, err
	}
	s.token = refreshed
	s.loaded = true
	return codexCredential(refreshed), nil
}

func (s *codexCredentialSource) currentTime() time.Time {
	if s.now == nil {
		return time.Now().UTC()
	}
	return s.now().UTC()
}

func codexCredential(token codexauth.Token) subscription.Credential {
	return subscription.Credential{
		AccessToken: strings.TrimSpace(token.AccessToken),
		AccountID:   strings.TrimSpace(token.AccountID),
	}
}

type xaiCredentialSource struct {
	mu        sync.RWMutex
	tokenPath string
	oauth     xaiauth.OAuthConfig
	token     xaiauth.Token
	loaded    bool
	now       func() time.Time
	refresh   func(context.Context, xaiauth.OAuthConfig, string) (xaiauth.Token, error)
}

func newXAICredentialSource(tokenPath string, oauth xaiauth.OAuthConfig) *xaiCredentialSource {
	return &xaiCredentialSource{
		tokenPath: tokenPath,
		oauth:     oauth,
		now:       func() time.Time { return time.Now().UTC() },
		refresh:   xaiauth.RefreshToken,
	}
}

func (s *xaiCredentialSource) Credential(ctx context.Context) (subscription.Credential, error) {
	now := s.currentTime()
	s.mu.RLock()
	if s.loaded && s.token.IsAccessTokenUsable(now) {
		token := s.token
		s.mu.RUnlock()
		return subscription.Credential{AccessToken: strings.TrimSpace(token.AccessToken)}, nil
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return subscription.Credential{}, err
	}
	if s.token.IsAccessTokenUsable(s.currentTime()) {
		return subscription.Credential{AccessToken: strings.TrimSpace(s.token.AccessToken)}, nil
	}
	return s.refreshLocked(ctx)
}

func (s *xaiCredentialSource) RefreshRejected(ctx context.Context, rejectedAccessToken string) (subscription.Credential, error) {
	rejectedAccessToken = strings.TrimSpace(rejectedAccessToken)
	now := s.currentTime()
	s.mu.RLock()
	if s.loaded && s.token.AccessToken != rejectedAccessToken && s.token.IsAccessTokenUsable(now) {
		token := s.token
		s.mu.RUnlock()
		return subscription.Credential{AccessToken: strings.TrimSpace(token.AccessToken)}, nil
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return subscription.Credential{}, err
	}
	if s.token.AccessToken != rejectedAccessToken && s.token.IsAccessTokenUsable(s.currentTime()) {
		return subscription.Credential{AccessToken: strings.TrimSpace(s.token.AccessToken)}, nil
	}
	return s.refreshLocked(ctx)
}

func (s *xaiCredentialSource) loadLocked() error {
	if s.loaded {
		return nil
	}
	token, err := readTokenFile[xaiauth.Token](s.tokenPath)
	if err != nil {
		return tokenReadError("xAI", err)
	}
	s.token = token
	s.loaded = true
	return nil
}

func (s *xaiCredentialSource) refreshLocked(ctx context.Context) (subscription.Credential, error) {
	old := s.token
	if strings.TrimSpace(old.RefreshToken) == "" {
		return subscription.Credential{}, xaiauth.ErrNotLoggedIn
	}
	refreshed, err := s.refresh(ctx, s.oauth, old.RefreshToken)
	if err != nil {
		if errors.Is(err, xaiauth.ErrNotLoggedIn) {
			_ = deleteTokenFile(s.tokenPath)
			s.token = xaiauth.Token{}
			s.loaded = false
		}
		return subscription.Credential{}, err
	}
	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = old.RefreshToken
	}
	if refreshed.TokenType == "" {
		refreshed.TokenType = old.TokenType
	}
	if refreshed.Scope == "" {
		refreshed.Scope = old.Scope
	}
	if !old.CreatedAt.IsZero() {
		refreshed.CreatedAt = old.CreatedAt
	}
	if refreshed.UpdatedAt.IsZero() {
		refreshed.UpdatedAt = s.currentTime()
	}
	if err := writeTokenFile(s.tokenPath, refreshed); err != nil {
		return subscription.Credential{}, err
	}
	s.token = refreshed
	s.loaded = true
	return subscription.Credential{AccessToken: strings.TrimSpace(refreshed.AccessToken)}, nil
}

func (s *xaiCredentialSource) currentTime() time.Time {
	if s.now == nil {
		return time.Now().UTC()
	}
	return s.now().UTC()
}

func runRefreshLoop(
	ctx context.Context,
	interval time.Duration,
	source subscription.CredentialSource,
	onError func(error),
) {
	if interval <= 0 || source == nil {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := source.Credential(ctx); err != nil && ctx.Err() == nil && onError != nil {
				onError(err)
			}
		}
	}
}

func readTokenFile[T any](path string) (T, error) {
	var token T
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return token, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxTokenFileSize))
	if err := decoder.Decode(&token); err != nil {
		return token, fmt.Errorf("decode token file: %w", err)
	}
	return token, nil
}

func writeTokenFile(path string, token any) error {
	path = filepath.Clean(path)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create token directory: %w", err)
	}
	temporary, err := os.CreateTemp(dir, ".subscription-token-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary token file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("set token file permissions: %w", err)
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(token); err != nil {
		temporary.Close()
		return fmt.Errorf("encode token file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync token file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close token file: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace token file: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("set stored token permissions: %w", err)
	}
	return nil
}

func deleteTokenFile(path string) error {
	err := os.Remove(filepath.Clean(path))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func tokenReadError(backend string, err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s is not logged in", backend)
	}
	return fmt.Errorf("read %s token: %w", backend, err)
}
