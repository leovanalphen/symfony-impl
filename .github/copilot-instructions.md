# Copilot Instructions

## Project Overview

Symphony is a long-running Go automation daemon that polls [Linear](https://linear.app) for eligible issues, creates isolated per-issue workspaces, and drives a [Codex](https://openai.com/codex) coding-agent session for each one. It is a Go implementation of the [Symphony specification](https://github.com/openai/symphony/blob/main/SPEC.md).

## Tech Stack

- **Language**: Go 1.25+
- **Template engine**: `github.com/osteele/liquid` (Liquid templates)
- **Config/front matter**: `gopkg.in/yaml.v3`
- **File watching**: `github.com/fsnotify/fsnotify`
- **Logging**: standard library `log/slog` (structured JSON to stderr)
- **Testing**: standard library `testing` package — no third-party test frameworks

## Repository Layout

```
main.go                     # CLI entry point (flags, signal handling, file watcher)
WORKFLOW.md                 # Example workflow definition (YAML front matter + Liquid prompt)
internal/
  agent/       # JSON-RPC client for codex app-server subprocess; multi-turn loop
  config/      # Typed config getters with defaults, $VAR and ~ expansion
  orchestrator/# Poll loop, dispatch, retry/backoff, state reconciliation
  server/      # Optional HTTP dashboard and JSON REST API
  tracker/     # Linear GraphQL client — fetch, paginate, normalise issues
  workflow/    # Parse WORKFLOW.md (YAML front matter + Liquid template body)
  workspace/   # Workspace lifecycle (create, clean, hooks) with path safety
```

## Build & Test

```bash
# Build
go build -o symphony .

# Run all tests
go test ./...

# Run tests for a single package
go test ./internal/config/...
go test ./internal/workflow/...
go test ./internal/tracker/...
go test ./internal/workspace/...
```

All tests must pass before merging. There is no separate lint script; standard `go vet ./...` and `go build ./...` are the correctness checks.

## Coding Conventions

### Naming
- **Package names**: lowercase, single word (e.g., `config`, `tracker`, `agent`)
- **Exported identifiers**: PascalCase (e.g., `New`, `Load`, `SanitizeKey`)
- **Unexported identifiers**: camelCase (e.g., `getNestedRaw`, `expandValue`)
- **Constructor functions**: `NewXXX()` returning a pointer to the new type

### Error Handling
- Use early returns with explicit `if err != nil` checks.
- Wrap errors with context using `fmt.Errorf("context: %w", err)`.
- Use descriptive prefixes for domain errors (e.g., `"workflow_parse_error:"`, `"workspace error:"`).
- Log errors with `slog` using a structured `"error"` field.

### Imports
Group imports with a blank line separating standard library from third-party packages:
```go
import (
    "context"
    "fmt"

    "github.com/osteele/liquid"
    "gopkg.in/yaml.v3"
)
```

### Comments
Follow Go doc comment style — exported identifiers start their comment with the identifier name:
```go
// SanitizeKey replaces characters not in [A-Za-z0-9._-] with underscore.
func SanitizeKey(s string) string { ... }
```

### Concurrency
- Use `context.Context` for cancellation and timeouts throughout.
- Spawn background goroutines with `go func() { ... }()` guarded by `ctx.Done()` selects.
- Prefer `context.WithTimeout` over manual timers.

## Testing Conventions

- Use the standard `testing` package only — no assertion libraries or mocking frameworks.
- Table-driven tests with anonymous struct slices for multiple cases:
  ```go
  tests := []struct{ input, want string }{...}
  for _, tt := range tests { ... }
  ```
- Use `t.TempDir()` for temporary directories (automatic cleanup).
- Use `t.Setenv()` for environment variables scoped to a single test.
- Use `t.Fatalf` for setup failures, `t.Errorf` for assertion failures.
- Tests live in the same package as the code under test (e.g., `package config`).

## Configuration (WORKFLOW.md)

The `WORKFLOW.md` file uses YAML front matter (delimited by `---`) followed by a Liquid template body. The front matter drives the `Config` object; the template body is rendered per issue using the `{{ issue.* }}` binding. Refer to `WORKFLOW.md` and `internal/workflow/loader.go` for the full schema.
