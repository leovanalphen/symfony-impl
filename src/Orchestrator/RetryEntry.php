<?php
declare(strict_types=1);

namespace Symphony\Orchestrator;

class RetryEntry
{
    public function __construct(
        public int $attempt,
        public int $retryAfter,
        public bool $wasNormal,
    ) {}
}
