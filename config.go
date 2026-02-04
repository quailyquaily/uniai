package uniai

// Config provides shared configuration for uniai clients.
// Fields are optional and used by specific providers/features.
type Config struct {
	Provider string
	Debug    bool

	// OpenAI / OpenAI-compatible
	OpenAIAPIKey  string
	OpenAIAPIBase string
	OpenAIModel   string

	// Azure OpenAI
	AzureOpenAIAPIKey     string
	AzureOpenAIEndpoint   string
	AzureOpenAIModel      string
	AzureOpenAIAPIVersion string

	// Anthropic
	AnthropicAPIKey string
	AnthropicModel  string

	// AWS Bedrock
	AwsKey             string
	AwsSecret          string
	AwsRegion          string
	AwsBedrockModelArn string

	// Susanoo
	SusanooAPIBase string
	SusanooAPIKey  string

	// Embeddings / Images / Rerank / Classify
	OpenAIEmbeddingModel      string
	AzureOpenAIEmbeddingModel string
	AwsBedrockEmbeddingModel  string

	JinaAPIKey    string
	JinaAPIBase   string
	GeminiAPIKey  string
	GeminiAPIBase string
	GeminiModel   string
}

const (
	DefaultOpenAIAPIBase = "https://api.openai.com/v1"
	DefaultJinaAPIBase   = "https://api.jina.ai"
	DefaultGeminiAPIBase = "https://generativelanguage.googleapis.com"
)

func (cfg Config) withDefaults() Config {
	if cfg.OpenAIAPIBase == "" {
		cfg.OpenAIAPIBase = DefaultOpenAIAPIBase
	}
	if cfg.JinaAPIBase == "" {
		cfg.JinaAPIBase = DefaultJinaAPIBase
	}
	if cfg.GeminiAPIBase == "" {
		cfg.GeminiAPIBase = DefaultGeminiAPIBase
	}
	return cfg
}
