package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"

	"github.com/leovanalphen/symfony-impl/internal/config"
)

// EventType represents a type of agent event.
type EventType string

const (
	EventTurnCompleted EventType = "turn_completed"
	EventTurnFailed    EventType = "turn_failed"
	EventTurnCancelled EventType = "turn_cancelled"
	EventMessage       EventType = "message"
	EventTokenUsage    EventType = "token_usage"
)

// AgentEvent is emitted by the AppServerClient during a run.
type AgentEvent struct {
	Type         EventType
	Message      string
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
	Error        string
}

// RunResult is the result of a single turn run.
type RunResult struct {
	ThreadID     string
	TurnID       string
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
}

type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
}

var requestCounter atomic.Int64

// AppServerClient launches and communicates with a codex app-server subprocess.
type AppServerClient struct {
	cfg           *config.Config
	workspacePath string
	apiKey        string
	eventCh       chan<- AgentEvent
}

// NewAppServerClient creates a new AppServerClient.
func NewAppServerClient(cfg *config.Config, workspacePath, apiKey string, eventCh chan<- AgentEvent) *AppServerClient {
	return &AppServerClient{
		cfg:           cfg,
		workspacePath: workspacePath,
		apiKey:        apiKey,
		eventCh:       eventCh,
	}
}

func nextID() int64 {
	return requestCounter.Add(1)
}

