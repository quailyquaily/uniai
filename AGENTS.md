# Repository Guidelines

## Project Structure & Module Organization
- Root package `uniai` (`client.go`, `config.go`, `exports.go`) wires shared configuration and entrypoints.
- Feature packages live in `chat/`, `classify/`, `embedding/`, `image/`, and `rerank/` with their own request/response types and client helpers.
- Provider implementations are in `providers/` (e.g., `openai/`, `azure/`, `anthropic/`, `bedrock/`, `cloudflare/`).
- Unit tests are `*_test.go` files (currently in `chat/` and `providers/openai/`).

## Build, Test, and Development Commands
This directory is a Go package (import path `github.com/quailyquaily/uniai`) and expects to be within a Go module. Run commands from the module root that contains `go.mod`.
- `go test ./...` — run all unit tests.
- `go test ./... -run TestName` — run a specific test.
- `go vet ./...` — static checks for common issues.
- `gofmt -w .` — format all Go files (tabs, standard Go style).

## Coding Style & Naming Conventions
- Go formatting is required (`gofmt`); indentation uses tabs per Go standards.
- Package names are lowercase; exported identifiers use PascalCase, unexported use camelCase.
- File names are lowercase with underscores when needed (e.g., `types.go`, `openai_test.go`).
- New provider integrations should live under `providers/<name>/` with a config struct and a constructor.

## Testing Guidelines
- Use Go’s `testing` package; name tests `TestXxx` in `*_test.go`.
- Keep tests deterministic and offline; prefer pure request/response mapping checks.
- Add coverage when changing option handling or provider request mapping logic.

## New Model Compatibility Checks
Every model addition or upgrade must include a compatibility inspection, not just a model ID or pricing update.

- Check current official documentation for the exact API model ID, aliases, supported endpoints, defaults, and parameter constraints. Record the source links and check date in the relevant model, cache, or pricing documentation. Do not infer a model's rules from its name or another model in the same family.
- Trace option handling from public options and raw provider options through validation, model overlays, and final request serialization. Check precedence and conflict handling; raw options must not bypass model restrictions, and request construction must not mutate caller-owned options.
- Inspect each supported route separately: Chat Completions, Responses, native Anthropic, and Bedrock where applicable. Check reasoning effort values and mappings, default effort, thinking modes, manual budgets, sampling and logprob fields, and the output-token limit field. Do not assume that changing providers preserves behavior. Treat Azure deployment names and subscription routes according to their own contracts.
- Verify tool support, forced tool choice, endpoint requirements, and replay of reasoning or signed content. Check both blocking and streaming paths when response handling is affected.
- Check cache modes, request and breakpoint TTLs, legacy retention conversion, and cache usage fields against current documentation. Verify prices for cache reads, writes by TTL, and long-context tiers, including threshold boundaries. State any limits of local cost estimation and any required pricing overrides.
- Use focused offline request/response mapping tests for changed behavior, including relevant public/raw option conflicts and invalid values. Reuse existing test tables where practical; do not add tests merely for documentation changes or call paid APIs to validate mappings.
- Report confirmed compatibility changes, existing gaps, and unresolved endpoint behavior separately. Passing local tests does not establish upstream API acceptance. Keep README, cache documentation, and the pricing catalog consistent with the implemented behavior and its limits.

## Commit & Pull Request Guidelines
- No Git metadata is present in this checkout, so follow conventions from the module root if available.
- If no local conventions are found, use Conventional Commits (`feat:`, `fix:`, `refactor:`, `test:`, `chore:`) with concise, imperative subjects.
- PRs should include a short description, test commands run (with output), and note any config or provider behavior changes.

## Security & Configuration Tips
- API keys and endpoints are configured via `Config`; never commit secrets to the repo.
- When adding providers, validate required config fields and document any new settings.
