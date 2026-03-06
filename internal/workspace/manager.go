package workspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var nonSafe = regexp.MustCompile(`[^A-Za-z0-9._\-]`)

// SanitizeKey replaces characters not in [A-Za-z0-9._-] with underscore.
func SanitizeKey(identifier string) string {
	return nonSafe.ReplaceAllString(identifier, "_")
}

// WorkspacePath returns the path for a workspace identified by identifier under root.
func WorkspacePath(root, identifier string) string {
	return filepath.Join(root, SanitizeKey(identifier))
}

// checkSafety verifies that path is under root.
func checkSafety(root, path string) error {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(absPath, absRoot+string(filepath.Separator)) {
		return fmt.Errorf("workspace path %q is not under root %q", absPath, absRoot)
	}
	return nil
}

// EnsureWorkspace creates the workspace directory if it doesn't exist.
// Returns the path, whether it was just created, and any error.
func EnsureWorkspace(ctx context.Context, root, identifier string) (string, bool, error) {
	path := WorkspacePath(root, identifier)
	if err := checkSafety(root, path); err != nil {
		return "", false, err
	}

	info, err := os.Stat(path)
	if err == nil && info.IsDir() {
		return path, false, nil
	}

	if err := os.MkdirAll(path, 0o755); err != nil {
		return "", false, fmt.Errorf("failed to create workspace: %w", err)
	}

	return path, true, nil
}

// RunHook runs a shell hook script in the given workspace directory.
func RunHook(ctx context.Context, hookScript, workspacePath string, timeoutMs int) error {
	if hookScript == "" {
		return nil
	}

	timeout := time.Duration(timeoutMs) * time.Millisecond
	hookCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(hookCtx, "bash", "-c", hookScript)
	cmd.Dir = workspacePath
	cmd.Env = append(os.Environ(), fmt.Sprintf("WORKSPACE_PATH=%s", workspacePath))

	if err := cmd.Run(); err != nil {
		if hookCtx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("hook timed out after %dms", timeoutMs)
		}
		return fmt.Errorf("hook failed: %w", err)
	}

	return nil
}

// CleanWorkspace removes the workspace directory, optionally running a pre-remove hook.
func CleanWorkspace(ctx context.Context, root, identifier, beforeRemoveHook string, hookTimeoutMs int) error {
	path := WorkspacePath(root, identifier)
	if err := checkSafety(root, path); err != nil {
		return err
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}

	if err := RunHook(ctx, beforeRemoveHook, path, hookTimeoutMs); err != nil {
		return fmt.Errorf("before_remove hook failed: %w", err)
	}

	return os.RemoveAll(path)
}
