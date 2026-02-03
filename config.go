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
	AzureOpenAIAPIKey   string
	AzureOpenAIEndpoint string
	AzureOpenAIModel    string

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
}
