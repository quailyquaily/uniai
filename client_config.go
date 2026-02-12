package uniai

const (
	deepseekAPIBase = "https://api.deepseek.com"
	xaiAPIBase      = "https://api.x.ai/v1"
	groqAPIBase     = "https://api.groq.com/openai/v1"
)

// ClientConfigView is a non-sensitive view of runtime client config.
// It is intended for diagnostics and helper tools.
type ClientConfigView struct {
	Provider string
	Model    string
	APIBase  string
}

// GetConfig returns a non-sensitive client configuration snapshot.
// It intentionally excludes secrets such as API keys.
func (c *Client) GetConfig() ClientConfigView {
	provider := c.cfg.Provider
	if provider == "" {
		provider = "openai"
	}

	out := ClientConfigView{
		Provider: provider,
	}

	switch provider {
	case "openai", "openai_custom":
		out.Model = c.cfg.OpenAIModel
		out.APIBase = c.cfg.OpenAIAPIBase
	case "deepseek":
		out.Model = c.cfg.OpenAIModel
		out.APIBase = deepseekAPIBase
	case "xai":
		out.Model = c.cfg.OpenAIModel
		out.APIBase = xaiAPIBase
	case "groq":
		out.Model = c.cfg.OpenAIModel
		out.APIBase = groqAPIBase
	case "gemini":
		out.Model = c.cfg.GeminiModel
		if out.Model == "" {
			out.Model = c.cfg.OpenAIModel
		}
		out.APIBase = c.cfg.GeminiAPIBase
	case "azure":
		out.Model = c.cfg.AzureOpenAIModel
		out.APIBase = c.cfg.AzureOpenAIEndpoint
	case "anthropic":
		out.Model = c.cfg.AnthropicModel
	case "bedrock":
		out.Model = c.cfg.AwsBedrockModelArn
		out.APIBase = c.cfg.AwsRegion
	case "susanoo":
		out.Model = c.cfg.OpenAIModel
		out.APIBase = c.cfg.SusanooAPIBase
	case "cloudflare":
		// Cloudflare chat requires request model; callers usually set this in OpenAIModel.
		out.Model = c.cfg.OpenAIModel
		out.APIBase = c.cfg.CloudflareAPIBase
	default:
		out.Model = c.cfg.OpenAIModel
		out.APIBase = c.cfg.OpenAIAPIBase
	}

	return out
}
