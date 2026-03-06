package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config wraps raw workflow config with typed getters and env-var resolution.
type Config struct {
	raw map[string]any
}

// New creates a Config from raw workflow front-matter map.
func New(raw map[string]any) *Config {
	if raw == nil {
		raw = map[string]any{}
	}
	return &Config{raw: raw}
}

// getNestedString retrieves a nested string value by dot-separated key path.
func (c *Config) getNestedRaw(keys []string) (any, bool) {
	var cur any = c.raw
	for _, k := range keys {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		v, ok := m[k]
		if !ok {
			return nil, false
		}
		cur = v
	}
	return cur, true
}

func splitKey(key string) []string {
	return strings.Split(key, ".")
}

func expandValue(s string) string {
	// Expand $VAR and ${VAR} environment variables
	s = os.ExpandEnv(s)
	// Expand leading ~
	if strings.HasPrefix(s, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			s = filepath.Join(home, s[2:])
		}
	} else if s == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			s = home
		}
	}
	return s
}

func (c *Config) getString(key, def string) string {
	v, ok := c.getNestedRaw(splitKey(key))
	if !ok || v == nil {
		return expandValue(def)
	}
	switch sv := v.(type) {
	case string:
		return expandValue(sv)
	default:
		return expandValue(fmt.Sprintf("%v", sv))
	}
}

func (c *Config) getInt(key string, def int) int {
	v, ok := c.getNestedRaw(splitKey(key))
	if !ok || v == nil {
		return def
	}
	switch iv := v.(type) {
	case int:
		return iv
	case int64:
		return int(iv)
	case float64:
		return int(iv)
	default:
		return def
	}
}

func (c *Config) getStringSlice(key string, def []string) []string {
	v, ok := c.getNestedRaw(splitKey(key))
	if !ok || v == nil {
		return def
	}
	switch sv := v.(type) {
	case []any:
		out := make([]string, 0, len(sv))
		for _, item := range sv {
			out = append(out, fmt.Sprintf("%v", item))
		}
		return out
	case []string:
		return sv
	default:
		return def
	}
}

func (c *Config) getStringMap(key string) map[string]int {
	v, ok := c.getNestedRaw(splitKey(key))
	if !ok || v == nil {
		return map[string]int{}
	}
	m, ok := v.(map[string]any)
	if !ok {
		return map[string]int{}
	}
	out := make(map[string]int, len(m))
	for k, val := range m {
		switch iv := val.(type) {
		case int:
			out[k] = iv
		case float64:
			out[k] = int(iv)
		}
	}
	return out
}

// TrackerKind returns the tracker kind (e.g., "linear").
func (c *Config) TrackerKind() string {
	return c.getString("tracker.kind", "")
}

// TrackerEndpoint returns the tracker GraphQL endpoint.
func (c *Config) TrackerEndpoint() string {
	return c.getString("tracker.endpoint", "https://api.linear.app/graphql")
}

// TrackerAPIKey returns the tracker API key.
func (c *Config) TrackerAPIKey() string {
	return c.getString("tracker.api_key", "$LINEAR_API_KEY")
}

// TrackerProjectSlug returns the Linear project slug.
func (c *Config) TrackerProjectSlug() string {
	return c.getString("tracker.project_slug", "")
}

// TrackerActiveStates returns the active issue states.
func (c *Config) TrackerActiveStates() []string {
	return c.getStringSlice("tracker.active_states", []string{"Todo", "In Progress"})
}

// TrackerTerminalStates returns the terminal issue states.
func (c *Config) TrackerTerminalStates() []string {
	return c.getStringSlice("tracker.terminal_states", []string{"Closed", "Cancelled", "Canceled", "Duplicate", "Done"})
}

// PollingIntervalMs returns the polling interval in milliseconds.
func (c *Config) PollingIntervalMs() int {
	return c.getInt("polling.interval_ms", 30000)
}

// WorkspaceRoot returns the workspace root directory.
func (c *Config) WorkspaceRoot() string {
	def := filepath.Join(os.TempDir(), "symphony_workspaces")
	return c.getString("workspace.root", def)
}

// HookAfterCreate returns the after-create hook script.
func (c *Config) HookAfterCreate() string {
	return c.getString("hooks.after_create", "")
}

// HookBeforeRun returns the before-run hook script.
func (c *Config) HookBeforeRun() string {
	return c.getString("hooks.before_run", "")
}

// HookAfterRun returns the after-run hook script.
func (c *Config) HookAfterRun() string {
	return c.getString("hooks.after_run", "")
}

// HookBeforeRemove returns the before-remove hook script.
func (c *Config) HookBeforeRemove() string {
	return c.getString("hooks.before_remove", "")
}

// HookTimeoutMs returns the hook timeout in milliseconds.
func (c *Config) HookTimeoutMs() int {
	return c.getInt("hooks.timeout_ms", 60000)
}

// MaxConcurrentAgents returns the maximum number of concurrent agents.
func (c *Config) MaxConcurrentAgents() int {
	return c.getInt("agent.max_concurrent_agents", 10)
}

// MaxRetryBackoffMs returns the maximum retry backoff in milliseconds.
func (c *Config) MaxRetryBackoffMs() int {
	return c.getInt("agent.max_retry_backoff_ms", 300000)
}

// MaxConcurrentAgentsByState returns per-state concurrency limits.
func (c *Config) MaxConcurrentAgentsByState() map[string]int {
	return c.getStringMap("agent.max_concurrent_agents_by_state")
}

// CodexCommand returns the codex app-server command.
func (c *Config) CodexCommand() string {
	return c.getString("codex.command", "codex app-server")
}

// CodexTurnTimeoutMs returns the codex turn timeout in milliseconds.
func (c *Config) CodexTurnTimeoutMs() int {
	return c.getInt("codex.turn_timeout_ms", 3600000)
}

// CodexReadTimeoutMs returns the codex read timeout in milliseconds.
func (c *Config) CodexReadTimeoutMs() int {
	return c.getInt("codex.read_timeout_ms", 5000)
}

// CodexStallTimeoutMs returns the codex stall timeout in milliseconds.
func (c *Config) CodexStallTimeoutMs() int {
	return c.getInt("codex.stall_timeout_ms", 300000)
}

// ServerPort returns the HTTP server port.
func (c *Config) ServerPort() int {
	return c.getInt("server.port", 0)
}
