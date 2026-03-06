package tracker

import "time"

// BlockerRef references a blocking issue.
type BlockerRef struct {
	ID         string
	Identifier string
	State      string
}

// Issue represents a tracker issue.
type Issue struct {
	ID          string
	Identifier  string
	Title       string
	Description string
	Priority    *int
	State       string
	BranchName  string
	URL         string
	Labels      []string
	BlockedBy   []BlockerRef
	CreatedAt   *time.Time
	UpdatedAt   *time.Time
}