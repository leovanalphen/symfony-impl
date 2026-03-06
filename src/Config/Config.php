<?php
declare(strict_types=1);

namespace Symphony\Config;

class Config
{
    public function __construct(private readonly array $raw) {}

    private function get(string $key, mixed $default = null): mixed
    {
        $parts = explode('.', $key);
        $current = $this->raw;
        foreach ($parts as $part) {
            if (!is_array($current) || !array_key_exists($part, $current)) {
                return $default;
            }
            $current = $current[$part];
        }
        return $current;
    }

    public function resolveValue(mixed $value): mixed
    {
        if (!is_string($value)) {
            return $value;
        }
        if (str_starts_with($value, '$')) {
            $envName = substr($value, 1);
            $envVal = getenv($envName);
            return $envVal !== false ? $envVal : $value;
        }
        return $value;
    }

    public function resolvePath(string $value): string
    {
        if (!str_contains($value, '/') && !str_contains($value, '\\')) {
            return $value;
        }
        if (str_starts_with($value, '~/') || $value === '~') {
            $home = getenv('HOME');
            if ($home === false) {
                if (function_exists('posix_getpwuid') && function_exists('posix_getuid')) {
                    $info = posix_getpwuid(posix_getuid());
                    $home = $info['dir'] ?? sys_get_temp_dir();
                } else {
                    $home = sys_get_temp_dir();
                }
            }
            $value = $home . substr($value, 1);
        }
        $value = preg_replace_callback('/\$([A-Za-z_][A-Za-z0-9_]*)/', function ($m) {
            $envVal = getenv($m[1]);
            return $envVal !== false ? $envVal : $m[0];
        }, $value);
        return $value;
    }

    public function getTrackerKind(): string
    {
        return (string)($this->resolveValue($this->get('tracker.kind', 'linear')) ?? 'linear');
    }

    public function getTrackerEndpoint(): string
    {
        return (string)($this->resolveValue($this->get('tracker.endpoint', 'https://api.linear.app/graphql')) ?? 'https://api.linear.app/graphql');
    }

    public function getTrackerApiKey(): string
    {
        $val = $this->get('tracker.api_key', '$LINEAR_API_KEY');
        return (string)$this->resolveValue($val);
    }

    public function getTrackerProjectSlug(): string
    {
        return (string)($this->resolveValue($this->get('tracker.project_slug', '')) ?? '');
    }

    public function getActiveStates(): array
    {
        $val = $this->get('tracker.active_states', ['Todo', 'In Progress']);
        if (!is_array($val)) {
            return ['Todo', 'In Progress'];
        }
        return $val;
    }

    public function getTerminalStates(): array
    {
        $val = $this->get('tracker.terminal_states', ['Closed', 'Cancelled', 'Canceled', 'Duplicate', 'Done']);
        if (!is_array($val)) {
            return ['Closed', 'Cancelled', 'Canceled', 'Duplicate', 'Done'];
        }
        return $val;
    }

    public function getPollingIntervalMs(): int
    {
        return (int)($this->get('polling.interval_ms', 30000) ?? 30000);
    }

    public function getWorkspaceRoot(): string
    {
        $val = $this->get('workspace.root', null);
        if ($val === null) {
            return sys_get_temp_dir() . '/symphony_workspaces';
        }
        return $this->resolvePath((string)$val);
    }

    public function getHook(string $name): ?string
    {
        $val = $this->get("hooks.{$name}", null);
        return $val !== null ? (string)$val : null;
    }

    public function getHookTimeoutMs(): int
    {
        return (int)($this->get('hooks.timeout_ms', 60000) ?? 60000);
    }

    public function getMaxConcurrentAgents(): int
    {
        return (int)($this->get('agent.max_concurrent_agents', 10) ?? 10);
    }

    public function getMaxTurns(): int
    {
        return (int)($this->get('agent.max_turns', 20) ?? 20);
    }

    public function getMaxRetryBackoffMs(): int
    {
        return (int)($this->get('agent.max_retry_backoff_ms', 300000) ?? 300000);
    }

    public function getMaxConcurrentAgentsByState(): array
    {
        $val = $this->get('agent.max_concurrent_agents_by_state', []);
        if (!is_array($val)) {
            return [];
        }
        $normalized = [];
        foreach ($val as $state => $limit) {
            $normalized[strtolower((string)$state)] = (int)$limit;
        }
        return $normalized;
    }

    public function getCodexCommand(): string
    {
        return (string)($this->get('codex.command', 'codex app-server') ?? 'codex app-server');
    }

    public function getCodexApprovalPolicy(): string
    {
        return (string)($this->get('codex.approval_policy', 'auto-edit') ?? 'auto-edit');
    }

    public function getCodexThreadSandbox(): string
    {
        return (string)($this->get('codex.thread_sandbox', 'none') ?? 'none');
    }

    public function getCodexTurnSandboxPolicy(): string
    {
        return (string)($this->get('codex.turn_sandbox_policy', 'none') ?? 'none');
    }

    public function getCodexTurnTimeoutMs(): int
    {
        return (int)($this->get('codex.turn_timeout_ms', 3600000) ?? 3600000);
    }

    public function getCodexReadTimeoutMs(): int
    {
        return (int)($this->get('codex.read_timeout_ms', 5000) ?? 5000);
    }

    public function getCodexStallTimeoutMs(): int
    {
        return (int)($this->get('codex.stall_timeout_ms', 300000) ?? 300000);
    }

    public function getServerPort(): ?int
    {
        $val = $this->get('server.port', null);
        return $val !== null ? (int)$val : null;
    }

    public function getRaw(): array
    {
        return $this->raw;
    }
}
