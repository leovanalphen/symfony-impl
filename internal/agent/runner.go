package agent

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/osteele/liquid"

	"github.com/leovanalphen/symfony-impl/internal/config"
	"github.com/leovanalphen/symfony-impl/internal/tracker"
	"github.com/leovanalphen/symfony-impl/internal/workflow"
	"github.com/leovanalphen/symfony-impl/internal/workspace"
)

// RunnerEventType describes the type of runner event.
type RunnerEventType string

const (
	RunnerEventStarted   RunnerEventType = "started"
	RunnerEventCompleted RunnerEventType = "completed"
	RunnerEventFailed    RunnerEventType = "failed"
	RunnerEventCancelled RunnerEventType = "cancelled"
	RunnerEventMessage   RunnerEventType = "message"
	RunnerEventTokens    RunnerEventType = "tokens"
)

// RunnerEvent is sent from a runner to the orchestrator.
type RunnerEvent struct {
	Type            RunnerEventType
	IssueID         string
	IssueIdentifier string
	SessionID       string
	ThreadID        string
	TurnID          string
	AppServerPID    int
	Message         string
	Error           string
	InputTokens     int64
	OutputTokens    int64
	TotalTokens     int64
	TurnCount       int
}

// AgentRunner coordinates workspace provisioning, prompt rendering and app-server execution.
type AgentRunner struct {
	cfg      *config.Config
	workflow *workflow.WorkflowDefinition
	eventCh  chan<- RunnerEvent
}

// NewAgentRunner creates a new AgentRunner.
func NewAgentRunner(cfg *config.Config, wf *workflow.WorkflowDefinition, eventCh chan<- RunnerEvent) *AgentRunner {
	return &AgentRunner{cfg: cfg, workflow: wf, eventCh: eventCh}
}

// Run executes the agent for a given issue.
func (r *AgentRunner) Run(ctx context.Context, issue tracker.Issue, sessionID string) {
	log := slog.With("issue", issue.Identifier, "session", sessionID)
	log.Info("runner starting")

	root := r.cfg.WorkspaceRoot()
	wsPath, createdNow, err := workspace.EnsureWorkspace(ctx, root, issue.Identifier)
	if err != nil {
		r.sendEvent(RunnerEvent{
			Type:            RunnerEventFailed,
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       sessionID,
			Error:           fmt.Sprintf("workspace error: %v", err),
		})
		return
	}

	if createdNow {
		if hookErr := workspace.RunHook(ctx, r.cfg.HookAfterCreate(), wsPath, r.cfg.HookTimeoutMs()); hookErr != nil {
			log.Warn("after_create hook failed", "error", hookErr)
		}
	}

	if hookErr := workspace.RunHook(ctx, r.cfg.HookBeforeRun(), wsPath, r.cfg.HookTimeoutMs()); hookErr != nil {
		log.Warn("before_run hook failed", "error", hookErr)
	}

	prompt, err := r.renderPrompt(issue)
	if err != nil {
		r.sendEvent(RunnerEvent{
			Type:            RunnerEventFailed,
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       sessionID,
			Error:           fmt.Sprintf("prompt render error: %v", err),
		})
		return
	}

	r.sendEvent(RunnerEvent{
		Type:            RunnerEventStarted,
		IssueID:         issue.ID,
		IssueIdentifier: issue.Identifier,
		SessionID:       sessionID,
	})

	agentEventCh := make(chan AgentEvent, 32)
	client := NewAppServerClient(r.cfg, wsPath, r.cfg.TrackerAPIKey(), agentEventCh)

	var threadID string
	var totalInput, totalOutput, totalAll int64
	turnCount := 0

	for {
		select {
		case <-ctx.Done():
			r.sendEvent(RunnerEvent{
				Type:            RunnerEventCancelled,
				IssueID:         issue.ID,
				IssueIdentifier: issue.Identifier,
				SessionID:       sessionID,
				TurnCount:       turnCount,
			})
			return
		default:
		}

		turnResult, runErr := client.Run(ctx, threadID, prompt)

		// Drain agent events
		for {
			select {
			case ae := <-agentEventCh:
				switch ae.Type {
				case EventMessage:
					r.sendEvent(RunnerEvent{
						Type:            RunnerEventMessage,
						IssueID:         issue.ID,
						IssueIdentifier: issue.Identifier,
						SessionID:       sessionID,
						Message:         ae.Message,
					})
				case EventTokenUsage:
					totalInput += ae.InputTokens
					totalOutput += ae.OutputTokens
					totalAll += ae.TotalTokens
				}
			default:
				goto drained
			}
		}
	drained:

		turnCount++

		if runErr != nil {
			log.Warn("turn failed", "error", runErr, "turn", turnCount)
			r.sendEvent(RunnerEvent{
				Type:            RunnerEventFailed,
				IssueID:         issue.ID,
				IssueIdentifier: issue.Identifier,
				SessionID:       sessionID,
				TurnCount:       turnCount,
				Error:           runErr.Error(),
			})
			break
		}

		threadID = turnResult.ThreadID
		totalInput += turnResult.InputTokens
		totalOutput += turnResult.OutputTokens
		totalAll += turnResult.TotalTokens

		r.sendEvent(RunnerEvent{
			Type:            RunnerEventTokens,
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       sessionID,
			ThreadID:        threadID,
			TurnID:          turnResult.TurnID,
			TurnCount:       turnCount,
			InputTokens:     totalInput,
			OutputTokens:    totalOutput,
			TotalTokens:     totalAll,
		})

		// Completed successfully - break for now (single turn per session)
		r.sendEvent(RunnerEvent{
			Type:            RunnerEventCompleted,
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       sessionID,
			ThreadID:        threadID,
			TurnID:          turnResult.TurnID,
			TurnCount:       turnCount,
			InputTokens:     totalInput,
			OutputTokens:    totalOutput,
			TotalTokens:     totalAll,
		})
		break
	}

	if hookErr := workspace.RunHook(ctx, r.cfg.HookAfterRun(), wsPath, r.cfg.HookTimeoutMs()); hookErr != nil {
		log.Warn("after_run hook failed", "error", hookErr)
	}
}

func (r *AgentRunner) renderPrompt(issue tracker.Issue) (string, error) {
	engine := liquid.NewEngine()

	var priority int
	if issue.Priority != nil {
		priority = *issue.Priority
	}

	var createdAt, updatedAt string
	if issue.CreatedAt != nil {
		createdAt = issue.CreatedAt.Format(time.RFC3339)
	}
	if issue.UpdatedAt != nil {
		updatedAt = issue.UpdatedAt.Format(time.RFC3339)
	}

	bindings := map[string]any{
		"issue": map[string]any{
			"id":          issue.ID,
			"identifier":  issue.Identifier,
			"title":       issue.Title,
			"description": issue.Description,
			"priority":    priority,
			"state":       issue.State,
			"branch_name": issue.BranchName,
			"url":         issue.URL,
			"labels":      issue.Labels,
			"created_at":  createdAt,
			"updated_at":  updatedAt,
		},
	}

	out, err := engine.ParseAndRenderString(r.workflow.PromptTemplate, bindings)
	if err != nil {
		return "", fmt.Errorf("liquid render: %w", err)
	}
	return out, nil
}

func (r *AgentRunner) sendEvent(e RunnerEvent) {
	select {
	case r.eventCh <- e:
	default:
	}
}
