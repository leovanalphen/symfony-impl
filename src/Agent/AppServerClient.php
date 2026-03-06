<?php
declare(strict_types=1);

namespace Symphony\Agent;

use Symphony\Config\Config;

class AppServerClient
{
    private mixed $process = null;
    private array $pipes = [];
    private int $nextId = 1;
    private string $readBuffer = '';

    public function __construct(
        private readonly Config $config,
        private readonly string $workspacePath,
    ) {}

    public function launch(): void
    {
        $command = $this->config->getCodexCommand();
        $descriptors = [
            0 => ['pipe', 'r'],
            1 => ['pipe', 'w'],
            2 => STDERR,
        ];

        $this->process = proc_open(
            ['bash', '-lc', $command],
            $descriptors,
            $this->pipes,
            $this->workspacePath,
            null
        );

        if (!is_resource($this->process)) {
            throw new \RuntimeException("Failed to launch codex app-server");
        }

        stream_set_blocking($this->pipes[1], false);
    }

    public function initialize(): array
    {
        $id = $this->nextId++;
        $this->send([
            'id' => $id,
            'method' => 'initialize',
            'params' => [
                'clientInfo' => ['name' => 'symphony', 'version' => '1.0'],
                'capabilities' => (object)[],
            ],
        ]);

        $this->send([
            'method' => 'initialized',
            'params' => (object)[],
        ]);

        return $this->waitForResponse($id);
    }

    public function startThread(): string
    {
        $id = $this->nextId++;
        $this->send([
            'id' => $id,
            'method' => 'thread/start',
            'params' => [
                'approvalPolicy' => $this->config->getCodexApprovalPolicy(),
                'sandbox' => $this->config->getCodexThreadSandbox(),
                'cwd' => $this->workspacePath,
            ],
        ]);

        $response = $this->waitForResponse($id);
        return $response['result']['thread']['id'] ?? throw new \RuntimeException("No thread ID in response");
    }

    public function startTurn(string $threadId, string $prompt, string $title): string
    {
        $id = $this->nextId++;
        $this->send([
            'id' => $id,
            'method' => 'turn/start',
            'params' => [
                'threadId' => $threadId,
                'input' => [['type' => 'text', 'text' => $prompt]],
                'cwd' => $this->workspacePath,
                'title' => $title,
                'approvalPolicy' => $this->config->getCodexApprovalPolicy(),
                'sandboxPolicy' => ['type' => $this->config->getCodexTurnSandboxPolicy()],
            ],
        ]);

        $response = $this->waitForResponse($id);
        return $response['result']['turn']['id'] ?? throw new \RuntimeException("No turn ID in response");
    }

    public function streamMessages(callable $onMessage): string
    {
        $timeoutMs = $this->config->getCodexTurnTimeoutMs();
        $stallTimeoutMs = $this->config->getCodexStallTimeoutMs();
        $startTime = $this->nowMs();
        $lastEventTime = $startTime;

        $totalUsage = ['input_tokens' => 0, 'output_tokens' => 0, 'total_tokens' => 0];

        while (true) {
            $now = $this->nowMs();

            if ($now - $startTime > $timeoutMs) {
                throw new \RuntimeException("Turn timeout exceeded");
            }

            if ($now - $lastEventTime > $stallTimeoutMs) {
                throw new \RuntimeException("Stall timeout exceeded");
            }

            $msg = $this->readMessage(1000);
            if ($msg === null) {
                continue;
            }

            $lastEventTime = $this->nowMs();
            $onMessage($msg);

            $method = $msg['method'] ?? null;

            if ($method === 'approval/request') {
                $approvalId = $msg['params']['id'] ?? ($msg['id'] ?? null);
                if ($approvalId !== null) {
                    $this->send([
                        'id' => $approvalId,
                        'result' => ['approved' => true],
                    ]);
                }
                continue;
            }

            if ($method === 'tool/call') {
                $toolId = $msg['id'] ?? null;
                $toolName = $msg['params']['name'] ?? '';

                if ($toolName === 'linear_graphql') {
                    $result = $this->handleLinearGraphql($msg['params'] ?? []);
                    if ($toolId !== null) {
                        $this->send(['id' => $toolId, 'result' => $result]);
                    }
                } elseif ($toolId !== null) {
                    $this->send([
                        'id' => $toolId,
                        'result' => ['success' => false, 'error' => 'unsupported_tool_call'],
                    ]);
                }
                continue;
            }

            if ($method === 'input/request') {
                throw new \RuntimeException("Agent requested user input - cannot continue");
            }

            if (isset($msg['params']['usage'])) {
                $u = $msg['params']['usage'];
                $totalUsage['input_tokens'] += (int)($u['inputTokens'] ?? $u['input_tokens'] ?? 0);
                $totalUsage['output_tokens'] += (int)($u['outputTokens'] ?? $u['output_tokens'] ?? 0);
                $totalUsage['total_tokens'] += (int)($u['totalTokens'] ?? $u['total_tokens'] ?? 0);
            }

            if ($method === 'turn/completed' || $method === 'turn/done') {
                return json_encode($totalUsage);
            }
            if ($method === 'turn/failed' || $method === 'turn/error') {
                $error = $msg['params']['error'] ?? 'Unknown error';
                throw new \RuntimeException("Turn failed: {$error}");
            }
            if ($method === 'turn/cancelled') {
                throw new \RuntimeException("Turn was cancelled");
            }
        }
    }

