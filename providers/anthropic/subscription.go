package anthropic

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/quailyquaily/uniai/chat"
	"github.com/quailyquaily/uniai/internal/diag"
	"github.com/quailyquaily/uniai/internal/httputil"
	"github.com/quailyquaily/uniai/subscription"
	"github.com/quailyquaily/uniai/subscription/claude/claudecode"
)

var ErrSubscriptionUnauthorized = errors.New("Claude subscription authorization was rejected")

// SubscriptionError reports status without exposing upstream response bodies.
type SubscriptionError struct{ StatusCode int }

func (e *SubscriptionError) Error() string {
	return fmt.Sprintf("Claude subscription inference failed with HTTP %d", e.StatusCode)
}

func (p *Provider) doSubscription(ctx context.Context, data []byte, debugFn chat.DebugFn) (*http.Response, error) {
	credential, err := p.cfg.CredentialSource.Credential(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateSubscriptionCredential(credential); err != nil {
		return nil, err
	}
	data, session, err := p.cfg.ClaudeCode.Prepare(data, credential.AccountID)
	if err != nil {
		return nil, err
	}
	diag.LogText(p.cfg.Debug, debugFn, "claude_oauth.chat.request", string(data))
	client := *httputil.ClientForContext(ctx)
	if p.cfg.HTTPClient != nil {
		client = *p.cfg.HTTPClient
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, claudecode.MessagesURL, bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		httputil.ApplyHeaders(req.Header, p.cfg.Headers)
		if err := p.cfg.ClaudeCode.ApplyHeaders(req.Header, credential.AccessToken, session); err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("Claude subscription inference request failed")
		}
		if resp.StatusCode == http.StatusOK {
			return resp, nil
		}
		// Never log or return upstream error bodies; they can echo credentials.
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			return nil, &SubscriptionError{StatusCode: resp.StatusCode}
		}
		if attempt != 0 {
			return nil, ErrSubscriptionUnauthorized
		}
		rejected := strings.TrimSpace(credential.AccessToken)
		refreshed, err := p.cfg.CredentialSource.RefreshRejected(ctx, rejected)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("%w: credential refresh failed", ErrSubscriptionUnauthorized)
		}
		if validateSubscriptionCredential(refreshed) != nil || strings.TrimSpace(refreshed.AccessToken) == rejected || refreshed.AccountID != credential.AccountID {
			return nil, ErrSubscriptionUnauthorized
		}
		credential = refreshed
	}
	return nil, ErrSubscriptionUnauthorized
}

func validateSubscriptionCredential(credential subscription.Credential) error {
	if strings.TrimSpace(credential.AccessToken) == "" {
		return fmt.Errorf("Claude subscription access token is empty")
	}
	if strings.TrimSpace(credential.AccountID) == "" {
		return fmt.Errorf("Claude subscription account ID is empty; log in again")
	}
	return nil
}
