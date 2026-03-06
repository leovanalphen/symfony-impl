package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/leovanalphen/symfony-impl/internal/agent"
	"github.com/leovanalphen/symfony-impl/internal/config"
	"github.com/leovanalphen/symfony-impl/internal/tracker"
	"github.com/leovanalphen/symfony-impl/internal/workflow"
	"github.com/leovanalphen/symfony-impl/internal/workspace"
)

// RunningEntry tracks an actively running agent.
type RunningEntry struct {
	IssueID          string
	IssueIdentifier  string
	IssueState       string
	SessionID        string
	ThreadID         string
	TurnID           string
	LastEvent        string
	LastEventAt      time.Time
	LastMessage      string
	TurnCount        int
	StartedAt        time.Time
	InputTokens      int64
	OutputTokens     int64
	TotalTokens      int64
	LastInputTokens  int64
	LastOutputTokens int64
	LastTotalTokens  int64
	CancelFn         context.CancelFunc
}

// RetryEntry tracks an issue scheduled for retry.
type RetryEntry struct {
	IssueID    string
	Identifier string
	Attempt    int
	DueAtMs    int64
	Error      string
	Timer      *time.Timer
}

// CodexTotals tracks cumulative token usage.
type CodexTotals struct {
	InputTokens    int64
	OutputTokens   int64
	TotalTokens    int64
	SecondsRunning float64
}

// StateSnapshot is the full orchestrator state for the API server.
type StateSnapshot struct {
	Running map[string]*RunningEntry
	Retries map[string]*RetryEntry
	Totals  CodexTotals
}

// Orchestrator coordinates the poll-dispatch loop and agent lifecycle.
type Orchestrator struct {
	cfg      *config.Config
	workflow *workflow.WorkflowDefinition
	linear   *tracker.LinearClient

	mu      sync.RWMutex
	running map[string]*RunningEntry
	retries map[string]*RetryEntry
	totals  CodexTotals

	eventCh    chan agent.RunnerEvent
	refreshCh  chan struct{}
	sessionSeq int64
}

// New creates a new Orchestrator.
func New(cfg *config.Config, wf *workflow.WorkflowDefinition) *Orchestrator {
	return &Orchestrator{
		cfg:       cfg,
		workflow:  wf,
		linear:    tracker.NewLinearClient(),
		running:   map[string]*RunningEntry{},
		retries:   map[string]*RetryEntry{},
		eventCh:   make(chan agent.RunnerEvent, 128),
		refreshCh: make(chan struct{}, 1),
	}
}

// UpdateWorkflow atomically replaces the workflow definition.
func (o *Orchestrator) UpdateWorkflow(wf *workflow.WorkflowDefinition) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.workflow = wf
	o.cfg = config.New(wf.Config)
}

// Refresh triggers an immediate poll.
func (o *Orchestrator) Refresh() {
	select {
	case o.refreshCh <- struct{}{}:
	default:
	}
}

// Snapshot returns a copy of the current state.
func (o *Orchestrator) Snapshot() StateSnapshot {
	o.mu.RLock()
	defer o.mu.RUnlock()
	runCopy := make(map[string]*RunningEntry, len(o.running))
	for k, v := range o.running {
		cp := *v
		runCopy[k] = &cp
	}
	retryCopy := make(map[string]*RetryEntry, len(o.retries))
	for k, v := range o.retries {
		cp := *v
		retryCopy[k] = &cp
	}
	return StateSnapshot{
		Running: runCopy,
		Retries: retryCopy,
		Totals:  o.totals,
	}
}

// Run starts the orchestrator event loop.
func (o *Orchestrator) Run(ctx context.Context) {
	interval := time.Duration(o.cfg.PollingIntervalMs()) * time.Millisecond
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Initial poll
	o.poll(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			o.poll(ctx)
		case <-o.refreshCh:
			o.poll(ctx)
		case ev := <-o.eventCh:
			o.handleEvent(ctx, ev)
		}
	}
}

