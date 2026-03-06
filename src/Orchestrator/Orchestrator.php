<?php
declare(strict_types=1);

namespace Symphony\Orchestrator;

use Symphony\Config\Config;
use Symphony\Domain\Issue;
use Symphony\Tracker\LinearClient;
use Symphony\Workspace\WorkspaceManager;
use Twig\Environment;
use Twig\Loader\ArrayLoader;

class Orchestrator
{
    private OrchestratorState $state;
    private bool $running = true;
    private ?string $stateFile = null;

    public function __construct(
        private readonly Config $config,
        private readonly LinearClient $tracker,
        private readonly WorkspaceManager $workspaceManager,
        private readonly string $promptTemplate,
        private readonly ?string $logsRoot = null,
    ) {
        $this->state = new OrchestratorState();
    }

    public function setStateFile(string $path): void
    {
        $this->stateFile = $path;
    }

    public function stop(): void
    {
        $this->running = false;
    }

    public function run(): void
    {
        if (function_exists('pcntl_signal')) {
            pcntl_signal(SIGTERM, function () { $this->running = false; });
            pcntl_signal(SIGINT, function () { $this->running = false; });
        }

        $this->log('level=info msg=orchestrator_started');

        while ($this->running) {
            if (function_exists('pcntl_signal_dispatch')) {
                pcntl_signal_dispatch();
            }

            $this->tick();
            $this->saveState();

            $intervalMs = $this->config->getPollingIntervalMs();
            usleep($intervalMs * 1000);
        }

        $this->shutdown();
    }

    private function tick(): void
    {
        $this->processWorkerEvents();
        $this->reconcileRunning();

        try {
            $candidates = $this->tracker->fetchCandidateIssues();
        } catch (\Throwable $e) {
            $this->log("level=error msg=fetch_failed error=" . json_encode($e->getMessage()));
            return;
        }

        $sorted = $this->sortIssues($candidates);
        $this->dispatchEligible($sorted);
    }

    private function processWorkerEvents(): void
    {
        if (empty($this->state->running)) {
            return;
        }

        $pipes = [];
        $entryMap = [];
        foreach ($this->state->running as $issueId => $entry) {
            if (is_resource($entry->pipe)) {
                $pipes[] = $entry->pipe;
                $entryMap[(int)$entry->pipe] = $issueId;
            }
        }

        if (empty($pipes)) {
            return;
        }

        $read = $pipes;
        $write = $except = null;
        $ready = @stream_select($read, $write, $except, 0, 100000);

        if ($ready === false || $ready === 0) {
            return;
        }

        foreach ($read as $pipe) {
            $pipeId = (int)$pipe;
            if (!isset($entryMap[$pipeId])) {
                continue;
            }
            $issueId = $entryMap[$pipeId];
            if (!isset($this->state->running[$issueId])) {
                continue;
            }
            $entry = $this->state->running[$issueId];

            $chunk = fread($pipe, 4096);
            if ($chunk === false || $chunk === '') {
                $status = proc_get_status($entry->process);
                if (!$status['running']) {
                    $this->handleWorkerExit($issueId, $entry);
                }
                continue;
            }

            $entry->buffer .= $chunk;
            $entry->lastEventAt = $this->nowMs();

            while (($pos = strpos($entry->buffer, "\n")) !== false) {
                $line = substr($entry->buffer, 0, $pos);
                $entry->buffer = substr($entry->buffer, $pos + 1);
                $this->handleWorkerEvent($issueId, $line);
            }
        }
    }

    private function handleWorkerEvent(string $issueId, string $line): void
    {
        $event = json_decode($line, true);
        if (!is_array($event)) {
            return;
        }

        $eventType = $event['event'] ?? '';
        $this->log("level=debug msg=worker_event issue_id={$issueId} event={$eventType}");

        switch ($eventType) {
            case 'session_started':
                if (isset($this->state->running[$issueId])) {
                    $this->state->running[$issueId]->sessionId = $event['session_id'] ?? null;
                }
                break;

            case 'turn_completed':
                $usage = $event['usage'] ?? [];
                $this->state->codexTotals['input_tokens'] += (int)($usage['input_tokens'] ?? 0);
                $this->state->codexTotals['output_tokens'] += (int)($usage['output_tokens'] ?? 0);
                $this->state->codexTotals['total_tokens'] += (int)($usage['total_tokens'] ?? 0);
                break;

            case 'worker_exit':
                $wasNormal = (bool)($event['normal'] ?? false);
                $attempt = (int)($event['attempt'] ?? 1);
                $this->scheduleRetry($issueId, $wasNormal, $attempt);
                break;
        }
    }

