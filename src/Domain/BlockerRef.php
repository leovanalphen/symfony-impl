<?php
declare(strict_types=1);

namespace Symphony\Domain;

class BlockerRef
{
    public function __construct(
        public readonly ?string $id,
        public readonly ?string $identifier,
        public readonly ?string $state,
    ) {}
}
