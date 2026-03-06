<?php
declare(strict_types=1);

namespace Symphony\Orchestrator;

class RunningEntry
{
    public string $buffer = '';
    public ?string $sessionId = null;

    public function __construct(
        public string $issueId,
        public string $identifier,
        public mixed $pipe,
        public mixed $process,
        public int $pid,
        public int $startedAt,
        public int $lastEventAt,
        public int $attempt,
    ) {}
}
