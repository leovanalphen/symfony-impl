package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/leovanalphen/symfony-impl/internal/orchestrator"
)

type mockOrch struct {
	snap orchestrator.StateSnapshot
}

func (m *mockOrch) Snapshot() orchestrator.StateSnapshot { return m.snap }
func (m *mockOrch) Refresh()                             {}

func TestHandleDashboard_XSSEscaping(t *testing.T) {
	xssPayload := `<script>alert('xss')</script>`
	snap := orchestrator.StateSnapshot{
		Running: map[string]*orchestrator.RunningEntry{
			"1": {
				IssueIdentifier: xssPayload,
				LastEvent:       xssPayload,
				TurnCount:       1,
				TotalTokens:     100,
			},
		},
		Retries: map[string]*orchestrator.RetryEntry{
			"1": {
				Identifier: xssPayload,
				Attempt:    1,
				DueAtMs:    time.Now().UnixMilli(),
			},
		},
	}

	s := New(&mockOrch{snap: snap}, 0)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	s.handleDashboard(rec, req)

	body := rec.Body.String()

	// The raw script tag must not appear in the output.
	if strings.Contains(body, xssPayload) {
		t.Errorf("dashboard output contains unescaped XSS payload: %s", xssPayload)
	}

	// The escaped form should appear instead.
	escaped := "&lt;script&gt;alert(&#39;xss&#39;)&lt;/script&gt;"
	if !strings.Contains(body, escaped) {
		t.Errorf("dashboard output does not contain escaped payload; body:\n%s", body)
	}
}
