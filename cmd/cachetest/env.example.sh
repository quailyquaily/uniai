#!/usr/bin/env bash

# usage:
#   source cmd/cachetest/env.example.sh
#   # then export real provider credentials in your shell

export PROVIDER=openai_resp
export CACHE_SCENE=stats
export CACHE_TTL=5m
export CACHE_STREAM=false
export TIMEOUT_SECONDS=180

# Optional common overrides
# export MODEL=
# export PROMPT_CACHE_RETENTION=24h

# OpenAI / OpenAI Responses
# export OPENAI_API_KEY=
# export OPENAI_API_BASE=
# export OPENAI_MODEL=

# Azure OpenAI
# export AZURE_OPENAI_API_KEY=
# export AZURE_OPENAI_ENDPOINT=
# export AZURE_OPENAI_DEPLOYMENT=
# export AZURE_OPENAI_API_VERSION=2024-08-01-preview

# Anthropic
# export ANTHROPIC_API_KEY=
# export ANTHROPIC_MODEL=

# Bedrock
# export AWS_ACCESS_KEY_ID=
# export AWS_SECRET_ACCESS_KEY=
# export AWS_REGION=us-east-1
# export BEDROCK_MODEL_ARN=