func (o *Orchestrator) poll(ctx context.Context) {
	slog.Debug("poll starting")

	// 1. Reconcile: check running entries against tracker state
	o.reconcile(ctx)

	// 2. Fetch candidates
	candidates, err := o.linear.FetchCandidateIssues(ctx, o.cfg)
	if err != nil {
		slog.Warn("fetch candidates failed", "error", err)
		return
	}

	// 3. Sort by priority (lower number = higher priority; 0 = no priority, sort last)
	sort.Slice(candidates, func(i, j int) bool {
		pi := 999
		pj := 999
		if candidates[i].Priority != nil && *candidates[i].Priority > 0 {
			pi = *candidates[i].Priority
		}
		if candidates[j].Priority != nil && *candidates[j].Priority > 0 {
			pj = *candidates[j].Priority
		}
		return pi < pj
	})

	// 4. Dispatch eligible issues
	for _, issue := range candidates {
		if !o.canDispatch(issue) {
			continue
		}
		o.dispatch(ctx, issue)
	}
}

func (o *Orchestrator) reconcile(ctx context.Context) {
	o.mu.Lock()
	ids := make([]string, 0, len(o.running))
	for id := range o.running {
		ids = append(ids, id)
	}
	o.mu.Unlock()

	if len(ids) == 0 {
		return
	}

	states, err := o.linear.FetchIssueStatesByIDs(ctx, o.cfg, ids)
	if err != nil {
		slog.Warn("reconcile fetch states failed", "error", err)
		return
	}

	terminalStates := make(map[string]bool)
	for _, s := range o.cfg.TrackerTerminalStates() {
		terminalStates[s] = true
	}

	o.mu.Lock()
	defer o.mu.Unlock()

	for id, state := range states {
		entry, ok := o.running[id]
		if !ok {
			continue
		}
		entry.IssueState = state
		if terminalStates[state] {
			slog.Info("issue reached terminal state, cancelling", "id", id, "state", state)
			if entry.CancelFn != nil {
				entry.CancelFn()
			}
		}
	}
}

func (o *Orchestrator) canDispatch(issue tracker.Issue) bool {
	o.mu.RLock()
	defer o.mu.RUnlock()

	// Already running
	if _, ok := o.running[issue.ID]; ok {
		return false
	}

	// In retry backoff
	if _, ok := o.retries[issue.ID]; ok {
		return false
	}

	// Check global concurrency limit
	if len(o.running) >= o.cfg.MaxConcurrentAgents() {
		return false
	}

	// Check per-state concurrency
	byState := o.cfg.MaxConcurrentAgentsByState()
	if limit, ok := byState[issue.State]; ok {
		count := 0
		for _, e := range o.running {
			if e.IssueState == issue.State {
				count++
			}
		}
		if count >= limit {
			return false
		}
	}

	// Check if blocked by any non-terminal issue
	terminalStates := make(map[string]bool)
	for _, s := range o.cfg.TrackerTerminalStates() {
		terminalStates[s] = true
	}
	for _, blocker := range issue.BlockedBy {
		if !terminalStates[blocker.State] {
			return false
		}
	}

	return true
}

func (o *Orchestrator) dispatch(ctx context.Context, issue tracker.Issue) {
	o.mu.Lock()
	o.sessionSeq++
	sessionID := fmt.Sprintf("session-%d", o.sessionSeq)

	issueCtx, cancel := context.WithCancel(ctx)
	entry := &RunningEntry{
		IssueID:         issue.ID,
		IssueIdentifier: issue.Identifier,
		IssueState:      issue.State,
		SessionID:       sessionID,
		StartedAt:       time.Now(),
		LastEventAt:     time.Now(),
		CancelFn:        cancel,
	}
	o.running[issue.ID] = entry
	wf := o.workflow
	cfg := o.cfg
	o.mu.Unlock()

	slog.Info("dispatching agent", "issue", issue.Identifier, "session", sessionID)

	runner := agent.NewAgentRunner(cfg, wf, o.eventCh)
	go runner.Run(issueCtx, issue, sessionID)
}

