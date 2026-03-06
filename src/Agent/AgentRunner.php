<?php
declare(strict_types=1);

namespace Symphony\Agent;

use Symphony\Config\Config;
use Symphony\Domain\Issue;

class AgentRunner
{
    public function __construct(
        private readonly Config $config,
        private readonly string $workspacePath,
        private readonly Issue $issue,
        private readonly string $prompt,
        private readonly int $attempt,
    ) {}

    private function emit(array $event): void
    {
        $event['timestamp'] = date('c');
        echo json_encode($event) . "\n";
        flush();
    }

    public function run(): void
    {
        $issueId = $this->issue->id;

        $client = new AppServerClient($this->config, $this->workspacePath);

        try {
            $client->launch();
            $client->initialize();
            $threadId = $client->startThread();
            $title = $this->issue->identifier . ': ' . $this->issue->title;
            $turnId = $client->startTurn($threadId, $this->prompt, $title);
            $sessionId = $threadId . '-' . $turnId;

            $this->emit([
                'event' => 'session_started',
                'issue_id' => $issueId,
                'session_id' => $sessionId,
                'pid' => getmypid(),
            ]);

            $usageJson = $client->streamMessages(function (array $msg) use ($issueId) {
                $method = $msg['method'] ?? '';
                if (!empty($method)) {
                    $this->emit([
                        'event' => 'codex_update',
                        'issue_id' => $issueId,
                        'type' => 'notification',
                        'message' => $method,
                    ]);
                }
            });

            $usage = json_decode($usageJson, true) ?? [];
            $usage['input_tokens'] ??= 0;
            $usage['output_tokens'] ??= 0;
            $usage['total_tokens'] ??= 0;

            $this->emit([
                'event' => 'turn_completed',
                'issue_id' => $issueId,
                'usage' => $usage,
            ]);

            $this->emit([
                'event' => 'worker_exit',
                'issue_id' => $issueId,
                'normal' => true,
                'attempt' => $this->attempt,
            ]);
        } catch (\Throwable $e) {
            $this->emit([
                'event' => 'turn_failed',
                'issue_id' => $issueId,
                'error' => $e->getMessage(),
            ]);

            $this->emit([
                'event' => 'worker_exit',
                'issue_id' => $issueId,
                'normal' => false,
                'attempt' => $this->attempt,
            ]);
        } finally {
            $client->close();
        }
    }
}
