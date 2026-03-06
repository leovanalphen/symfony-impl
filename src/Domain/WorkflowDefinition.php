<?php
declare(strict_types=1);

namespace Symphony\Domain;

class WorkflowDefinition
{
    public function __construct(
        public readonly array $config,
        public readonly string $promptTemplate,
    ) {}
}
