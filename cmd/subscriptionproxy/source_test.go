package main

import (
	"context"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quailyquaily/uniai/subscription"
	codexauth "github.com/quailyquaily/uniai/subscription/codex"
	xaiauth "github.com/quailyquaily/uniai/subscription/xai"
)

func TestTokenFileRoundTripUsesPrivatePermissions(t *testing.T) {
	path := t.TempDir() + "/credentials.json"
	want := codexauth.Token{AccessToken: "access", RefreshToken: "refresh", AccountID: "account"}
	if err := writeTokenFile(path, want); err != nil {
		t.Fatalf("writeTokenFile() error = %v", err)
	}
	got, err := readTokenFile[codexauth.Token](path)
	if err != nil {
		t.Fatalf("readTokenFile() error = %v", err)
	}
	if got.AccessToken != want.AccessToken || got.RefreshToken != want.RefreshToken || got.AccountID != want.AccountID {
		t.Fatalf("token = %+v", got)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %o, want 0600", info.Mode().Perm())
		}
	}
}

func TestCodexCredentialSourceRefreshesExpiredTokenOnce(t *testing.T) {
	now := time.Date(2026, 8, 24, 6, 0, 0, 0, time.UTC)
	path := t.TempDir() + "/codex.json"
	createdAt := now.Add(-24 * time.Hour)
	if err := writeTokenFile(path, codexauth.Token{
		AccessToken: "access-old", RefreshToken: "refresh-old", AccountID: "account",
		PlanType: "plus", ExpiresAt: now.Add(-time.Minute), CreatedAt: createdAt,
	}); err != nil {
		t.Fatal(err)
	}
	source := newCodexCredentialSource(path, codexauth.OAuthConfig{})
	source.now = func() time.Time { return now }
	var refreshes atomic.Int32
	source.refresh = func(context.Context, codexauth.OAuthConfig, string) (codexauth.Token, error) {
		refreshes.Add(1)
		return codexauth.Token{
			AccessToken: "access-new", RefreshToken: "refresh-new", ExpiresAt: now.Add(time.Hour),
		}, nil
	}

	var wg sync.WaitGroup
	errors := make(chan error, 2)
	credentials := make(chan subscription.Credential, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			credential, err := source.Credential(context.Background())
			errors <- err
			credentials <- credential
		}()
	}
	wg.Wait()
	close(errors)
	close(credentials)
	for err := range errors {
		if err != nil {
			t.Fatalf("Credential() error = %v", err)
		}
	}
	for credential := range credentials {
		if credential.AccessToken != "access-new" || credential.AccountID != "account" {
			t.Fatalf("credential = %+v", credential)
		}
	}
	if refreshes.Load() != 1 {
		t.Fatalf("refreshes = %d", refreshes.Load())
	}
	stored, err := readTokenFile[codexauth.Token](path)
	if err != nil {
		t.Fatal(err)
	}
	if stored.PlanType != "plus" || stored.AccountID != "account" || !stored.CreatedAt.Equal(createdAt) {
		t.Fatalf("stored metadata = %+v", stored)
	}
}

