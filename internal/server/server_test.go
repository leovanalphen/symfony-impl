package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/leovanalphen/symfony-impl/internal/orchestrator"
)

// fakeOrch is a test double for OrchestratorState.
type fakeOrch struct {
	snap      orchestrator.StateSnapshot
	refreshed bool
}

func (f *fakeOrch) Snapshot() orchestrator.StateSnapshot { return f.snap }
func (f *fakeOrch) Refresh()                             { f.refreshed = true }

func makeEntry(id, identifier, lastEvent string) *orchestrator.RunningEntry {
	_, cancel := context.WithCancel(context.Background())
	return &orchestrator.RunningEntry{
		IssueID:         id,
		IssueIdentifier: identifier,
		IssueState:      "In Progress",
		SessionID:       "session-1",
		LastEvent:       lastEvent,
		LastEventAt:     time.Time{},
		StartedAt:       time.Time{},
		TurnCount:       3,
		TotalTokens:     100,
		// CancelFn intentionally set to verify it is not marshalled
		CancelFn: cancel,
	}
}

// TestHandleIssueJSONSafe verifies that handleIssue returns valid JSON even when
// the RunningEntry contains a CancelFn (which is not JSON-serialisable).
func TestHandleIssueJSONSafe(t *testing.T) {
	entry := makeEntry("id-1", "ENG-42", "tokens")
	orch := &fakeOrch{
		snap: orchestrator.StateSnapshot{
			Running: map[string]*orchestrator.RunningEntry{
				"id-1": entry,
			},
			Retries: map[string]*orchestrator.RetryEntry{},
		},
	}

	srv := New(orch, 0)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ENG-42", nil)
	req.SetPathValue("identifier", "ENG-42")
	w := httptest.NewRecorder()

	srv.handleIssue(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}

	if result["issue_identifier"] != "ENG-42" {
		t.Errorf("expected issue_identifier ENG-42, got %v", result["issue_identifier"])
	}

	// CancelFn must not appear in the JSON output
	if _, ok := result["CancelFn"]; ok {
		t.Error("CancelFn must not appear in JSON output")
	}
}

// TestHandleIssueNotFound verifies that a missing identifier returns 404.
func TestHandleIssueNotFound(t *testing.T) {
	orch := &fakeOrch{
		snap: orchestrator.StateSnapshot{
			Running: map[string]*orchestrator.RunningEntry{},
			Retries: map[string]*orchestrator.RetryEntry{},
		},
	}

	srv := New(orch, 0)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/UNKNOWN-1", nil)
	req.SetPathValue("identifier", "UNKNOWN-1")
	w := httptest.NewRecorder()

	srv.handleIssue(w, req)

	if w.Result().StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Result().StatusCode)
	}
}

// TestHandleIssueCaseInsensitive verifies that identifier matching is case-insensitive.
func TestHandleIssueCaseInsensitive(t *testing.T) {
	entry := makeEntry("id-2", "ENG-99", "started")
	orch := &fakeOrch{
		snap: orchestrator.StateSnapshot{
			Running: map[string]*orchestrator.RunningEntry{
				"id-2": entry,
			},
			Retries: map[string]*orchestrator.RetryEntry{},
		},
	}

	srv := New(orch, 0)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/eng-99", nil)
	req.SetPathValue("identifier", "eng-99")
	w := httptest.NewRecorder()

	srv.handleIssue(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Result().StatusCode)
	}
}

// TestHandleDashboardEscapesHTML verifies that user-controlled values are HTML-escaped
// in the dashboard to prevent XSS.
func TestHandleDashboardEscapesHTML(t *testing.T) {
	entry := &orchestrator.RunningEntry{
		IssueID:         "id-xss",
		IssueIdentifier: `<script>alert("xss")</script>`,
		LastEvent:       `<b>bold</b>`,
		TurnCount:       1,
		TotalTokens:     50,
	}
	retryEntry := &orchestrator.RetryEntry{
		IssueID:    "id-retry-xss",
		Identifier: `<img src=x onerror=alert(1)>`,
		Attempt:    1,
		DueAtMs:    12345,
	}

	orch := &fakeOrch{
		snap: orchestrator.StateSnapshot{
			Running: map[string]*orchestrator.RunningEntry{
				"id-xss": entry,
			},
			Retries: map[string]*orchestrator.RetryEntry{
				"id-retry-xss": retryEntry,
			},
		},
	}

	srv := New(orch, 0)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	srv.handleDashboard(w, req)

	body := w.Body.String()

	// Raw HTML/JS must not appear unescaped
	if strings.Contains(body, `<script>`) {
		t.Error("dashboard contains unescaped <script> tag (XSS vulnerability)")
	}
	if strings.Contains(body, `<b>`) {
		t.Error("dashboard contains unescaped <b> tag")
	}
	if strings.Contains(body, `<img`) {
		t.Error("dashboard contains unescaped <img> tag (XSS vulnerability)")
	}

	// Escaped versions must be present
	if !strings.Contains(body, `&lt;script&gt;`) {
		t.Error("expected escaped &lt;script&gt; in dashboard output")
	}
}

// TestHandleStateJSONSafe verifies that handleState returns valid JSON and uses the
// same JSON-safe projection as handleIssue (no CancelFn leakage).
func TestHandleStateJSONSafe(t *testing.T) {
	entry := makeEntry("id-3", "ENG-55", "message")
	orch := &fakeOrch{
		snap: orchestrator.StateSnapshot{
			Running: map[string]*orchestrator.RunningEntry{
				"id-3": entry,
			},
			Retries: map[string]*orchestrator.RetryEntry{},
		},
	}

	srv := New(orch, 0)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/state", nil)
	w := httptest.NewRecorder()

	srv.handleState(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}

	runningList, ok := result["running"].([]any)
	if !ok || len(runningList) == 0 {
		t.Fatalf("expected non-empty running list")
	}

	item, ok := runningList[0].(map[string]any)
	if !ok {
		t.Fatalf("expected running item to be a JSON object")
	}

	if _, ok := item["CancelFn"]; ok {
		t.Error("CancelFn must not appear in JSON output")
	}
}
