<?php
declare(strict_types=1);

namespace Symphony\Workspace;

use Symphony\Config\Config;

class WorkspaceManager
{
    private string $workspaceRoot;

    public function __construct(private readonly Config $config)
    {
        $this->workspaceRoot = $config->getWorkspaceRoot();
    }

    public function sanitizeKey(string $identifier): string
    {
        // Replace path separators with double underscore
        $result = str_replace(['/', '\\'], '__', $identifier);
        // Replace any remaining non-allowed characters with single underscore
        $result = preg_replace('/[^A-Za-z0-9._\-]/', '_', $result);
        // Replace dot-dot sequences to prevent path traversal
        $result = preg_replace('/\.{2,}/', '_', $result);
        return $result;
    }

    public function getWorkspacePath(string $identifier): string
    {
        $key = $this->sanitizeKey($identifier);
        if (!preg_match('/^[A-Za-z0-9._\-]+$/', $key)) {
            throw new \RuntimeException("Invalid workspace key: {$key}");
        }
        return $this->workspaceRoot . DIRECTORY_SEPARATOR . $key;
    }

    public function createForIssue(string $identifier): array
    {
        $path = $this->getWorkspacePath($identifier);
        
        $this->assertPathSafety($path);

        $createdNow = false;
        if (!is_dir($path)) {
            if (!mkdir($path, 0755, true)) {
                throw new \RuntimeException("Failed to create workspace directory: {$path}");
            }
            $createdNow = true;
        }

        if ($createdNow) {
            $this->runHook('after_create', $path);
        }

        return ['path' => $path, 'created_now' => $createdNow];
    }

    public function cleanupWorkspace(string $identifier): void
    {
        $path = $this->getWorkspacePath($identifier);
        $this->assertPathSafety($path);

        if (is_dir($path)) {
            $this->runHook('before_remove', $path);
            $this->removeDirectory($path);
        }
    }

    public function runHook(string $hookName, string $workspacePath): void
    {
        $script = $this->config->getHook($hookName);
        if ($script === null || trim($script) === '') {
            return;
        }

        $timeoutMs = $this->config->getHookTimeoutMs();
        $timeoutSec = (int)ceil($timeoutMs / 1000);

        $descriptors = [
            0 => ['pipe', 'r'],
            1 => ['pipe', 'w'],
            2 => ['pipe', 'w'],
        ];

        $proc = proc_open(
            ['sh', '-lc', $script],
            $descriptors,
            $pipes,
            $workspacePath,
            null
        );

        if (!is_resource($proc)) {
            throw new \RuntimeException("Failed to run hook {$hookName}");
        }

        fclose($pipes[0]);

        $start = time();
        while (true) {
            $status = proc_get_status($proc);
            if (!$status['running']) {
                break;
            }
            if (time() - $start > $timeoutSec) {
                proc_terminate($proc);
                break;
            }
            usleep(100000);
        }

        fclose($pipes[1]);
        fclose($pipes[2]);
        proc_close($proc);
    }

    private function assertPathSafety(string $path): void
    {
        $root = rtrim($this->workspaceRoot, DIRECTORY_SEPARATOR);
        
        if (!str_starts_with($path, $root . DIRECTORY_SEPARATOR) && $path !== $root) {
            throw new \RuntimeException(
                "Workspace path safety violation: '{$path}' is outside workspace root '{$root}'"
            );
        }
    }

    private function removeDirectory(string $path): void
    {
        if (!is_dir($path)) {
            return;
        }
        $items = scandir($path);
        foreach ($items as $item) {
            if ($item === '.' || $item === '..') {
                continue;
            }
            $full = $path . DIRECTORY_SEPARATOR . $item;
            if (is_dir($full) && !is_link($full)) {
                $this->removeDirectory($full);
            } else {
                unlink($full);
            }
        }
        rmdir($path);
    }
}
