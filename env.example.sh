# Copy this file to env.sh and fill in values, then run: source env.sh

# OpenAI
export TEST_OPENAI_API_KEY=""
export TEST_OPENAI_MODEL="gpt-5.2"
export TEST_OPENAI_API_BASE=""

# OpenAI-compatible custom endpoint
export TEST_OPENAI_CUSTOM_API_KEY=""
export TEST_OPENAI_CUSTOM_API_BASE=""
export TEST_OPENAI_CUSTOM_MODEL="gpt-5.2"

# Gemini (OpenAI-compatible in this repo)
export TEST_GEMINI_API_KEY=""
export TEST_GEMINI_MODEL="gemini-2.5-pro"
export TEST_GEMINI_API_BASE=""

# xAI (OpenAI-compatible)
export TEST_XAI_API_KEY=""
export TEST_XAI_MODEL="grok-3-mini"
export TEST_XAI_API_BASE=""

# Deepseek (OpenAI-compatible)
# NOTE: deepseek pricing is not in core/pricing.go; add it if you enable this test.
export TEST_DEEPSEEK_API_KEY=""
export TEST_DEEPSEEK_MODEL="deepseek-chat"
export TEST_DEEPSEEK_API_BASE=""

# Azure OpenAI
export TEST_AZURE_API_KEY=""
export TEST_AZURE_ENDPOINT=""
export TEST_AZURE_MODEL="o1-mini"

# Anthropic
export TEST_ANTHROPIC_API_KEY=""
export TEST_ANTHROPIC_MODEL="claude-3-5-sonnet"

# AWS Bedrock
export TEST_BEDROCK_AWS_KEY=""
export TEST_BEDROCK_AWS_SECRET=""
export TEST_BEDROCK_AWS_REGION="us-east-1"
export TEST_BEDROCK_MODEL="claude-3-5-sonnet"
export TEST_BEDROCK_MODEL_ARN="arn:aws:bedrock:us-east-1::foundation-model/anthropic.claude-3-5-sonnet-20240620-v1:0"

# Cloudflare Workers AI
export TEST_CLOUDFLARE_ACCOUNT_ID=""
export TEST_CLOUDFLARE_API_TOKEN=""
export TEST_CLOUDFLARE_TEXT_MODEL="@cf/openai/gpt-oss-120b"
export TEST_CLOUDFLARE_EMBEDDING_MODEL=""
export TEST_CLOUDFLARE_IMAGE_MODEL=""
export TEST_CLOUDFLARE_AUDIO_MODEL=""
export TEST_CLOUDFLARE_AUDIO_FILEPATH=""

# Jina (embeddings/classify/rerank)
export TEST_JINA_API_KEY=""
export TEST_JINA_API_BASE=""
