package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestSingleReaderGoroutine verifies that the scanner is driven by a single
// goroutine so timeouts do not cause concurrent reads (which would be a data
// race on bufio.Scanner).
func TestSingleReaderGoroutine(t *testing.T) {
	// Build a pipe: the writer side lets us control exactly when lines arrive.
	pr, pw := io.Pipe()

	scanner := bufio.NewScanner(pr)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type scanResult struct {
		line string
		err  error
	}
	lineCh := make(chan scanResult, 1)

	var goroutineCount atomic.Int32
	go func() {
		goroutineCount.Add(1)
		defer goroutineCount.Add(-1)
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

	readMsg := func(timeoutMs int) (string, error) {
		select {
		case <-time.After(time.Duration(timeoutMs) * time.Millisecond):
			return "", fmt.Errorf("read timeout after %dms", timeoutMs)
		case r := <-lineCh:
			return r.line, r.err
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	// First call times out (no data written yet).
	_, err := readMsg(10)
	if err == nil || !strings.Contains(err.Error(), "read timeout") {
		t.Fatalf("expected timeout error, got %v", err)
	}

	// Verify exactly one reader goroutine is active.
	if n := goroutineCount.Load(); n != 1 {
		t.Fatalf("expected 1 reader goroutine, got %d", n)
	}

	// Write two lines; both must be received in order.
	lines := []string{`{"jsonrpc":"2.0","id":1}`, `{"jsonrpc":"2.0","id":2}`}
	go func() {
		for _, l := range lines {
			fmt.Fprintln(pw, l)
		}
	}()

	for i, want := range lines {
		got, err := readMsg(500)
		if err != nil {
			t.Fatalf("readMsg[%d]: unexpected error: %v", i, err)
		}
		if got != want {
			t.Fatalf("readMsg[%d]: want %q, got %q", i, want, got)
		}
	}

	// Goroutine count must still be 1 – no additional goroutines were spawned.
	if n := goroutineCount.Load(); n != 1 {
		t.Fatalf("expected 1 reader goroutine after reads, got %d", n)
	}

	// Close the pipe and expect EOF on the channel.
	_ = pw.Close()
	_, err = readMsg(500)
	if err != io.EOF {
		t.Fatalf("expected io.EOF after pipe close, got %v", err)
	}
}

// TestReadMsgOrdering verifies that messages received through lineCh arrive in
// the same order they were written, even when timeouts occur between reads.
func TestReadMsgOrdering(t *testing.T) {
	pr, pw := io.Pipe()
	scanner := bufio.NewScanner(pr)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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

	readMsg := func(timeoutMs int) (string, error) {
		select {
		case <-time.After(time.Duration(timeoutMs) * time.Millisecond):
			return "", fmt.Errorf("read timeout after %dms", timeoutMs)
		case r := <-lineCh:
			return r.line, r.err
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	const msgCount = 20
	// Write all messages before any are read.
	go func() {
		for i := 0; i < msgCount; i++ {
			msg := map[string]any{"jsonrpc": "2.0", "id": i}
			b, _ := json.Marshal(msg)
			fmt.Fprintln(pw, string(b))
		}
	}()

	for i := 0; i < msgCount; i++ {
		line, err := readMsg(500)
		if err != nil {
			t.Fatalf("msg %d: unexpected error: %v", i, err)
		}
		var msg map[string]any
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("msg %d: parse error: %v", i, err)
		}
		gotID := int(msg["id"].(float64))
		if gotID != i {
			t.Fatalf("msg %d: expected id=%d, got id=%d", i, i, gotID)
		}
	}
}