func TestCodexCredentialSourceCachesLoadedToken(t *testing.T) {
	now := time.Date(2026, 8, 24, 6, 30, 0, 0, time.UTC)
	path := t.TempDir() + "/codex.json"
	if err := writeTokenFile(path, codexauth.Token{
		AccessToken: "access", RefreshToken: "refresh", AccountID: "account", ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	source := newCodexCredentialSource(path, codexauth.OAuthConfig{})
	source.now = func() time.Time { return now }
	if _, err := source.Credential(context.Background()); err != nil {
		t.Fatalf("first Credential() error = %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	credential, err := source.Credential(context.Background())
	if err != nil {
		t.Fatalf("cached Credential() error = %v", err)
	}
	if credential.AccessToken != "access" || credential.AccountID != "account" {
		t.Fatalf("credential = %+v", credential)
	}
}

func TestCodexRefreshRejectedUsesNewerStoredToken(t *testing.T) {
	now := time.Date(2026, 8, 24, 7, 0, 0, 0, time.UTC)
	path := t.TempDir() + "/codex.json"
	if err := writeTokenFile(path, codexauth.Token{
		AccessToken: "access-new", RefreshToken: "refresh-new", AccountID: "account", ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	source := newCodexCredentialSource(path, codexauth.OAuthConfig{})
	source.now = func() time.Time { return now }
	source.refresh = func(context.Context, codexauth.OAuthConfig, string) (codexauth.Token, error) {
		t.Fatal("refresh must not be called")
		return codexauth.Token{}, nil
	}
	credential, err := source.RefreshRejected(context.Background(), "access-old")
	if err != nil {
		t.Fatalf("RefreshRejected() error = %v", err)
	}
	if credential.AccessToken != "access-new" {
		t.Fatalf("credential = %+v", credential)
	}
}

func TestXAICredentialSourceRefreshesAndPersistsRotatedToken(t *testing.T) {
	now := time.Date(2026, 8, 24, 8, 0, 0, 0, time.UTC)
	path := t.TempDir() + "/xai.json"
	if err := writeTokenFile(path, xaiauth.Token{
		AccessToken: "access-old", RefreshToken: "refresh-old", TokenType: "Bearer",
		Scope: "scope", ExpiresAt: now.Add(-time.Minute), CreatedAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	source := newXAICredentialSource(path, xaiauth.OAuthConfig{})
	source.now = func() time.Time { return now }
	source.refresh = func(context.Context, xaiauth.OAuthConfig, string) (xaiauth.Token, error) {
		return xaiauth.Token{
			AccessToken: "access-new", RefreshToken: "refresh-new", ExpiresAt: now.Add(time.Hour),
		}, nil
	}
	credential, err := source.Credential(context.Background())
	if err != nil {
		t.Fatalf("Credential() error = %v", err)
	}
	if credential.AccessToken != "access-new" {
		t.Fatalf("credential = %+v", credential)
	}
	stored, err := readTokenFile[xaiauth.Token](path)
	if err != nil {
		t.Fatal(err)
	}
	if stored.RefreshToken != "refresh-new" || stored.TokenType != "Bearer" || stored.Scope != "scope" {
		t.Fatalf("stored token = %+v", stored)
	}
}

func TestXAICredentialSourceCachesLoadedToken(t *testing.T) {
	now := time.Date(2026, 8, 24, 8, 30, 0, 0, time.UTC)
	path := t.TempDir() + "/xai.json"
	if err := writeTokenFile(path, xaiauth.Token{
		AccessToken: "access", RefreshToken: "refresh", ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	source := newXAICredentialSource(path, xaiauth.OAuthConfig{})
	source.now = func() time.Time { return now }
	if _, err := source.Credential(context.Background()); err != nil {
		t.Fatalf("first Credential() error = %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	credential, err := source.Credential(context.Background())
	if err != nil {
		t.Fatalf("cached Credential() error = %v", err)
	}
	if credential.AccessToken != "access" {
		t.Fatalf("credential = %+v", credential)
	}
}

func TestRefreshLoopChecksCredentialsPeriodically(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := &countingCredentialSource{called: make(chan struct{}, 2)}
	done := make(chan struct{})
	go func() {
		runRefreshLoop(ctx, 2*time.Millisecond, source, nil)
		close(done)
	}()
	select {
	case <-source.called:
	case <-time.After(time.Second):
		t.Fatal("periodic credential check did not run")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("refresh loop did not stop with context")
	}
}

type countingCredentialSource struct {
	called chan struct{}
}

func (s *countingCredentialSource) Credential(context.Context) (subscription.Credential, error) {
	select {
	case s.called <- struct{}{}:
	default:
	}
	return subscription.Credential{AccessToken: "access"}, nil
}

func (s *countingCredentialSource) RefreshRejected(context.Context, string) (subscription.Credential, error) {
	return subscription.Credential{AccessToken: "access"}, nil
}
