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

## Commit & Pull Request Guidelines
- No Git metadata is present in this checkout, so follow conventions from the module root if available.
- If no local conventions are found, use Conventional Commits (`feat:`, `fix:`, `refactor:`, `test:`, `chore:`) with concise, imperative subjects.
- PRs should include a short description, test commands run (with output), and note any config or provider behavior changes.

## Security & Configuration Tips
- API keys and endpoints are configured via `Config`; never commit secrets to the repo.
- When adding providers, validate required config fields and document any new settings.
