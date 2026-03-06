package tracker

import (
	"testing"
	"time"
)

func TestIssueFields(t *testing.T) {
	now := time.Now()
	p := 1
	issue := Issue{
		ID:          "id-1",
		Identifier:  "ENG-123",
		Title:       "Test issue",
		Description: "Description",
		Priority:    &p,
		State:       "In Progress",
		BranchName:  "eng-123-test-issue",
		URL:         "https://linear.app/issue/ENG-123",
		Labels:      []string{"bug", "backend"},
		BlockedBy: []BlockerRef{
			{ID: "id-2", Identifier: "ENG-100", State: "In Progress"},
		},
		CreatedAt: &now,
		UpdatedAt: &now,
	}

	if issue.ID != "id-1" {
		t.Errorf("unexpected ID: %s", issue.ID)
	}
	if issue.Priority == nil || *issue.Priority != 1 {
		t.Errorf("unexpected priority")
	}
	if len(issue.Labels) != 2 {
		t.Errorf("expected 2 labels, got %v", issue.Labels)
	}
	if len(issue.BlockedBy) != 1 {
		t.Errorf("expected 1 blocker, got %v", issue.BlockedBy)
	}
}

func TestBlockerRef(t *testing.T) {
	b := BlockerRef{
		ID:         "b-1",
		Identifier: "ENG-50",
		State:      "Done",
	}
	if b.State != "Done" {
		t.Errorf("unexpected state: %s", b.State)
	}
}