func (o *Orchestrator) handleEvent(ctx context.Context, ev agent.RunnerEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()

	entry, ok := o.running[ev.IssueID]
	if !ok {
		return
	}

	entry.LastEventAt = time.Now()

	switch ev.Type {
	case agent.RunnerEventStarted:
		entry.LastEvent = "started"

	case agent.RunnerEventMessage:
		entry.LastEvent = "message"
		entry.LastMessage = ev.Message

	case agent.RunnerEventTokens:
		entry.LastEvent = "tokens"
		entry.ThreadID = ev.ThreadID
		entry.TurnID = ev.TurnID
		entry.TurnCount = ev.TurnCount
		entry.LastInputTokens = ev.InputTokens - entry.InputTokens
		entry.LastOutputTokens = ev.OutputTokens - entry.OutputTokens
		entry.LastTotalTokens = ev.TotalTokens - entry.TotalTokens
		entry.InputTokens = ev.InputTokens
		entry.OutputTokens = ev.OutputTokens
		entry.TotalTokens = ev.TotalTokens
		o.totals.InputTokens += entry.LastInputTokens
		o.totals.OutputTokens += entry.LastOutputTokens
		o.totals.TotalTokens += entry.LastTotalTokens

	case agent.RunnerEventCompleted:
		entry.LastEvent = "completed"
		entry.ThreadID = ev.ThreadID
		entry.TurnID = ev.TurnID
		entry.TurnCount = ev.TurnCount
		entry.InputTokens = ev.InputTokens
		entry.OutputTokens = ev.OutputTokens
		entry.TotalTokens = ev.TotalTokens
		elapsed := time.Since(entry.StartedAt).Seconds()
		o.totals.SecondsRunning += elapsed
		if entry.CancelFn != nil {
			entry.CancelFn()
		}
		delete(o.running, ev.IssueID)
		// Schedule normal exit retry after 1000ms
		o.scheduleRetry(ev.IssueID, ev.IssueIdentifier, 0, "")

	case agent.RunnerEventFailed:
		entry.LastEvent = "failed"
		entry.TurnCount = ev.TurnCount
		attempt := 0
		if retry, ok := o.retries[ev.IssueID]; ok {
			attempt = retry.Attempt
		}
		if entry.CancelFn != nil {
			entry.CancelFn()
		}
		delete(o.running, ev.IssueID)
		o.scheduleRetry(ev.IssueID, ev.IssueIdentifier, attempt+1, ev.Error)

	case agent.RunnerEventCancelled:
		entry.LastEvent = "cancelled"
		if entry.CancelFn != nil {
			entry.CancelFn()
		}
		delete(o.running, ev.IssueID)
	}
}

func (o *Orchestrator) scheduleRetry(issueID, identifier string, attempt int, errMsg string) {
	var delayMs int64
	if attempt == 0 {
		delayMs = 1000
	} else {
		delayMs = int64(math.Min(float64(10000)*math.Pow(2, float64(attempt-1)), float64(o.cfg.MaxRetryBackoffMs())))
	}

	dueAtMs := time.Now().UnixMilli() + delayMs

	timer := time.AfterFunc(time.Duration(delayMs)*time.Millisecond, func() {
		o.mu.Lock()
		delete(o.retries, issueID)
		o.mu.Unlock()
		// Trigger a poll
		o.Refresh()
	})

	o.retries[issueID] = &RetryEntry{
		IssueID:    issueID,
		Identifier: identifier,
		Attempt:    attempt,
		DueAtMs:    dueAtMs,
		Error:      errMsg,
		Timer:      timer,
	}
}

// GetRunning returns a copy of a single running entry by issue ID.
func (o *Orchestrator) GetRunning(issueID string) (*RunningEntry, bool) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	e, ok := o.running[issueID]
	if !ok {
		return nil, false
	}
	cp := *e
	return &cp, true
}

// CleanupWorkspace removes a workspace for the given identifier.
func (o *Orchestrator) CleanupWorkspace(ctx context.Context, identifier string) error {
	return workspace.CleanWorkspace(ctx, o.cfg.WorkspaceRoot(), identifier, o.cfg.HookBeforeRemove(), o.cfg.HookTimeoutMs())
}