    private function handleWorkerExit(string $issueId, RunningEntry $entry): void
    {
        $this->log("level=info msg=worker_process_exited issue_id={$issueId}");
        fclose($entry->pipe);
        proc_close($entry->process);
        unset($this->state->running[$issueId]);
        unset($this->state->claimed[$issueId]);
    }

    private function scheduleRetry(string $issueId, bool $wasNormal, int $attempt): void
    {
        $delay = $this->calculateRetryDelay($wasNormal, $attempt, $this->config->getMaxRetryBackoffMs());
        $this->state->retryAttempts[$issueId] = new RetryEntry(
            attempt: $attempt + 1,
            retryAfter: $this->nowMs() + $delay,
            wasNormal: $wasNormal,
        );
    }

    public static function calculateRetryDelay(bool $wasNormal, int $attempt, int $maxBackoffMs): int
    {
        if ($wasNormal) {
            return 1000;
        }
        $delay = (int)(10000 * (2 ** ($attempt - 1)));
        return min($delay, $maxBackoffMs);
    }

    private function reconcileRunning(): void
    {
        $nowMs = $this->nowMs();
        $stallTimeoutMs = $this->config->getCodexStallTimeoutMs();

        foreach ($this->state->running as $issueId => $entry) {
            $elapsed = $nowMs - $entry->lastEventAt;
            if ($elapsed > $stallTimeoutMs) {
                $this->log("level=warn msg=stall_detected issue_id={$issueId}");
                proc_terminate($entry->process);
                fclose($entry->pipe);
                proc_close($entry->process);
                unset($this->state->running[$issueId]);
                unset($this->state->claimed[$issueId]);
                $this->scheduleRetry($issueId, false, $entry->attempt);
            }
        }
    }

    public static function sortIssues(array $issues): array
    {
        usort($issues, function (Issue $a, Issue $b) {
            if ($a->priority !== $b->priority) {
                if ($a->priority === null) return 1;
                if ($b->priority === null) return -1;
                return $a->priority <=> $b->priority;
            }
            if ($a->createdAt !== null && $b->createdAt !== null) {
                $cmp = $a->createdAt->getTimestamp() <=> $b->createdAt->getTimestamp();
                if ($cmp !== 0) return $cmp;
            } elseif ($a->createdAt !== null) {
                return -1;
            } elseif ($b->createdAt !== null) {
                return 1;
            }
            return strcmp($a->identifier, $b->identifier);
        });
        return $issues;
    }

    public static function isEligible(
        Issue $issue,
        array $activeStates,
        array $terminalStates,
        array $running,
        array $claimed,
        array $maxByState,
    ): bool {
        if (empty($issue->id) || empty($issue->identifier) || empty($issue->title) || empty($issue->state)) {
            return false;
        }

        if (!in_array($issue->state, $activeStates, true)) {
            return false;
        }

        if (in_array($issue->state, $terminalStates, true)) {
            return false;
        }

        if (isset($running[$issue->id]) || isset($claimed[$issue->id])) {
            return false;
        }

        $stateLower = strtolower($issue->state);
        if (isset($maxByState[$stateLower])) {
            $countInState = 0;
            foreach ($running as $runEntry) {
                if (is_array($runEntry) && isset($runEntry['state'])) {
                    if (strtolower($runEntry['state']) === $stateLower) {
                        $countInState++;
                    }
                }
            }
            if ($countInState >= $maxByState[$stateLower]) {
                return false;
            }
        }

        if (strtolower($issue->state) === 'todo') {
            foreach ($issue->blockedBy as $blocker) {
                if ($blocker->state === null || !in_array($blocker->state, $terminalStates, true)) {
                    return false;
                }
            }
        }

        return true;
    }

