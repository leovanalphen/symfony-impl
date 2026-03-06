<?php
declare(strict_types=1);

namespace Symphony\Tests;

use PHPUnit\Framework\TestCase;
use Symphony\Domain\BlockerRef;
use Symphony\Domain\Issue;
use Symphony\Orchestrator\Orchestrator;

class OrchestratorTest extends TestCase
{
    private function makeIssue(array $overrides = []): Issue
    {
        return new Issue(
            id: $overrides['id'] ?? 'issue-1',
            identifier: $overrides['identifier'] ?? 'ABC-1',
            title: $overrides['title'] ?? 'Test Issue',
            description: $overrides['description'] ?? null,
            priority: $overrides['priority'] ?? null,
            state: $overrides['state'] ?? 'Todo',
            branchName: $overrides['branchName'] ?? null,
            url: $overrides['url'] ?? null,
            labels: $overrides['labels'] ?? [],
            blockedBy: $overrides['blockedBy'] ?? [],
            createdAt: $overrides['createdAt'] ?? null,
            updatedAt: $overrides['updatedAt'] ?? null,
        );
    }

    public function testDispatchEligibility(): void
    {
        $issue = $this->makeIssue(['state' => 'Todo']);
        $activeStates = ['Todo', 'In Progress'];
        $terminalStates = ['Done', 'Cancelled'];

        $eligible = Orchestrator::isEligible($issue, $activeStates, $terminalStates, [], [], []);
        $this->assertTrue($eligible);
    }

    public function testNotEligibleWhenInTerminalState(): void
    {
        $issue = $this->makeIssue(['state' => 'Done']);
        $activeStates = ['Todo', 'In Progress'];
        $terminalStates = ['Done', 'Cancelled'];

        $eligible = Orchestrator::isEligible($issue, $activeStates, $terminalStates, [], [], []);
        $this->assertFalse($eligible);
    }

    public function testNotEligibleWhenAlreadyRunning(): void
    {
        $issue = $this->makeIssue(['id' => 'issue-1', 'state' => 'Todo']);
        $activeStates = ['Todo', 'In Progress'];
        $terminalStates = ['Done'];

        $running = ['issue-1' => true];
        $eligible = Orchestrator::isEligible($issue, $activeStates, $terminalStates, $running, [], []);
        $this->assertFalse($eligible);
    }

    public function testSortOrder(): void
    {
        $now = new \DateTimeImmutable('2024-01-01T00:00:00Z');
        $later = new \DateTimeImmutable('2024-01-02T00:00:00Z');

        $issues = [
            $this->makeIssue(['id' => '3', 'identifier' => 'ABC-3', 'priority' => null, 'createdAt' => $now]),
            $this->makeIssue(['id' => '1', 'identifier' => 'ABC-1', 'priority' => 1, 'createdAt' => $now]),
            $this->makeIssue(['id' => '2', 'identifier' => 'ABC-2', 'priority' => 2, 'createdAt' => $later]),
        ];

        $sorted = Orchestrator::sortIssues($issues);

        $this->assertEquals('ABC-1', $sorted[0]->identifier);
        $this->assertEquals('ABC-2', $sorted[1]->identifier);
        $this->assertEquals('ABC-3', $sorted[2]->identifier);
    }

    public function testSortByCreatedAtWhenSamePriority(): void
    {
        $earlier = new \DateTimeImmutable('2024-01-01T00:00:00Z');
        $later = new \DateTimeImmutable('2024-01-02T00:00:00Z');

        $issues = [
            $this->makeIssue(['id' => '2', 'identifier' => 'ABC-2', 'priority' => 1, 'createdAt' => $later]),
            $this->makeIssue(['id' => '1', 'identifier' => 'ABC-1', 'priority' => 1, 'createdAt' => $earlier]),
        ];

        $sorted = Orchestrator::sortIssues($issues);
        $this->assertEquals('ABC-1', $sorted[0]->identifier);
        $this->assertEquals('ABC-2', $sorted[1]->identifier);
    }

    public function testBlockerRules(): void
    {
        $terminalStates = ['Done', 'Cancelled'];
        $activeStates = ['Todo', 'In Progress'];

        $blocker = new BlockerRef('blocker-1', 'ABC-0', 'In Progress');
        $issue = $this->makeIssue([
            'state' => 'Todo',
            'blockedBy' => [$blocker],
        ]);

        $eligible = Orchestrator::isEligible($issue, $activeStates, $terminalStates, [], [], []);
        $this->assertFalse($eligible);

        $resolvedBlocker = new BlockerRef('blocker-1', 'ABC-0', 'Done');
        $issue2 = $this->makeIssue([
            'state' => 'Todo',
            'blockedBy' => [$resolvedBlocker],
        ]);

        $eligible2 = Orchestrator::isEligible($issue2, $activeStates, $terminalStates, [], [], []);
        $this->assertTrue($eligible2);
    }

    public function testRetryBackoff(): void
    {
        $this->assertEquals(1000, Orchestrator::calculateRetryDelay(true, 1, 300000));
        $this->assertEquals(1000, Orchestrator::calculateRetryDelay(true, 5, 300000));

        $this->assertEquals(10000, Orchestrator::calculateRetryDelay(false, 1, 300000));
        $this->assertEquals(20000, Orchestrator::calculateRetryDelay(false, 2, 300000));
        $this->assertEquals(40000, Orchestrator::calculateRetryDelay(false, 3, 300000));

        $this->assertEquals(300000, Orchestrator::calculateRetryDelay(false, 10, 300000));
    }
}
