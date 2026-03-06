package workflow

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadNoFrontMatter(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "WORKFLOW.md")
	_ = os.WriteFile(p, []byte("Hello world prompt"), 0o644)

	loader := NewWorkflowLoader()
	def, err := loader.Load(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if def.PromptTemplate != "Hello world prompt" {
		t.Errorf("expected prompt, got %q", def.PromptTemplate)
	}
	if len(def.Config) != 0 {
		t.Errorf("expected empty config, got %v", def.Config)
	}
}

func TestLoadWithFrontMatter(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "WORKFLOW.md")
	content := "---\ntracker:\n  kind: linear\n---\nDo the work"
	_ = os.WriteFile(p, []byte(content), 0o644)

	loader := NewWorkflowLoader()
	def, err := loader.Load(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if def.PromptTemplate != "Do the work" {
		t.Errorf("expected prompt, got %q", def.PromptTemplate)
	}
	tracker, ok := def.Config["tracker"].(map[string]any)
	if !ok {
		t.Fatalf("expected tracker map, got %T", def.Config["tracker"])
	}
	if tracker["kind"] != "linear" {
		t.Errorf("expected linear, got %v", tracker["kind"])
	}
}

func TestLoadMissingFile(t *testing.T) {
	loader := NewWorkflowLoader()
	_, err := loader.Load("/nonexistent/path/WORKFLOW.md")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadUnterminatedFrontMatter(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "WORKFLOW.md")
	_ = os.WriteFile(p, []byte("---\ntracker:\n  kind: linear\n"), 0o644)

	loader := NewWorkflowLoader()
	_, err := loader.Load(p)
	if err == nil {
		t.Fatal("expected error for unterminated front matter")
	}
}

func TestLoadFrontMatterNotMap(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "WORKFLOW.md")
	_ = os.WriteFile(p, []byte("---\n- item1\n- item2\n---\nPrompt"), 0o644)

	loader := NewWorkflowLoader()
	_, err := loader.Load(p)
	if err == nil {
		t.Fatal("expected error for non-map front matter")
	}
}

func TestLoadTrimsPrompt(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "WORKFLOW.md")
	_ = os.WriteFile(p, []byte("---\n---\n\n  trimmed  \n"), 0o644)

	loader := NewWorkflowLoader()
	def, err := loader.Load(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if def.PromptTemplate != "trimmed" {
		t.Errorf("expected trimmed, got %q", def.PromptTemplate)
	}
}