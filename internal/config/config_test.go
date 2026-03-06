package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaults(t *testing.T) {
	cfg := New(nil)

	if cfg.TrackerKind() != "" {
		t.Errorf("expected empty tracker kind, got %q", cfg.TrackerKind())
	}
	if cfg.TrackerEndpoint() != "https://api.linear.app/graphql" {
		t.Errorf("unexpected tracker endpoint: %q", cfg.TrackerEndpoint())
	}
	if cfg.PollingIntervalMs() != 30000 {
		t.Errorf("unexpected polling interval: %d", cfg.PollingIntervalMs())
	}
	if cfg.MaxConcurrentAgents() != 10 {
		t.Errorf("unexpected max concurrent agents: %d", cfg.MaxConcurrentAgents())
	}
	if cfg.CodexCommand() != "codex app-server" {
		t.Errorf("unexpected codex command: %q", cfg.CodexCommand())
	}
	if cfg.ServerPort() != 0 {
		t.Errorf("unexpected server port: %d", cfg.ServerPort())
	}
	states := cfg.TrackerActiveStates()
	if len(states) != 2 {
		t.Errorf("unexpected active states: %v", states)
	}
}

func TestEnvVarResolution(t *testing.T) {
	t.Setenv("TEST_API_KEY", "secret123")
	cfg := New(map[string]any{
		"tracker": map[string]any{
			"api_key": "$TEST_API_KEY",
		},
	})
	if cfg.TrackerAPIKey() != "secret123" {
		t.Errorf("expected secret123, got %q", cfg.TrackerAPIKey())
	}
}

func TestTildeExpansion(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	cfg := New(map[string]any{
		"workspace": map[string]any{
			"root": "~/workspaces",
		},
	})
	expected := filepath.Join(home, "workspaces")
	if cfg.WorkspaceRoot() != expected {
		t.Errorf("expected %q, got %q", expected, cfg.WorkspaceRoot())
	}
}

func TestActiveStatesFromConfig(t *testing.T) {
	cfg := New(map[string]any{
		"tracker": map[string]any{
			"active_states": []any{"In Review", "In Progress"},
		},
	})
	states := cfg.TrackerActiveStates()
	if len(states) != 2 || states[0] != "In Review" {
		t.Errorf("unexpected active states: %v", states)
	}
}

func TestTerminalStatesDefaults(t *testing.T) {
	cfg := New(nil)
	states := cfg.TrackerTerminalStates()
	if len(states) < 4 {
		t.Errorf("expected at least 4 terminal states, got %v", states)
	}
}

func TestWorkspaceRootDefault(t *testing.T) {
	cfg := New(nil)
	root := cfg.WorkspaceRoot()
	if root == "" {
		t.Error("expected non-empty workspace root")
	}
}