<?php
declare(strict_types=1);

namespace Symphony\Orchestrator;

class OrchestratorState
{
    /** @var array<string, RunningEntry> */
    public array $running = [];

    /** @var array<string, true> */
    public array $claimed = [];

    /** @var array<string, RetryEntry> */
    public array $retryAttempts = [];

    /** @var array<string, true> */
    public array $completed = [];

    public array $codexTotals = [
        'input_tokens' => 0,
        'output_tokens' => 0,
        'total_tokens' => 0,
        'seconds_running' => 0,
    ];

    public ?array $codexRateLimits = null;

    public function toArray(): array
    {
        $running = [];
        foreach ($this->running as $id => $entry) {
            $running[$id] = [
                'issue_id' => $entry->issueId,
                'identifier' => $entry->identifier,
                'pid' => $entry->pid,
                'session_id' => $entry->sessionId,
                'started_at' => $entry->startedAt,
                'last_event_at' => $entry->lastEventAt,
                'attempt' => $entry->attempt,
            ];
        }

        $retries = [];
        foreach ($this->retryAttempts as $id => $entry) {
            $retries[$id] = [
                'attempt' => $entry->attempt,
                'retry_after' => $entry->retryAfter,
                'was_normal' => $entry->wasNormal,
            ];
        }

        return [
            'running' => $running,
            'claimed' => array_keys($this->claimed),
            'retry_attempts' => $retries,
            'completed' => array_keys($this->completed),
            'codex_totals' => $this->codexTotals,
            'codex_rate_limits' => $this->codexRateLimits,
        ];
    }
}
