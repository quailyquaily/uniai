package uniai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/quailyquaily/uniai/internal/httputil"
)

// ModelInfo describes a model returned by the provider's live catalog.
// Optional fields remain empty or zero when the provider does not report them.
// A catalog entry does not guarantee access or support for a particular task.
type ModelInfo struct {
	ID               string          `json:"id"`
	DisplayName      string          `json:"display_name,omitempty"`
	Description      string          `json:"description,omitempty"`
	OwnedBy          string          `json:"owned_by,omitempty"`
	InputTokenLimit  int             `json:"input_token_limit,omitempty"`
	OutputTokenLimit int             `json:"output_token_limit,omitempty"`
	Raw              json.RawMessage `json:"raw,omitempty"`
}

// ListModels fetches the provider's model catalog, following pagination where
// supported. An empty provider selects Config.Provider, defaulting to openai.
// It preserves upstream order and metadata, without filtering by task or adding
// entries from the pricing catalog. Errors return nil rather than partial lists.
func (c *Client) ListModels(ctx context.Context, provider string) ([]ModelInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if provider == "" {
		provider = c.cfg.Provider
	}
	if provider == "" {
		provider = "openai"
	}
	base, path, key, format := c.cfg.OpenAIAPIBase, "/models", c.cfg.OpenAIAPIKey, "openai"
	headers := make(http.Header)
	switch provider {
	case "openai", "openai_resp":
	case "openai_codex":
		if c.cfg.CodexSubscription != nil {
			return nil, fmt.Errorf("ListModels is not supported for openai_codex subscription credentials")
		}
	case "deepseek":
		base = deepseekAPIBase
	case "xai":
		base = xaiAPIBase
	case "groq":
		base = groqAPIBase
	case "meta":
		base = resolveMetaAPIBase(base)
	case "sakana":
		base = resolveSakanaAPIBase(base)
	case "anthropic":
		base, key, format = c.cfg.AnthropicAPIBase, c.cfg.AnthropicAPIKey, provider
		headers.Set("x-api-key", key)
		headers.Set("anthropic-version", "2023-06-01")
	case "gemini":
		base, path, key, format = c.cfg.GeminiAPIBase, "/v1beta/models", c.cfg.GeminiAPIKey, provider
		if key == "" {
			key = c.cfg.OpenAIAPIKey
		}
		headers.Set("x-goog-api-key", key)
	case "cloudflare":
		if strings.TrimSpace(c.cfg.CloudflareAccountID) == "" {
			return nil, fmt.Errorf("cloudflare account id is required for ListModels")
		}
		base, key, format = c.cfg.CloudflareAPIBase, c.cfg.CloudflareAPIToken, provider
		path = "/accounts/" + url.PathEscape(c.cfg.CloudflareAccountID) + "/ai/models/search"
	default:
		return nil, fmt.Errorf("ListModels is not supported for provider %q", provider)
	}
	if strings.TrimSpace(key) == "" {
		return nil, fmt.Errorf("%s API key or token is required for ListModels", provider)
	}
	if format == "openai" || format == "cloudflare" {
		headers.Set("Authorization", "Bearer "+key)
	}
	headers.Set("Accept", "application/json")
	httputil.ApplyHeaders(headers, c.cfg.ModelsHeaders)
	u, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("%s models base URL must be HTTP(S) without credentials, query or fragment", provider)
	}
	u, err = url.Parse(u.String() + path)
	if err != nil {
		return nil, fmt.Errorf("invalid %s models URL", provider)
	}
	client := c.cfg.ModelsHTTPClient
	if client == nil {
		client = httputil.ClientForContext(ctx)
	}
	// Keep credentials on the configured origin without mutating a shared client.
	modelsClient := *client
	checkRedirect := client.CheckRedirect
	modelsClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != u.Scheme || !strings.EqualFold(req.URL.Host, u.Host) || req.URL.User != nil {
			return fmt.Errorf("%s ListModels: redirect must stay on the configured origin without URL credentials", provider)
		}
		if checkRedirect != nil {
			return checkRedirect(req, via)
		}
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		return nil
	}

	models := make([]ModelInfo, 0)
	seen := make(map[string]bool)
	cursor := ""
	for pageNumber := 1; ; pageNumber++ {
		query := make(url.Values)
		switch format {
		case "anthropic":
			query.Set("limit", "1000")
			if cursor != "" {
				query.Set("after_id", cursor)
			}
		case "gemini":
			query.Set("pageSize", "1000")
			if cursor != "" {
				query.Set("pageToken", cursor)
			}
		case "cloudflare":
			query.Set("page", strconv.Itoa(pageNumber))
			query.Set("per_page", "100")
		}
		u.RawQuery = query.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("%s ListModels request: %w", provider, err)
		}
		req.Header = headers.Clone()
		resp, err := modelsClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("%s ListModels: %w", provider, err)
		}
		data, err := httputil.ReadBody(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("%s ListModels response: %w", provider, err)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("%s ListModels: status %d: %s", provider, resp.StatusCode, strings.TrimSpace(string(data)))
		}
		var page *struct {
			Data          []json.RawMessage `json:"data"`
			Models        []json.RawMessage `json:"models"`
			Result        []json.RawMessage `json:"result"`
			HasMore       bool              `json:"has_more"`
			LastID        string            `json:"last_id"`
			NextPageToken string            `json:"nextPageToken"`
			Success       *bool             `json:"success"`
			Error         json.RawMessage   `json:"error"`
			Errors        json.RawMessage   `json:"errors"`
			ResultInfo    struct {
				TotalPages int `json:"total_pages"`
			} `json:"result_info"`
		}
		if err := json.Unmarshal(data, &page); err != nil || page == nil {
			return nil, fmt.Errorf("%s ListModels: invalid JSON response", provider)
		}
		if len(page.Error) > 0 && string(page.Error) != "null" {
			return nil, fmt.Errorf("%s ListModels: upstream error: %s", provider, page.Error)
		}
		items := page.Data
		switch format {
		case "gemini":
			items = page.Models
		case "cloudflare":
			if page.Success == nil || !*page.Success {
				return nil, fmt.Errorf("cloudflare ListModels failed: %s", page.Errors)
			}
			items = page.Result
		}
		if items == nil && format != "gemini" {
			return nil, fmt.Errorf("%s ListModels: missing model array", provider)
		}
		for _, raw := range items {
			model, err := decodeListedModel(raw, format)
			if err != nil {
				return nil, fmt.Errorf("%s ListModels: %w", provider, err)
			}
			models = append(models, model)
		}
		switch format {
		case "anthropic":
			if !page.HasMore {
				return models, nil
			}
			cursor = page.LastID
			if cursor == "" || len(items) == 0 {
				return nil, fmt.Errorf("anthropic ListModels: missing pagination cursor or models")
			}
		case "gemini":
			cursor = page.NextPageToken
			if cursor == "" {
				return models, nil
			}
		case "cloudflare":
			if len(items) == 0 || (page.ResultInfo.TotalPages > 0 && pageNumber >= page.ResultInfo.TotalPages) {
				return models, nil
			}
			// Some catalog responses omit result_info; fetch until an empty page.
			cursor = ""
			for _, model := range models[len(models)-len(items):] {
				cursor += model.ID + "\x00"
			}
		default:
			return models, nil
		}
		if seen[cursor] {
			return nil, fmt.Errorf("%s ListModels: repeated pagination cursor or page", provider)
		}
		seen[cursor] = true
	}
}

