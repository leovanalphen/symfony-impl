package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSanitizeKey(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"ENG-123", "ENG-123"},
		{"ENG/123", "ENG_123"},
		{"hello world", "hello_world"},
		{"valid.name-ok_1", "valid.name-ok_1"},
		{"a b/c\\d", "a_b_c_d"},
	}

	for _, tt := range tests {
		got := SanitizeKey(tt.input)
		if got != tt.want {
			t.Errorf("SanitizeKey(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestWorkspacePath(t *testing.T) {
	root := "/tmp/workspaces"
	path := WorkspacePath(root, "ENG-123")
	expected := filepath.Join(root, "ENG-123")
	if path != expected {
		t.Errorf("expected %q, got %q", expected, path)
	}
}

func TestWorkspacePathSanitizes(t *testing.T) {
	root := "/tmp/workspaces"
	path := WorkspacePath(root, "ENG/123 test")
	expected := filepath.Join(root, "ENG_123_test")
	if path != expected {
		t.Errorf("expected %q, got %q", expected, path)
	}
}

func TestCheckSafety(t *testing.T) {
	root := "/tmp/workspaces"
	if err := checkSafety(root, filepath.Join(root, "ENG-123")); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if err := checkSafety(root, "/tmp/other"); err == nil {
		t.Error("expected safety error for path outside root")
	}
	// path traversal attempt
	if err := checkSafety(root, "/tmp/workspaces/../evil"); err == nil {
		t.Error("expected safety error for path traversal")
	}
}

func TestEnsureWorkspace(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	path, created, err := EnsureWorkspace(ctx, dir, "ENG-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !created {
		t.Error("expected workspace to be created")
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("workspace dir should exist: %v", err)
	}

	// Call again - should not be created
	_, created2, err := EnsureWorkspace(ctx, dir, "ENG-123")
	if err != nil {
		t.Fatalf("unexpected error on second call: %v", err)
	}
	if created2 {
		t.Error("expected workspace to not be re-created")
	}
}

func TestCleanWorkspace(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	path, _, err := EnsureWorkspace(ctx, dir, "ENG-999")
	if err != nil {
		t.Fatalf("setup error: %v", err)
	}

	if err := CleanWorkspace(ctx, dir, "ENG-999", "", 5000); err != nil {
		t.Fatalf("clean error: %v", err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("expected workspace to be removed")
	}
}

func TestCleanWorkspaceNonexistent(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	// Should not error on nonexistent workspace
	if err := CleanWorkspace(ctx, dir, "NONEXISTENT-1", "", 5000); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}