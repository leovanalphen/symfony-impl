<?php
declare(strict_types=1);

namespace Symphony\Tests;

use PHPUnit\Framework\TestCase;
use Symphony\Config\Config;
use Symphony\Tracker\LinearClient;
use Symfony\Component\HttpClient\MockHttpClient;
use Symfony\Component\HttpClient\Response\MockResponse;

class LinearClientTest extends TestCase
{
    private function makeConfig(): Config
    {
        return new Config([
            'tracker' => [
                'kind' => 'linear',
                'api_key' => 'test-key',
                'project_slug' => 'test-project',
                'active_states' => ['Todo', 'In Progress'],
                'terminal_states' => ['Done', 'Cancelled'],
            ],
        ]);
    }

    public function testIssueNormalization(): void
    {
        $responseData = [
            'data' => [
                'issues' => [
                    'pageInfo' => ['hasNextPage' => false, 'endCursor' => null],
                    'nodes' => [
                        [
                            'id' => 'issue-abc',
                            'identifier' => 'ABC-123',
                            'title' => 'Fix the bug',
                            'description' => 'A bug description',
                            'priority' => 2,
                            'branchName' => 'fix-the-bug',
                            'url' => 'https://linear.app/issue/ABC-123',
                            'createdAt' => '2024-01-01T00:00:00Z',
                            'updatedAt' => '2024-01-02T00:00:00Z',
                            'state' => ['name' => 'Todo'],
                            'labels' => ['nodes' => [
                                ['name' => 'Bug'],
                                ['name' => 'Priority'],
                            ]],
                            'relations' => ['nodes' => []],
                        ],
                    ],
                ],
            ],
        ];

        $mockResponse = new MockResponse(json_encode($responseData), [
            'http_code' => 200,
            'response_headers' => ['Content-Type: application/json'],
        ]);
        $mockClient = new MockHttpClient($mockResponse);

        $config = $this->makeConfig();
        $client = new LinearClient($config, $mockClient);

        $issues = $client->fetchCandidateIssues();

        $this->assertCount(1, $issues);
        $issue = $issues[0];

        $this->assertEquals('issue-abc', $issue->id);
        $this->assertEquals('ABC-123', $issue->identifier);
        $this->assertEquals('Fix the bug', $issue->title);
        $this->assertEquals('A bug description', $issue->description);
        $this->assertEquals(2, $issue->priority);
        $this->assertEquals('Todo', $issue->state);
        $this->assertEquals('fix-the-bug', $issue->branchName);

        $this->assertContains('bug', $issue->labels);
        $this->assertContains('priority', $issue->labels);

        $this->assertInstanceOf(\DateTimeImmutable::class, $issue->createdAt);
        $this->assertInstanceOf(\DateTimeImmutable::class, $issue->updatedAt);
    }

    public function testStateFiltering(): void
    {
        $responseData = [
            'data' => [
                'issues' => [
                    'pageInfo' => ['hasNextPage' => false, 'endCursor' => null],
                    'nodes' => [
                        [
                            'id' => 'issue-1',
                            'identifier' => 'ABC-1',
                            'title' => 'Todo issue',
                            'description' => null,
                            'priority' => null,
                            'branchName' => null,
                            'url' => null,
                            'createdAt' => '2024-01-01T00:00:00Z',
                            'updatedAt' => '2024-01-01T00:00:00Z',
                            'state' => ['name' => 'Todo'],
                            'labels' => ['nodes' => []],
                            'relations' => ['nodes' => []],
                        ],
                        [
                            'id' => 'issue-2',
                            'identifier' => 'ABC-2',
                            'title' => 'In Progress issue',
                            'description' => null,
                            'priority' => null,
                            'branchName' => null,
                            'url' => null,
                            'createdAt' => '2024-01-01T00:00:00Z',
                            'updatedAt' => '2024-01-01T00:00:00Z',
                            'state' => ['name' => 'In Progress'],
                            'labels' => ['nodes' => []],
                            'relations' => ['nodes' => []],
                        ],
                    ],
                ],
            ],
        ];

        $mockResponse = new MockResponse(json_encode($responseData), [
            'http_code' => 200,
            'response_headers' => ['Content-Type: application/json'],
        ]);
        $mockClient = new MockHttpClient($mockResponse);

        $config = $this->makeConfig();
        $client = new LinearClient($config, $mockClient);

        $issues = $client->fetchCandidateIssues();

        $this->assertCount(2, $issues);
        $states = array_map(fn($i) => $i->state, $issues);
        $this->assertContains('Todo', $states);
        $this->assertContains('In Progress', $states);
    }
}
