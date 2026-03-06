<?php
declare(strict_types=1);

namespace Symphony\Http;

class ApiServer
{
    private mixed $process = null;
    private string $handlerScript;

    public function __construct(
        private readonly int $port,
        private readonly string $stateFile,
    ) {
        $this->handlerScript = sys_get_temp_dir() . '/symphony_api_handler_' . getmypid() . '.php';
    }

    public function start(): void
    {
        $this->writeHandlerScript();

        $descriptors = [
            0 => ['pipe', 'r'],
            1 => STDOUT,
            2 => STDERR,
        ];

        $this->process = proc_open(
            ['php', '-S', "0.0.0.0:{$this->port}", $this->handlerScript],
            $descriptors,
            $pipes,
            sys_get_temp_dir(),
            null
        );

        if (!is_resource($this->process)) {
            throw new \RuntimeException("Failed to start HTTP server on port {$this->port}");
        }
    }

    public function stop(): void
    {
        if ($this->process !== null && is_resource($this->process)) {
            proc_terminate($this->process);
            proc_close($this->process);
        }
        if (file_exists($this->handlerScript)) {
            unlink($this->handlerScript);
        }
    }

    private function writeHandlerScript(): void
    {
        $stateFile = addslashes($this->stateFile);
        $script = <<<PHP
<?php
\$stateFile = '{$stateFile}';
\$uri = \$_SERVER['REQUEST_URI'] ?? '/';
\$method = \$_SERVER['REQUEST_METHOD'] ?? 'GET';

\$state = [];
if (file_exists(\$stateFile)) {
    \$state = json_decode(file_get_contents(\$stateFile), true) ?? [];
}

if (\$method === 'GET' && \$uri === '/') {
    header('Content-Type: text/html');
    \$running = count(\$state['running'] ?? []);
    \$completed = count(\$state['completed'] ?? []);
    echo "<html><body><h1>Symphony Dashboard</h1>";
    echo "<p>Running: {\$running}</p>";
    echo "<p>Completed: {\$completed}</p>";
    echo "<pre>" . htmlspecialchars(json_encode(\$state, JSON_PRETTY_PRINT)) . "</pre>";
    echo "</body></html>";
} elseif (\$method === 'GET' && \$uri === '/api/v1/state') {
    header('Content-Type: application/json');
    echo json_encode(\$state);
} elseif (\$method === 'POST' && \$uri === '/api/v1/refresh') {
    header('Content-Type: application/json');
    echo json_encode(['status' => 'ok', 'message' => 'Refresh triggered']);
} elseif (\$method === 'GET' && preg_match('#^/api/v1/([^/]+)$#', \$uri, \$m)) {
    header('Content-Type: application/json');
    \$identifier = \$m[1];
    \$found = null;
    foreach (\$state['running'] ?? [] as \$id => \$entry) {
        if ((\$entry['identifier'] ?? '') === \$identifier) {
            \$found = \$entry;
            break;
        }
    }
    echo json_encode(['identifier' => \$identifier, 'entry' => \$found, 'state' => \$state]);
} else {
    http_response_code(404);
    echo json_encode(['error' => 'Not found']);
}
PHP;
        file_put_contents($this->handlerScript, $script);
    }
}
