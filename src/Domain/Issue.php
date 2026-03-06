<?php
declare(strict_types=1);

namespace Symphony\Domain;

class Issue
{
    public function __construct(
        public readonly string $id,
        public readonly string $identifier,
        public readonly string $title,
        public readonly ?string $description,
        public readonly ?int $priority,
        public readonly string $state,
        public readonly ?string $branchName,
        public readonly ?string $url,
        public readonly array $labels,
        public readonly array $blockedBy,
        public readonly ?\DateTimeImmutable $createdAt,
        public readonly ?\DateTimeImmutable $updatedAt,
    ) {}
}
