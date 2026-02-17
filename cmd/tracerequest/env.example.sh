#!/usr/bin/env bash

# copy: cp cmd/tracerequest/env.example.sh .env.tracerequest.sh
# usage: source .env.tracerequest.sh

# Common
export PROVIDER="openai"
export SCENE="none"
export MODEL=""
export PROMPT="Reply with exactly: tracerequest-demo"
export TIMEOUT_SECONDS="90"
export DUMP_DIR="dump"

# OpenAI
export OPENAI_API_KEY=""
export OPENAI_API_BASE=""
export OPENAI_MODEL=""

# Cloudflare Workers AI
export CLOUDFLARE_ACCOUNT_ID=""
export CLOUDFLARE_API_TOKEN=""
export CLOUDFLARE_MODEL=""
export CLOUDFLARE_API_BASE=""

# Gemini
export GEMINI_API_KEY=""
export GEMINI_MODEL=""
export GEMINI_API_BASE=""