// Run launches the app server, runs a single turn, and returns the result.
func (c *AppServerClient) Run(ctx context.Context, threadID, prompt string) (RunResult, error) {
	cmdStr := c.cfg.CodexCommand()
	cmd := exec.CommandContext(ctx, "bash", "-lc", cmdStr)
	cmd.Dir = c.workspacePath
	cmd.Env = os.Environ()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return RunResult{}, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return RunResult{}, fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return RunResult{}, fmt.Errorf("start app server: %w", err)
	}
	defer func() {
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	slog.Debug("app server started", "pid", cmd.Process.Pid)

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	// Single reader goroutine: reads lines from the scanner and sends them to
	// lineCh. This prevents concurrent scanner access that would occur if each
	// readMsg call spawned its own goroutine.
	type scanResult struct {
		line string
		err  error
	}
	lineCh := make(chan scanResult, 1)
	go func() {
		for scanner.Scan() {
			select {
			case lineCh <- scanResult{line: scanner.Text()}:
			case <-ctx.Done():
				return
			}
		}
		err := scanner.Err()
		if err == nil {
			err = io.EOF
		}
		select {
		case lineCh <- scanResult{err: err}:
		case <-ctx.Done():
		}
	}()

	sendMsg := func(msg jsonRPCRequest) error {
		b, err := json.Marshal(msg)
		if err != nil {
			return err
		}
		b = append(b, '\n')
		_, err = stdin.Write(b)
		return err
	}

	readMsg := func(timeoutMs int) (*jsonRPCResponse, error) {
		select {
		case <-time.After(time.Duration(timeoutMs) * time.Millisecond):
			return nil, fmt.Errorf("read timeout after %dms", timeoutMs)
		case r := <-lineCh:
			if r.err != nil {
				return nil, r.err
			}
			var resp jsonRPCResponse
			if err := json.Unmarshal([]byte(r.line), &resp); err != nil {
				return nil, fmt.Errorf("parse response: %w", err)
			}
			return &resp, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// Initialize
	initID := nextID()
	if err := sendMsg(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      initID,
		Method:  "initialize",
		Params: map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "symphony", "version": "1.0"},
		},
	}); err != nil {
		return RunResult{}, fmt.Errorf("send initialize: %w", err)
	}

	resp, err := readMsg(c.cfg.CodexReadTimeoutMs())
	if err != nil {
		return RunResult{}, fmt.Errorf("read initialize response: %w", err)
	}
	if resp.Error != nil {
		return RunResult{}, fmt.Errorf("initialize error: %s", resp.Error.Message)
	}

	// Send initialized notification
	if err := sendMsg(jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}); err != nil {
		return RunResult{}, fmt.Errorf("send initialized: %w", err)
	}

	// Start thread if needed
	if threadID == "" {
		threadStartID := nextID()
		if err := sendMsg(jsonRPCRequest{
			JSONRPC: "2.0",
			ID:      threadStartID,
			Method:  "thread/start",
			Params:  map[string]any{},
		}); err != nil {
			return RunResult{}, fmt.Errorf("send thread/start: %w", err)
		}

		resp, err = readMsg(c.cfg.CodexReadTimeoutMs())
		if err != nil {
			return RunResult{}, fmt.Errorf("read thread/start response: %w", err)
		}
		if resp.Error != nil {
			return RunResult{}, fmt.Errorf("thread/start error: %s", resp.Error.Message)
		}

		var threadResult struct {
			ThreadID string `json:"threadId"`
		}
		if resp.Result != nil {
			_ = json.Unmarshal(resp.Result, &threadResult)
		}
		threadID = threadResult.ThreadID
	}

	// Start turn
	turnStartID := nextID()
	if err := sendMsg(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      turnStartID,
		Method:  "turn/start",
		Params: map[string]any{
			"threadId": threadID,
			"input":    prompt,
		},
	}); err != nil {
		return RunResult{}, fmt.Errorf("send turn/start: %w", err)
	}

	// Read turn response and subsequent events
	var result RunResult
	result.ThreadID = threadID

	turnTimeout := time.Duration(c.cfg.CodexTurnTimeoutMs()) * time.Millisecond
	stallTimeout := time.Duration(c.cfg.CodexStallTimeoutMs()) * time.Millisecond
	turnDeadline := time.Now().Add(turnTimeout)
	stallDeadline := time.Now().Add(stallTimeout)

	for {
		remaining := time.Until(turnDeadline)
		stallRemaining := time.Until(stallDeadline)
		timeoutMs := remaining
		if stallRemaining < timeoutMs {
			timeoutMs = stallRemaining
		}
		if timeoutMs <= 0 {
			return RunResult{}, fmt.Errorf("turn timed out")
		}

		msg, err := readMsg(int(timeoutMs.Milliseconds()))
		if err != nil {
			return RunResult{}, fmt.Errorf("read turn event: %w", err)
		}

		stallDeadline = time.Now().Add(stallTimeout)

		method := msg.Method
		if method == "" && msg.Result != nil {
			var turnResp struct {
				TurnID string `json:"turnId"`
			}
			_ = json.Unmarshal(msg.Result, &turnResp)
			if turnResp.TurnID != "" {
				result.TurnID = turnResp.TurnID
			}
			continue
		}

		switch method {
		case "turn/completed":
			var params struct {
				TurnID string `json:"turnId"`
				Usage  struct {
					InputTokens  int64 `json:"inputTokens"`
					OutputTokens int64 `json:"outputTokens"`
					TotalTokens  int64 `json:"totalTokens"`
				} `json:"usage"`
			}
			if msg.Params != nil {
				_ = json.Unmarshal(msg.Params, &params)
			}
			if params.TurnID != "" {
				result.TurnID = params.TurnID
			}
			result.InputTokens = params.Usage.InputTokens
			result.OutputTokens = params.Usage.OutputTokens
			result.TotalTokens = params.Usage.TotalTokens
			c.emit(AgentEvent{
				Type:         EventTurnCompleted,
				InputTokens:  result.InputTokens,
				OutputTokens: result.OutputTokens,
				TotalTokens:  result.TotalTokens,
			})
			return result, nil

		case "turn/failed":
			var params struct {
				Error string `json:"error"`
			}
			if msg.Params != nil {
				_ = json.Unmarshal(msg.Params, &params)
			}
			c.emit(AgentEvent{Type: EventTurnFailed, Error: params.Error})
			return RunResult{}, fmt.Errorf("turn failed: %s", params.Error)

		case "turn/cancelled":
			c.emit(AgentEvent{Type: EventTurnCancelled})
			return RunResult{}, fmt.Errorf("turn cancelled")

		case "approval/request":
			var params struct {
				ID string `json:"id"`
			}
			if msg.Params != nil {
				_ = json.Unmarshal(msg.Params, &params)
			}
			approvalID := nextID()
			_ = sendMsg(jsonRPCRequest{
				JSONRPC: "2.0",
				ID:      approvalID,
				Method:  "approval/respond",
				Params: map[string]any{
					"id":       params.ID,
					"approved": true,
				},
			})

		case "item/tool/requestUserInput":
			c.emit(AgentEvent{Type: EventTurnFailed, Error: "agent requested user input"})
			return RunResult{}, fmt.Errorf("agent requested user input")

		case "item/tool/call":
			var params struct {
				ID       string          `json:"id"`
				ToolName string          `json:"toolName"`
				Input    json.RawMessage `json:"input"`
			}
			if msg.Params != nil {
				_ = json.Unmarshal(msg.Params, &params)
			}

			if params.ToolName == "linear_graphql" && c.apiKey != "" {
				resultStr, toolErr := c.handleLinearGraphQL(ctx, params.Input)
				callID := nextID()
				var toolResult any
				if toolErr != nil {
					toolResult = map[string]any{"error": toolErr.Error()}
				} else {
					toolResult = map[string]any{"result": resultStr}
				}
				_ = sendMsg(jsonRPCRequest{
					JSONRPC: "2.0",
					ID:      callID,
					Method:  "item/tool/result",
					Params: map[string]any{
						"id":     params.ID,
						"result": toolResult,
					},
				})
			} else {
				callID := nextID()
				_ = sendMsg(jsonRPCRequest{
					JSONRPC: "2.0",
					ID:      callID,
					Method:  "item/tool/result",
					Params: map[string]any{
						"id": params.ID,
						"result": map[string]any{
							"error": fmt.Sprintf("tool %q not supported", params.ToolName),
						},
					},
				})
			}

		default:
			if method == "item/message" || strings.HasPrefix(method, "item/") {
				var params struct {
					Content string `json:"content"`
				}
				if msg.Params != nil {
					_ = json.Unmarshal(msg.Params, &params)
				}
				if params.Content != "" {
					c.emit(AgentEvent{Type: EventMessage, Message: params.Content})
				}
			}
		}
	}
}

func (c *AppServerClient) handleLinearGraphQL(ctx context.Context, inputRaw json.RawMessage) (string, error) {
	var inputMap map[string]any
	if err := json.Unmarshal(inputRaw, &inputMap); err != nil {
		return "", fmt.Errorf("invalid linear_graphql input: %w", err)
	}

	query, _ := inputMap["query"].(string)
	variables, _ := inputMap["variables"].(map[string]any)

	endpoint := c.cfg.TrackerEndpoint()
	reqBody := map[string]any{"query": query}
	if len(variables) > 0 {
		reqBody["variables"] = variables
	}

	b, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", c.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	resultBytes, _ := json.Marshal(result)
	return string(resultBytes), nil
}

func (c *AppServerClient) emit(event AgentEvent) {
	select {
	case c.eventCh <- event:
	default:
	}
}