    private function handleLinearGraphql(array $params): array
    {
        $query = $params['query'] ?? '';
        $variables = $params['variables'] ?? [];
        $apiKey = $this->config->getTrackerApiKey();
        $endpoint = $this->config->getTrackerEndpoint();

        $ch = curl_init($endpoint);
        curl_setopt_array($ch, [
            CURLOPT_POST => true,
            CURLOPT_RETURNTRANSFER => true,
            CURLOPT_HTTPHEADER => [
                'Authorization: ' . $apiKey,
                'Content-Type: application/json',
            ],
            CURLOPT_POSTFIELDS => json_encode(['query' => $query, 'variables' => $variables]),
            CURLOPT_TIMEOUT => 30,
        ]);

        $result = curl_exec($ch);
        $error = curl_error($ch);
        curl_close($ch);

        if ($error) {
            return ['success' => false, 'error' => $error];
        }

        return ['success' => true, 'data' => json_decode($result, true)];
    }

    private function send(array $message): void
    {
        $line = json_encode($message) . "\n";
        fwrite($this->pipes[0], $line);
    }

    private function waitForResponse(int $id): array
    {
        $readTimeoutMs = $this->config->getCodexReadTimeoutMs();
        $start = $this->nowMs();

        while (true) {
            if ($this->nowMs() - $start > $readTimeoutMs * 10) {
                throw new \RuntimeException("Timeout waiting for response to request {$id}");
            }

            $msg = $this->readMessage(100);
            if ($msg !== null && isset($msg['id']) && $msg['id'] === $id) {
                return $msg;
            }
        }
    }

    private function readMessage(int $timeoutMs): ?array
    {
        $newlinePos = strpos($this->readBuffer, "\n");
        if ($newlinePos !== false) {
            $line = substr($this->readBuffer, 0, $newlinePos);
            $this->readBuffer = substr($this->readBuffer, $newlinePos + 1);
            $decoded = json_decode($line, true);
            return is_array($decoded) ? $decoded : null;
        }

        $read = [$this->pipes[1]];
        $write = $except = null;
        $sec = (int)floor($timeoutMs / 1000);
        $usec = ($timeoutMs % 1000) * 1000;

        $ready = stream_select($read, $write, $except, $sec, $usec);
        if ($ready === false || $ready === 0) {
            return null;
        }

        $chunk = fread($this->pipes[1], 4096);
        if ($chunk === false || $chunk === '') {
            return null;
        }

        $this->readBuffer .= $chunk;
        $newlinePos = strpos($this->readBuffer, "\n");
        if ($newlinePos !== false) {
            $line = substr($this->readBuffer, 0, $newlinePos);
            $this->readBuffer = substr($this->readBuffer, $newlinePos + 1);
            $decoded = json_decode($line, true);
            return is_array($decoded) ? $decoded : null;
        }

        return null;
    }

    public function close(): void
    {
        if (isset($this->pipes[0]) && is_resource($this->pipes[0])) {
            fclose($this->pipes[0]);
        }
        if (isset($this->pipes[1]) && is_resource($this->pipes[1])) {
            fclose($this->pipes[1]);
        }
        if ($this->process !== null && is_resource($this->process)) {
            proc_terminate($this->process);
            proc_close($this->process);
        }
    }

    private function nowMs(): int
    {
        return (int)(microtime(true) * 1000);
    }
}
