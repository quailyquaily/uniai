package codex

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/lyricat/goutils/structs"
	openai "github.com/openai/openai-go/v3"
	"github.com/quailyquaily/uniai/chat"
	"github.com/quailyquaily/uniai/internal/httputil"
	openairesp "github.com/quailyquaily/uniai/providers/openai_resp"
	"github.com/quailyquaily/uniai/subscription"
	codexauth "github.com/quailyquaily/uniai/subscription/codex"
)

const DefaultAPIBase = codexauth.DefaultAPIBase

var ErrUnauthorized = errors.New("codex subscription authorization was rejected")

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
	upstreamKey  subscription.Credential
}

func New(cfg Config) (*Provider, error) {
	if cfg.CredentialSource == nil {
		return nil, fmt.Errorf("codex subscription credential source is required")
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
		return nil, fmt.Errorf("codex subscription provider is not configured")
	}
	if req == nil {
		return nil, fmt.Errorf("codex subscription request is nil")
	}
	prepared, err := prepareRequest(req)
	if err != nil {
		return nil, err
	}
	credential, err := p.source.Credential(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateCredential(credential); err != nil {
		return nil, err
	}
	result, err := p.chatWithCredential(ctx, prepared, credential)
	if !isHTTPStatus(err, http.StatusUnauthorized) {
		return sanitizeResultError(result, err)
	}

	rejectedAccessToken := strings.TrimSpace(credential.AccessToken)
	refreshed, refreshErr := p.source.RefreshRejected(ctx, rejectedAccessToken)
	if refreshErr != nil {
		return nil, fmt.Errorf("%w: credential refresh failed: %v", ErrUnauthorized, refreshErr)
	}
	if validateCredential(refreshed) != nil || strings.TrimSpace(refreshed.AccessToken) == rejectedAccessToken {
		return nil, ErrUnauthorized
	}
	result, err = p.chatWithCredential(ctx, prepared, refreshed)
	if isHTTPStatus(err, http.StatusUnauthorized) {
		return nil, ErrUnauthorized
	}
	return sanitizeResultError(result, err)
}

func (p *Provider) chatWithCredential(ctx context.Context, req *chat.Request, credential subscription.Credential) (*chat.Result, error) {
	provider, err := p.upstreamForCredential(credential)
	if err != nil {
		return nil, err
	}
	return provider.Chat(ctx, req)
}

func (p *Provider) upstreamForCredential(credential subscription.Credential) (*openairesp.Provider, error) {
	credential.AccessToken = strings.TrimSpace(credential.AccessToken)
	credential.AccountID = strings.TrimSpace(credential.AccountID)
	p.upstreamMu.RLock()
	if p.upstream != nil && p.upstreamKey == credential {
		provider := p.upstream
		p.upstreamMu.RUnlock()
		return provider, nil
	}
	p.upstreamMu.RUnlock()

	p.upstreamMu.Lock()
	defer p.upstreamMu.Unlock()
	if p.upstream != nil && p.upstreamKey == credential {
		return p.upstream, nil
	}
	headers := httputil.CloneHeaders(p.headers)
	if headers == nil {
		headers = map[string]string{}
	}
	if accountID := credential.AccountID; accountID != "" {
		headers["ChatGPT-Account-ID"] = accountID
	}
	provider, err := openairesp.New(openairesp.Config{
		APIKey:       credential.AccessToken,
		BaseURL:      DefaultAPIBase,
		DefaultModel: p.defaultModel,
		Headers:      headers,
		HTTPClient:   p.httpClient,
		Debug:        p.debug,
		OpenAICodex:  true,
	})
	if err != nil {
		return nil, err
	}
	p.upstream = provider
	p.upstreamKey = credential
	return provider, nil
}

func prepareRequest(req *chat.Request) (*chat.Request, error) {
	out := *req
	out.Messages = append([]chat.Message(nil), req.Messages...)
	out.Options.OpenAI = cloneJSONMap(req.Options.OpenAI)

	instructions := make([]string, 0, 2)
	if existing := strings.TrimSpace(out.Options.OpenAI.GetString("instructions")); existing != "" {
		instructions = append(instructions, existing)
	}
	messages := make([]chat.Message, 0, len(out.Messages))
	for _, message := range out.Messages {
		if strings.EqualFold(strings.TrimSpace(message.Role), chat.RoleSystem) {
			text, err := chat.MessageText(message)
			if err != nil {
				return nil, fmt.Errorf("codex subscription system message: %w", err)
			}
			if text = strings.TrimSpace(text); text != "" {
				instructions = append(instructions, text)
			}
			continue
		}
		messages = append(messages, message)
	}
	if len(instructions) == 0 {
		return nil, fmt.Errorf("codex subscription requires instructions or a system message")
	}
	out.Messages = messages
	out.Options.OpenAI["instructions"] = strings.Join(instructions, "\n\n")
	out.Options.OpenAI["store"] = false
	if out.Options.OnStream == nil {
		// The subscription endpoint is streaming-only. openai_resp still
		// aggregates the completed event into an ordinary chat.Result.
		out.Options.OnStream = func(chat.StreamEvent) error { return nil }
	}
	return &out, nil
}

func cloneJSONMap(input structs.JSONMap) structs.JSONMap {
	output := make(structs.JSONMap, len(input)+2)
	for key, value := range input {
		output[key] = value
	}
	return output
}

func sanitizeHeaders(headers map[string]string) map[string]string {
	output := httputil.CloneHeaders(headers)
	for key := range output {
		if strings.EqualFold(key, "authorization") || strings.EqualFold(key, "chatgpt-account-id") {
			delete(output, key)
		}
	}
	return output
}

func validateCredential(credential subscription.Credential) error {
	if strings.TrimSpace(credential.AccessToken) == "" {
		return fmt.Errorf("codex subscription access token is empty")
	}
	return nil
}

func isHTTPStatus(err error, status int) bool {
	var apiErr *openai.Error
	return errors.As(err, &apiErr) && apiErr.StatusCode == status
}

func sanitizeResultError(result *chat.Result, err error) (*chat.Result, error) {
	if err == nil {
		return result, nil
	}
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		return nil, fmt.Errorf("codex subscription inference failed with HTTP %d", apiErr.StatusCode)
	}
	return nil, err
}
