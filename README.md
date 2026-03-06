# Symphony — Go Implementation

> A long-running automation daemon that reads work from [Linear](https://linear.app), creates an isolated per-issue workspace, and runs a [Codex](https://openai.com/codex) coding-agent session for every eligible issue.

This repository is a Go implementation of the [Symphony specification](https://github.com/openai/symphony/blob/main/SPEC.md).

---

## Table of Contents

1. [Overview](#overview)
2. [Architecture](#architecture)
3. [Prerequisites](#prerequisites)
4. [Installation](#installation)
5. [Quick Start](#quick-start)
6. [WORKFLOW.md Reference](#workflowmd-reference)
   - [Front Matter Schema](#front-matter-schema)
   - [Prompt Template](#prompt-template)
7. [CLI Reference](#cli-reference)
8. [HTTP API Reference](#http-api-reference)
9. [Workspace Management](#workspace-management)
10. [Hooks](#hooks)
11. [Scheduling, Retry & Backoff](#scheduling-retry--backoff)
12. [Live Reload](#live-reload)
13. [Logging](#logging)
14. [Security Notes](#security-notes)
15. [Development](#development)
16. [Project Layout](#project-layout)

---

## Overview

Symphony continuously:

1. **Polls** a Linear project for issues in configured active states (default: `Todo`, `In Progress`).
2. **Creates** an isolated workspace directory for each eligible issue.
3. **Renders** a Liquid prompt template with the issue's fields.
4. **Launches** a `codex app-server` subprocess inside the workspace and drives a JSON-RPC session over stdio.
5. **Reconciles** running sessions every tick — stopping agents when their issue transitions to a terminal state.
6. **Retries** failed sessions with exponential backoff.
7. **Exposes** an optional HTTP dashboard and JSON REST API for observability.

Symphony is a **scheduler and runner only**. Ticket mutations (state transitions, comments, PR links) are performed by the coding agent itself using tools that are defined in the workflow prompt.

---

## Architecture

```
┌──────────────────────────────────────────────┐
│                   main.go                    │
│  CLI flags · fsnotify watcher · signal trap  │
└────────────────────┬─────────────────────────┘
                     │
          ┌──────────▼──────────┐
          │    Orchestrator     │  ← single authoritative state
          │  poll · dispatch    │
          │  reconcile · retry  │
          └──┬───────────┬──────┘
             │           │
   ┌─────────▼──┐   ┌────▼──────────┐
   │  Linear    │   │  AgentRunner  │  (one goroutine per issue)
   │  GraphQL   │   │  workspace    │
   │  client    │   │  prompt render│
   └────────────┘   │  AppServer    │
                    │  client       │
                    └───────────────┘
                          │ JSON-RPC stdio
                    ┌─────▼──────┐
                    │ codex      │
                    │ app-server │
                    └────────────┘
```

| Package | Responsibility |
|---|---|
| `internal/workflow` | Parse `WORKFLOW.md` — YAML front matter + Liquid prompt body |
| `internal/config` | Typed getters, built-in defaults, `$VAR` / `~` resolution |
| `internal/tracker` | Linear GraphQL client — fetch, paginate, normalize issues |
| `internal/workspace` | Per-issue directory lifecycle, key sanitization, path-safety invariant |
| `internal/agent` | `AppServerClient` (JSON-RPC over stdio) + `AgentRunner` (turn loop) |
| `internal/orchestrator` | Poll loop, dispatch, retry/backoff, reconciliation, state snapshot |
| `internal/server` | Optional HTTP server — dashboard, `/api/v1/*` |

---

## Prerequisites

| Requirement | Notes |
|---|---|
| Go ≥ 1.22 | `go version` to check |
| `codex` CLI | Must be on `PATH`; must support `app-server` subcommand |
| Linear API key | `LINEAR_API_KEY` environment variable (or set in `WORKFLOW.md`) |
| Linux / macOS | Workspace hooks run via `bash -c`; Windows is not currently supported |

---

## Installation

### From source

```bash
git clone https://github.com/leovanalphen/symfony-impl.git
cd symfony-impl
go build -o symphony .
```

The resulting `symphony` binary can be placed anywhere on your `PATH`.

### Verify

```bash
./symphony --help
```

---

## Quick Start

1. **Set your Linear API key:**

   ```bash
   export LINEAR_API_KEY=lin_api_xxxxxxxxxxxxxxxxxxxx
   ```

2. **Copy the example workflow file** to the root of the repository you want Symphony to work on, then edit it:

   ```bash
   cp /path/to/symfony-impl/WORKFLOW.md ./WORKFLOW.md
   # Edit tracker.project_slug, active_states, and the prompt body
   ```

3. **Run Symphony:**

   ```bash
   symphony --workflow ./WORKFLOW.md
   ```

4. **With the HTTP dashboard:**

   ```bash
   symphony --workflow ./WORKFLOW.md --port 8080
   # Open http://localhost:8080
   ```

Symphony will immediately poll Linear, create workspaces for eligible issues, and launch `codex app-server` sessions. Press `Ctrl-C` (or send `SIGTERM`) to stop gracefully.

---

## WORKFLOW.md Reference

`WORKFLOW.md` is the single configuration and prompt contract for a Symphony deployment. It lives in (and should be version-controlled alongside) your project repository.

**Format:**

```
---
<YAML front matter>
---

<Liquid prompt template>
```

- If the file starts with `---`, everything up to the next `---` line is parsed as YAML front matter.
- Everything after the closing `---` is the prompt body (trimmed before use).
- If there is no front matter, the entire file is treated as the prompt body and an empty config map is used.

### Front Matter Schema

All keys are optional unless noted as required.

#### `tracker`

Controls how Symphony connects to the issue tracker.

| Key | Type | Default | Description |
|---|---|---|---|
| `kind` | string | — | **Required for dispatch.** Currently only `linear` is supported. |
| `endpoint` | string | `https://api.linear.app/graphql` | GraphQL endpoint URL. |
| `api_key` | string or `$VAR` | `$LINEAR_API_KEY` | Linear API token. Use `$VAR_NAME` to read from an environment variable. |
| `project_slug` | string | — | **Required.** Linear project `slugId` (the short identifier visible in project URLs). |
| `active_states` | list of strings | `[Todo, In Progress]` | Issues in these states are eligible for dispatch. |
| `terminal_states` | list of strings | `[Closed, Cancelled, Canceled, Duplicate, Done]` | Issues in these states cause running agents to be stopped and workspaces cleaned. |

#### `polling`

| Key | Type | Default | Description |
|---|---|---|---|
| `interval_ms` | integer | `30000` | How often (ms) to poll Linear and reconcile running agents. Changes take effect on the next tick without restart. |

#### `workspace`

| Key | Type | Default | Description |
|---|---|---|---|
| `root` | path string | `<system temp>/symphony_workspaces` | Root directory where per-issue workspaces are created. Supports `~` and `$VAR` expansion. |

#### `hooks`

Shell scripts executed at workspace lifecycle events. Each script runs with `bash -c` inside the workspace directory. See [Hooks](#hooks) for full semantics.

| Key | Type | Default | Description |
|---|---|---|---|
| `after_create` | shell script | — | Runs once when a workspace directory is first created. |
| `before_run` | shell script | — | Runs before each agent attempt. |
| `after_run` | shell script | — | Runs after each agent attempt (success or failure). |
| `before_remove` | shell script | — | Runs before a workspace directory is deleted. |
| `timeout_ms` | integer | `60000` | Timeout applied to all hooks. |

#### `agent`

| Key | Type | Default | Description |
|---|---|---|---|
| `max_concurrent_agents` | integer | `10` | Global cap on simultaneously running agent sessions. |
| `max_retry_backoff_ms` | integer | `300000` | Upper bound on retry delay (5 minutes). |
| `max_concurrent_agents_by_state` | map `state → int` | `{}` | Per-state concurrency caps (state names are normalized to lowercase). |

#### `codex`

| Key | Type | Default | Description |
|---|---|---|---|
| `command` | shell command | `codex app-server` | Command launched via `bash -lc` inside the workspace. |
| `approval_policy` | string | implementation-defined | Codex `AskForApproval` value passed to the app-server session. |
| `thread_sandbox` | string | implementation-defined | Codex `SandboxMode` value for the thread. |
| `turn_sandbox_policy` | string | implementation-defined | Codex `SandboxPolicy` value for each turn. |
| `turn_timeout_ms` | integer | `3600000` | Maximum duration for a single turn (1 hour). |
| `read_timeout_ms` | integer | `5000` | Timeout for startup handshake reads. |
| `stall_timeout_ms` | integer | `300000` | Inactivity timeout before a session is killed and retried (5 minutes). Set ≤ 0 to disable. |

#### `server` (extension)

| Key | Type | Default | Description |
|---|---|---|---|
| `port` | integer | `0` (disabled) | Port for the optional HTTP server. Overridden by `--port` CLI flag. |

#### Full Example

```yaml
---
tracker:
  kind: linear
  api_key: $LINEAR_API_KEY
  project_slug: my-project
  active_states:
    - Todo
    - In Progress
  terminal_states:
    - Done
    - Cancelled
    - Canceled
    - Closed
    - Duplicate

polling:
  interval_ms: 30000

workspace:
  root: ~/symphony_workspaces

hooks:
  after_create: |
    git clone https://github.com/my-org/my-repo.git .
  before_run: |
    git fetch origin && git reset --hard origin/main
  timeout_ms: 120000

agent:
  max_concurrent_agents: 5
  max_concurrent_agents_by_state:
    "in progress": 3

codex:
  command: codex app-server
  turn_timeout_ms: 3600000
  stall_timeout_ms: 300000

server:
  port: 8080
---

<Liquid prompt body here>
```

---

### Prompt Template

The Markdown body of `WORKFLOW.md` is a [Liquid](https://shopify.github.io/liquid/) template rendered once per agent session.

**Available variables:**

| Variable | Type | Description |
|---|---|---|
| `issue.id` | string | Stable Linear internal ID |
| `issue.identifier` | string | Human-readable key, e.g. `ABC-123` |
| `issue.title` | string | Issue title |
| `issue.description` | string | Issue description body |
| `issue.priority` | integer | Priority (1 = urgent … 4 = low; 0 = none) |
| `issue.state` | string | Current tracker state name |
| `issue.branch_name` | string | Tracker-provided branch metadata |
| `issue.url` | string | URL to the issue in Linear |
| `issue.labels` | array of strings | Labels (lowercased) |
| `issue.created_at` | string (RFC3339) | Creation timestamp |
| `issue.updated_at` | string (RFC3339) | Last-updated timestamp |
| `attempt` | integer or null | `null` on first run; `≥ 1` on retry / continuation |

**Example prompt body:**

```liquid
You are a software engineer working on {{ issue.identifier }}: {{ issue.title }}.

## Description

{{ issue.description }}

## Labels

{% for label in issue.labels %}
- {{ label }}
{% endfor %}

{% if attempt %}
⚠️ This is retry attempt {{ attempt }}. The previous session did not complete successfully.
{% endif %}

Please implement the requested changes, write tests, and make sure all existing tests pass.
When you are finished, summarise what you changed.
```

> **Strict rendering:** unknown variables and unknown filters cause the run attempt to fail immediately with a `template_render_error`. Use only the variables listed above.

---

## CLI Reference

```
symphony [flags]
```

| Flag | Default | Description |
|---|---|---|
| `--workflow <path>` | `./WORKFLOW.md` | Path to the workflow file. |
| `--port <n>` | `0` (disabled) | Start the HTTP server on port `n`. Overrides `server.port` in the workflow file. |

**Signals:**

| Signal | Behaviour |
|---|---|
| `SIGINT` / `SIGTERM` | Graceful shutdown — cancels all running agent contexts and stops the poll loop. |

**Logging** goes to `stderr` as structured JSON (using Go's `log/slog`). See [Logging](#logging) for field details.

---

## HTTP API Reference

The HTTP server is enabled when `--port` is provided or `server.port` is set in `WORKFLOW.md`. It binds to `127.0.0.1` by default.

### `GET /`

Human-readable HTML dashboard showing:
- Active agent sessions (identifier, last event, turn count, token usage)
- Retry queue (identifier, attempt number, due time)
- Aggregate token and runtime totals

### `GET /api/v1/state`

Returns a JSON snapshot of the full system state.

**Response `200 OK`:**

```json
{
  "running": [
    {
      "issue_id": "abc123",
      "issue_identifier": "MT-649",
      "issue_state": "In Progress",
      "session_id": "session-1",
      "thread_id": "thread-abc",
      "turn_id": "turn-1",
      "last_event": "message",
      "last_event_at": "2026-02-24T20:14:59Z",
      "last_message": "Running tests…",
      "turn_count": 3,
      "started_at": "2026-02-24T20:10:12Z",
      "input_tokens": 1200,
      "output_tokens": 800,
      "total_tokens": 2000
    }
  ],
  "retries": [
    {
      "issue_id": "def456",
      "identifier": "MT-650",
      "attempt": 2,
      "due_at_ms": 1740427000000,
      "error": "turn timed out"
    }
  ],
  "totals": {
    "InputTokens": 5000,
    "OutputTokens": 2400,
    "TotalTokens": 7400,
    "SecondsRunning": 1834.2
  }
}
```

### `GET /api/v1/{identifier}`

Returns the running entry for a specific issue identifier (e.g. `MT-649`).

**Response `200 OK`:** Same shape as a single element of the `running` array above.

**Response `404 Not Found`:** The identifier is not currently in the running state.

### `POST /api/v1/refresh`

Queues an immediate poll + reconciliation cycle. Repeated calls are coalesced.

**Response `200 OK`:**

```json
{ "ok": true }
```

---

## Workspace Management

Each issue gets a dedicated workspace directory:

```
<workspace.root>/<sanitized-identifier>/
```

**Key sanitisation:** any character outside `[A-Za-z0-9._-]` is replaced with `_`. For example, issue `ABC-123` maps to directory `ABC-123`; issue `my issue!` maps to `my_issue_`.

**Safety invariant:** before any agent subprocess is launched, Symphony verifies that the resolved workspace path is under `workspace.root`. Any attempt to escape the root is a hard error.

**Persistence:** Workspaces are preserved across runs for the same issue. They are removed only when an issue transitions to a terminal state (during reconciliation or startup cleanup).

---

## Hooks

Hooks are arbitrary shell scripts (`bash -c`) that run with the workspace directory as the working directory.

| Hook | When | Failure behaviour |
|---|---|---|
| `after_create` | Once, immediately after the workspace directory is first created | Fatal — workspace creation fails and the attempt is aborted |
| `before_run` | Before every agent attempt (workspace already exists) | Fatal — the attempt is aborted |
| `after_run` | After every agent attempt (success or failure) | Non-fatal — logged and ignored |
| `before_remove` | Before the workspace directory is deleted | Non-fatal — logged and ignored; deletion still proceeds |

The environment variable `WORKSPACE_PATH` is exported to every hook script.

**Timeout:** all hooks share `hooks.timeout_ms` (default 60 s). A hook that exceeds the timeout is killed and treated as a failure according to the table above.

**Common `after_create` pattern** (clone repo into fresh workspace):

```yaml
hooks:
  after_create: |
    git clone https://github.com/my-org/my-repo.git .
    npm ci
```

**Common `before_run` pattern** (keep workspace up-to-date):

```yaml
hooks:
  before_run: |
    git fetch origin
    git reset --hard origin/main
```

---

## Scheduling, Retry & Backoff

### Dispatch eligibility

An issue is dispatched only when **all** of these hold:

- It has `id`, `identifier`, `title`, and `state`.
- Its state is in `active_states` and not in `terminal_states`.
- It is not already running or waiting in the retry queue.
- The global concurrency cap (`max_concurrent_agents`) is not reached.
- The per-state cap (`max_concurrent_agents_by_state[state]`) is not reached.
- For issues in `Todo` state: no blocker is in a non-terminal state.

**Dispatch sort order** (stable, most-preferred first):

1. `priority` ascending (1 = urgent is dispatched before 4 = low; issues with no priority sort last)
2. `created_at` oldest first
3. `identifier` lexicographic tie-break

### Retry backoff

| Scenario | Delay formula |
|---|---|
| Clean worker exit (normal completion) | Fixed **1 000 ms** — the orchestrator re-checks whether the issue still needs work |
| First failure (attempt = 1) | `min(10 000 × 2^0, max_retry_backoff_ms)` = **10 s** |
| Second failure (attempt = 2) | `min(10 000 × 2^1, max_retry_backoff_ms)` = **20 s** |
| Third failure (attempt = 3) | `min(10 000 × 2^2, max_retry_backoff_ms)` = **40 s** |
| … | … |
| Capped | `max_retry_backoff_ms` (default **5 min**) |

When a retry timer fires, Symphony re-fetches the active candidate list. If the issue is no longer eligible it is released; otherwise it is re-dispatched if a concurrency slot is available.

### Stall detection

If no event is received from the running agent for `codex.stall_timeout_ms` (default 5 min), the session is killed and scheduled for a failure-driven retry. Set `stall_timeout_ms: 0` (or negative) to disable stall detection.

---

## Live Reload

Symphony watches `WORKFLOW.md` with [`fsnotify`](https://github.com/fsnotify/fsnotify). When the file changes:

1. The file is re-read and re-parsed.
2. If parsing succeeds, the orchestrator atomically adopts the new config and prompt template.
3. If parsing fails, Symphony logs a warning and **keeps the last known-good config** — no crash, no restart required.

Settings that take effect immediately on reload (no restart needed):

- `polling.interval_ms` — affects the next tick schedule
- `agent.max_concurrent_agents` and `max_concurrent_agents_by_state` — affect the next dispatch decision
- `hooks.*` — affect the next hook execution
- `codex.*` — affect the next agent session launch
- Prompt template — affects the next prompt render

Settings that require a restart:

- `server.port` — the HTTP listener is bound at startup only

---

## Logging

All log output goes to **`stderr`** as structured JSON using Go's `log/slog`. Fields included in every log record:

| Field | Description |
|---|---|
| `time` | RFC3339 timestamp |
| `level` | `DEBUG`, `INFO`, `WARN`, or `ERROR` |
| `msg` | Human-readable message |

Additional context fields for issue-related records:

| Field | Description |
|---|---|
| `issue` | Issue identifier (e.g. `ABC-123`) |
| `session` | Session ID |
| `error` | Error message (on failures) |

**Example log line:**

```json
{"time":"2026-02-24T20:10:12Z","level":"INFO","msg":"dispatching agent","issue":"MT-649","session":"session-1"}
```

To pretty-print logs during development:

```bash
symphony 2>&1 | jq .
```

---

## Security Notes

**Trust boundary.** Symphony is designed for trusted, operator-controlled environments. The coding agent runs with the permissions of the Symphony process. Review Codex's `approval_policy` and `thread_sandbox` settings before deploying in sensitive environments.

**Secret handling.** API keys should be provided via environment variables (`$LINEAR_API_KEY`) rather than hard-coded in `WORKFLOW.md`. Symphony never logs the value of API keys — only their presence or absence.

**Workspace isolation.** Before launching any agent, Symphony resolves the workspace path to an absolute path and verifies it is under `workspace.root`. Identifiers that would escape the root (e.g. via `../`) cause a hard error.

**Hook scripts.** Hooks are arbitrary shell commands from `WORKFLOW.md`. They are fully trusted configuration — treat `WORKFLOW.md` with the same care as other privileged configuration files.

**Network.** The HTTP server binds to `127.0.0.1` (loopback) by default. Do not expose it to untrusted networks without adding authentication.

---

## Development

### Running tests

```bash
go test ./...
```

### Running a single package

```bash
go test ./internal/workflow/...
go test ./internal/config/...
go test ./internal/tracker/...
go test ./internal/workspace/...
```

### Building

```bash
go build -o symphony .
```

### Dependencies

| Module | Purpose |
|---|---|
| `gopkg.in/yaml.v3` | YAML front matter parsing |
| `github.com/osteele/liquid` | Liquid template engine for prompt rendering |
| `github.com/fsnotify/fsnotify` | Cross-platform filesystem watcher for live reload |

---

## Project Layout

```
.
├── main.go                          # CLI entry point, watcher, signal handling
├── WORKFLOW.md                      # Example workflow (edit before use)
├── go.mod / go.sum
└── internal/
    ├── workflow/
    │   ├── loader.go                # WorkflowLoader — parse WORKFLOW.md
    │   └── loader_test.go
    ├── config/
    │   ├── config.go                # Typed config getters with defaults + $VAR/~ resolution
    │   └── config_test.go
    ├── tracker/
    │   ├── issue.go                 # Issue and BlockerRef domain types
    │   ├── linear.go                # Linear GraphQL client (paginated fetch, reconciliation)
    │   └── issue_test.go
    ├── workspace/
    │   ├── manager.go               # WorkspaceManager — lifecycle, hooks, safety invariant
    │   └── manager_test.go
    ├── agent/
    │   ├── client.go                # AppServerClient — JSON-RPC over stdio
    │   └── runner.go                # AgentRunner — prompt render + multi-turn loop
    ├── orchestrator/
    │   └── orchestrator.go          # Poll loop, dispatch, retry/backoff, reconciliation
    └── server/
        └── server.go                # Optional HTTP server — dashboard + /api/v1/*
```