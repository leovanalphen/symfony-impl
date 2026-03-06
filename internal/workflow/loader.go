package workflow

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// WorkflowDefinition holds the parsed workflow front matter config and prompt template.
type WorkflowDefinition struct {
	Config         map[string]any
	PromptTemplate string
}

// WorkflowLoader loads and parses WORKFLOW.md files.
type WorkflowLoader struct{}

// NewWorkflowLoader creates a new WorkflowLoader.
func NewWorkflowLoader() *WorkflowLoader { return &WorkflowLoader{} }

// Load reads and parses the workflow file at path.
func (l *WorkflowLoader) Load(path string) (*WorkflowDefinition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("missing_workflow_file: %w", err)
	}

	content := string(data)

	if !strings.HasPrefix(content, "---") {
		return &WorkflowDefinition{
			Config:         map[string]any{},
			PromptTemplate: strings.TrimSpace(content),
		}, nil
	}

	// Skip the opening "---"
	rest := content[3:]
	// Find closing "---"
	idx := strings.Index(rest, "\n---")
	if idx == -1 {
		return nil, fmt.Errorf("workflow_parse_error: unterminated front matter")
	}

	frontMatter := rest[:idx]
	body := rest[idx+4:]

	var fm any
	if err := yaml.Unmarshal([]byte(frontMatter), &fm); err != nil {
		return nil, fmt.Errorf("workflow_parse_error: %w", err)
	}

	if fm == nil {
		return &WorkflowDefinition{
			Config:         map[string]any{},
			PromptTemplate: strings.TrimSpace(body),
		}, nil
	}

	fmMap, ok := fm.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("workflow_front_matter_not_a_map")
	}

	return &WorkflowDefinition{
		Config:         fmMap,
		PromptTemplate: strings.TrimSpace(body),
	}, nil
}
