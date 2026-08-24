package xaioauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	openai "github.com/openai/openai-go/v3"
	"github.com/quailyquaily/uniai/chat"
	"github.com/quailyquaily/uniai/internal/httputil"
	openairesp "github.com/quailyquaily/uniai/providers/openai_resp"
	"github.com/quailyquaily/uniai/subscription"
	xaiauth "github.com/quailyquaily/uniai/subscription/xai"
)

const DefaultAPIBase = xaiauth.DefaultAPIBase

var (
	ErrUnauthorized     = errors.New("xAI subscription authorization was rejected")
	ErrEntitlement      = errors.New("xAI subscription inference is unavailable for this account")
	ErrRateLimited      = errors.New("xAI subscription inference was rate limited")
	ErrModelUnavailable = errors.New("xAI subscription model is unavailable for this account")
)

type Config struct {
	CredentialSource subscription.CredentialSource
	DefaultModel     string
	Headers          map[string]string
	HTTPClient       *http.Client
	Debug            bool
}

type Provider struct {
	source       subscription.CredentialSource
	defaultModel string
	headers      map[string]string
	httpClient   *http.Client
	debug        bool
	upstreamMu   sync.RWMutex
	upstream     *openairesp.Provider
	upstreamKey  string
}

func New(cfg Config) (*Provider, error) {
	if cfg.CredentialSource == nil {
		return nil, fmt.Errorf("xAI subscription credential source is required")
	}
	return &Provider{
		source:       cfg.CredentialSource,
		defaultModel: strings.TrimSpace(cfg.DefaultModel),
		headers:      sanitizeHeaders(cfg.Headers),
		httpClient:   cfg.HTTPClient,
		debug:        cfg.Debug,
	}, nil
}

func (p *Provider) Chat(ctx context.Context, req *chat.Request) (*chat.Result, error) {
	if p == nil || p.source == nil {
		return nil, fmt.Errorf("xAI subscription provider is not configured")
	}
	if req == nil {
		return nil, fmt.Errorf("xAI subscription request is nil")
	}
	credential, err := p.source.Credential(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateCredential(credential); err != nil {
		return nil, err
	}
	result, err := p.chatWithCredential(ctx, req, credential)
	if !isHTTPStatus(err, http.StatusUnauthorized) {
		return sanitizeResultError(result, err, req.Model)
	}

	rejectedAccessToken := strings.TrimSpace(credential.AccessToken)
	refreshed, refreshErr := p.source.RefreshRejected(ctx, rejectedAccessToken)
	if refreshErr != nil {
		return nil, fmt.Errorf("%w: credential refresh failed: %v", ErrUnauthorized, refreshErr)
	}
	if validateCredential(refreshed) != nil || strings.TrimSpace(refreshed.AccessToken) == rejectedAccessToken {
		return nil, ErrUnauthorized
	}
	result, err = p.chatWithCredential(ctx, req, refreshed)
	if isHTTPStatus(err, http.StatusUnauthorized) {
		return nil, ErrUnauthorized
	}
	return sanitizeResultError(result, err, req.Model)
}

func (p *Provider) chatWithCredential(ctx context.Context, req *chat.Request, credential subscription.Credential) (*chat.Result, error) {
	prepared := *req
	prepared.InferenceProvider = "xai_oauth"
	provider, err := p.upstreamForCredential(credential.AccessToken)
	if err != nil {
		return nil, err
	}
	return provider.Chat(ctx, &prepared)
}

func (p *Provider) upstreamForCredential(accessToken string) (*openairesp.Provider, error) {
	accessToken = strings.TrimSpace(accessToken)
	p.upstreamMu.RLock()
	if p.upstream != nil && p.upstreamKey == accessToken {
		provider := p.upstream
		p.upstreamMu.RUnlock()
		return provider, nil
	}
	p.upstreamMu.RUnlock()

	p.upstreamMu.Lock()
	defer p.upstreamMu.Unlock()
	if p.upstream != nil && p.upstreamKey == accessToken {
		return p.upstream, nil
	}
	provider, err := openairesp.New(openairesp.Config{
		APIKey:       accessToken,
		BaseURL:      DefaultAPIBase,
		DefaultModel: p.defaultModel,
		Headers:      p.headers,
		HTTPClient:   p.httpClient,
		Debug:        p.debug,
	})
	if err != nil {
		return nil, err
	}
	p.upstream = provider
	p.upstreamKey = accessToken
	return provider, nil
}

func sanitizeResultError(result *chat.Result, err error, model string) (*chat.Result, error) {
	if err == nil {
		return result, nil
	}
	var apiErr *openai.Error
	if !errors.As(err, &apiErr) {
		return nil, err
	}
	switch apiErr.StatusCode {
	case http.StatusUnauthorized:
		return nil, ErrUnauthorized
	case http.StatusForbidden:
		return nil, ErrEntitlement
	case http.StatusTooManyRequests:
		if retryAfter := formatRetryAfter(apiErr.Response); retryAfter != "" {
			return nil, fmt.Errorf("%w; retry after %s", ErrRateLimited, retryAfter)
		}
		return nil, ErrRateLimited
	case http.StatusNotFound:
		if model = strings.TrimSpace(model); model != "" {
			return nil, fmt.Errorf("%w: %s", ErrModelUnavailable, model)
		}
		return nil, ErrModelUnavailable
	default:
		return nil, fmt.Errorf("xAI subscription inference failed with HTTP %d", apiErr.StatusCode)
	}
}

func formatRetryAfter(response *http.Response) string {
	if response == nil {
		return ""
	}
	if raw := strings.TrimSpace(response.Header.Get("Retry-After-Ms")); raw != "" {
		if milliseconds, err := strconv.ParseFloat(raw, 64); err == nil && milliseconds > 0 {
			return time.Duration(milliseconds * float64(time.Millisecond)).String()
		}
	}
	raw := strings.TrimSpace(response.Header.Get("Retry-After"))
	if seconds, err := strconv.ParseFloat(raw, 64); err == nil && seconds > 0 {
		return time.Duration(seconds * float64(time.Second)).String()
	}
	if deadline, err := http.ParseTime(raw); err == nil {
		if wait := time.Until(deadline); wait > 0 {
			return wait.Round(time.Second).String()
		}
	}
	return ""
}

func sanitizeHeaders(headers map[string]string) map[string]string {
	output := httputil.CloneHeaders(headers)
	for key := range output {
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "authorization", "proxy-authorization", "x-api-key", "api-key":
			delete(output, key)
		}
	}
	return output
}

func validateCredential(credential subscription.Credential) error {
	if strings.TrimSpace(credential.AccessToken) == "" {
		return fmt.Errorf("xAI subscription access token is empty")
	}
	return nil
}

func isHTTPStatus(err error, status int) bool {
	var apiErr *openai.Error
	return errors.As(err, &apiErr) && apiErr.StatusCode == status
}
