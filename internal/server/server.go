package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/leovanalphen/symfony-impl/internal/orchestrator"
)

// OrchestratorState is the interface the server uses to query orchestrator state.
type OrchestratorState interface {
	Snapshot() orchestrator.StateSnapshot
	Refresh()
}

// Server is the HTTP API server.
type Server struct {
	orch OrchestratorState
	port int
	mux  *http.ServeMux
}

// New creates a new Server.
func New(orch OrchestratorState, port int) *Server {
	s := &Server{orch: orch, port: port, mux: http.NewServeMux()}
	s.registerRoutes()
	return s
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /api/v1/state", s.handleState)
	s.mux.HandleFunc("POST /api/v1/refresh", s.handleRefresh)
	s.mux.HandleFunc("GET /api/v1/{identifier}", s.handleIssue)
	s.mux.HandleFunc("GET /", s.handleDashboard)
}

// Start starts the HTTP server.
func (s *Server) Start() error {
	addr := fmt.Sprintf("127.0.0.1:%d", s.port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	slog.Info("server listening", "addr", ln.Addr().String())
	srv := &http.Server{
		Handler:      s.mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	return srv.Serve(ln)
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	snap := s.orch.Snapshot()
	type runningInfo struct {
		IssueID         string    `json:"issue_id"`
		IssueIdentifier string    `json:"issue_identifier"`
		IssueState      string    `json:"issue_state"`
		SessionID       string    `json:"session_id"`
		ThreadID        string    `json:"thread_id"`
		TurnID          string    `json:"turn_id"`
		LastEvent       string    `json:"last_event"`
		LastEventAt     time.Time `json:"last_event_at"`
		LastMessage     string    `json:"last_message"`
		TurnCount       int       `json:"turn_count"`
		StartedAt       time.Time `json:"started_at"`
		InputTokens     int64     `json:"input_tokens"`
		OutputTokens    int64     `json:"output_tokens"`
		TotalTokens     int64     `json:"total_tokens"`
	}
	type retryInfo struct {
		IssueID    string `json:"issue_id"`
		Identifier string `json:"identifier"`
		Attempt    int    `json:"attempt"`
		DueAtMs    int64  `json:"due_at_ms"`
		Error      string `json:"error"`
	}

	running := make([]runningInfo, 0, len(snap.Running))
	for _, e := range snap.Running {
		running = append(running, runningInfo{
			IssueID:         e.IssueID,
			IssueIdentifier: e.IssueIdentifier,
			IssueState:      e.IssueState,
			SessionID:       e.SessionID,
			ThreadID:        e.ThreadID,
			TurnID:          e.TurnID,
			LastEvent:       e.LastEvent,
			LastEventAt:     e.LastEventAt,
			LastMessage:     e.LastMessage,
			TurnCount:       e.TurnCount,
			StartedAt:       e.StartedAt,
			InputTokens:     e.InputTokens,
			OutputTokens:    e.OutputTokens,
			TotalTokens:     e.TotalTokens,
		})
	}

	retries := make([]retryInfo, 0, len(snap.Retries))
	for _, r := range snap.Retries {
		retries = append(retries, retryInfo{
			IssueID:    r.IssueID,
			Identifier: r.Identifier,
			Attempt:    r.Attempt,
			DueAtMs:    r.DueAtMs,
			Error:      r.Error,
		})
	}

	writeJSON(w, map[string]any{
		"running": running,
		"retries": retries,
		"totals":  snap.Totals,
	})
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	s.orch.Refresh()
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleIssue(w http.ResponseWriter, r *http.Request) {
	identifier := r.PathValue("identifier")
	snap := s.orch.Snapshot()
	for _, e := range snap.Running {
		if strings.EqualFold(e.IssueIdentifier, identifier) {
			writeJSON(w, e)
			return
		}
	}
	http.Error(w, "not found", http.StatusNotFound)
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	snap := s.orch.Snapshot()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>Symphony Dashboard</title></head>
<body>
<h1>Symphony Dashboard</h1>
<h2>Running (%d)</h2>
<ul>
`, len(snap.Running))
	for _, e := range snap.Running {
		fmt.Fprintf(w, "<li>%s - %s - turns: %d - tokens: %d</li>\n",
			e.IssueIdentifier, e.LastEvent, e.TurnCount, e.TotalTokens)
	}
	fmt.Fprintf(w, `</ul>
<h2>Retries (%d)</h2>
<ul>
`, len(snap.Retries))
	for _, r := range snap.Retries {
		fmt.Fprintf(w, "<li>%s - attempt %d - due: %d</li>\n", r.Identifier, r.Attempt, r.DueAtMs)
	}
	fmt.Fprintf(w, `</ul>
<h2>Totals</h2>
<p>Input tokens: %d | Output tokens: %d | Total tokens: %d | Seconds running: %.1f</p>
</body>
</html>
`, snap.Totals.InputTokens, snap.Totals.OutputTokens, snap.Totals.TotalTokens, snap.Totals.SecondsRunning)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("write JSON response failed", "error", err)
	}
}