func decodeListedModel(raw json.RawMessage, format string) (ModelInfo, error) {
	var model ModelInfo
	if err := json.Unmarshal(raw, &model); err != nil {
		return model, fmt.Errorf("invalid model metadata: %w", err)
	}
	switch format {
	case "anthropic":
		var limits struct {
			Input  int `json:"max_input_tokens"`
			Output int `json:"max_tokens"`
		}
		if err := json.Unmarshal(raw, &limits); err != nil {
			return model, fmt.Errorf("invalid model token limits: %w", err)
		}
		model.InputTokenLimit, model.OutputTokenLimit = limits.Input, limits.Output
	case "gemini", "cloudflare":
		var native struct {
			Name        string `json:"name"`
			DisplayName string `json:"displayName"`
			Input       int    `json:"inputTokenLimit"`
			Output      int    `json:"outputTokenLimit"`
		}
		if err := json.Unmarshal(raw, &native); err != nil {
			return model, fmt.Errorf("invalid model metadata: %w", err)
		}
		model.ID = native.Name
		if format == "gemini" {
			model.ID = strings.TrimPrefix(model.ID, "models/")
			model.DisplayName = native.DisplayName
			model.InputTokenLimit, model.OutputTokenLimit = native.Input, native.Output
		}
	}
	if strings.TrimSpace(model.ID) == "" {
		return model, fmt.Errorf("model is missing its callable ID")
	}
	model.Raw = raw
	return model, nil
}