    private function dispatchEligible(array $sortedIssues): void
    {
        $maxConcurrent = $this->config->getMaxConcurrentAgents();
        $maxByState = $this->config->getMaxConcurrentAgentsByState();
        $activeStates = $this->config->getActiveStates();
        $terminalStates = $this->config->getTerminalStates();
        $nowMs = $this->nowMs();

        foreach ($sortedIssues as $issue) {
            if (count($this->state->running) >= $maxConcurrent) {
                break;
            }

            if (isset($this->state->retryAttempts[$issue->id])) {
                $retry = $this->state->retryAttempts[$issue->id];
                if ($nowMs < $retry->retryAfter) {
                    continue;
                }
            }

            $runningWithState = [];
            foreach ($this->state->running as $id => $entry) {
                $runningWithState[$id] = ['state' => ''];
            }

            if (!self::isEligible($issue, $activeStates, $terminalStates, $this->state->running, $this->state->claimed, $maxByState)) {
                continue;
            }

            $this->dispatch($issue);
        }
    }

    private function dispatch(Issue $issue): void
    {
        try {
            $workspace = $this->workspaceManager->createForIssue($issue->identifier);
            $workspacePath = $workspace['path'];

            $attempt = isset($this->state->retryAttempts[$issue->id])
                ? $this->state->retryAttempts[$issue->id]->attempt
                : 1;

            $prompt = $this->renderPrompt($issue);

            $issueData = [
                'id' => $issue->id,
                'identifier' => $issue->identifier,
                'title' => $issue->title,
                'description' => $issue->description,
                'priority' => $issue->priority,
                'state' => $issue->state,
                'branchName' => $issue->branchName,
                'url' => $issue->url,
                'labels' => $issue->labels,
                'blockedBy' => array_map(fn($b) => ['id' => $b->id, 'identifier' => $b->identifier, 'state' => $b->state], $issue->blockedBy),
                'createdAt' => $issue->createdAt?->format('c'),
                'updatedAt' => $issue->updatedAt?->format('c'),
            ];

            $args = json_encode([
                'config' => $this->config->getRaw(),
                'issue' => $issueData,
                'workspace_path' => $workspacePath,
                'prompt' => $prompt,
                'attempt' => $attempt,
            ]);

            $workerScript = __DIR__ . '/../Worker/WorkerScript.php';
            $descriptors = [
                0 => ['pipe', 'r'],
                1 => ['pipe', 'w'],
                2 => STDERR,
            ];

            $proc = proc_open(
                ['php', $workerScript, $args],
                $descriptors,
                $pipes,
                $workspacePath,
                null
            );

            if (!is_resource($proc)) {
                throw new \RuntimeException("Failed to spawn worker for {$issue->identifier}");
            }

            fclose($pipes[0]);
            stream_set_blocking($pipes[1], false);

            $status = proc_get_status($proc);
            $pid = $status['pid'];

            $entry = new RunningEntry(
                issueId: $issue->id,
                identifier: $issue->identifier,
                pipe: $pipes[1],
                process: $proc,
                pid: $pid,
                startedAt: $this->nowMs(),
                lastEventAt: $this->nowMs(),
                attempt: $attempt,
            );

            $this->state->running[$issue->id] = $entry;
            $this->state->claimed[$issue->id] = true;
            unset($this->state->retryAttempts[$issue->id]);

            $this->log("level=info msg=dispatched issue_id={$issue->id} identifier={$issue->identifier} pid={$pid}");
        } catch (\Throwable $e) {
            $this->log("level=error msg=dispatch_failed issue_id={$issue->id} error=" . json_encode($e->getMessage()));
        }
    }

    private function renderPrompt(Issue $issue): string
    {
        $loader = new ArrayLoader(['prompt' => $this->promptTemplate]);
        $twig = new Environment($loader, ['autoescape' => false]);

        return $twig->render('prompt', [
            'issue' => [
                'id' => $issue->id,
                'identifier' => $issue->identifier,
                'title' => $issue->title,
                'description' => $issue->description,
                'priority' => $issue->priority,
                'state' => $issue->state,
                'branchName' => $issue->branchName,
                'url' => $issue->url,
                'labels' => $issue->labels,
            ],
        ]);
    }

    private function saveState(): void
    {
        if ($this->stateFile === null) {
            return;
        }
        file_put_contents($this->stateFile, json_encode($this->state->toArray()));
    }

    private function shutdown(): void
    {
        $this->log('level=info msg=shutting_down');
        foreach ($this->state->running as $issueId => $entry) {
            proc_terminate($entry->process);
            fclose($entry->pipe);
            proc_close($entry->process);
        }
        $this->state->running = [];
    }

    private function log(string $message): void
    {
        echo date('c') . ' ' . $message . "\n";
    }

    private function nowMs(): int
    {
        return (int)(microtime(true) * 1000);
    }
}
