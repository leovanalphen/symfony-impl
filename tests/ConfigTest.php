<?php
declare(strict_types=1);

namespace Symphony\Tests;

use PHPUnit\Framework\TestCase;
use Symphony\Config\Config;

class ConfigTest extends TestCase
{
    public function testDefaults(): void
    {
        $config = new Config([]);

        $this->assertEquals('linear', $config->getTrackerKind());
        $this->assertEquals('https://api.linear.app/graphql', $config->getTrackerEndpoint());
        $this->assertEquals(30000, $config->getPollingIntervalMs());
        $this->assertEquals(10, $config->getMaxConcurrentAgents());
        $this->assertEquals(20, $config->getMaxTurns());
        $this->assertEquals(300000, $config->getMaxRetryBackoffMs());
        $this->assertEquals('codex app-server', $config->getCodexCommand());
        $this->assertEquals(3600000, $config->getCodexTurnTimeoutMs());
        $this->assertEquals(5000, $config->getCodexReadTimeoutMs());
        $this->assertEquals(300000, $config->getCodexStallTimeoutMs());
        $this->assertNull($config->getServerPort());
    }

    public function testEnvVarResolution(): void
    {
        putenv('TEST_LINEAR_KEY=test-api-key-12345');

        $config = new Config([
            'tracker' => ['api_key' => '$TEST_LINEAR_KEY'],
        ]);

        $this->assertEquals('test-api-key-12345', $config->getTrackerApiKey());

        putenv('TEST_LINEAR_KEY');
    }

    public function testTildeExpansion(): void
    {
        $home = getenv('HOME');
        if ($home === false) {
            $this->markTestSkipped('HOME env var not set');
        }

        $config = new Config([
            'workspace' => ['root' => '~/symphony_workspaces'],
        ]);

        $root = $config->getWorkspaceRoot();
        $this->assertStringStartsWith($home, $root);
        $this->assertStringNotContainsString('~', $root);
    }

    public function testActiveStates(): void
    {
        $config = new Config([]);
        $states = $config->getActiveStates();
        $this->assertContains('Todo', $states);
        $this->assertContains('In Progress', $states);
    }

    public function testPerStateConcurrency(): void
    {
        $config = new Config([
            'agent' => [
                'max_concurrent_agents_by_state' => [
                    'Todo' => 3,
                    'In Progress' => 5,
                ],
            ],
        ]);

        $byState = $config->getMaxConcurrentAgentsByState();
        $this->assertEquals(3, $byState['todo']);
        $this->assertEquals(5, $byState['in progress']);
        $this->assertArrayNotHasKey('Todo', $byState);
    }
}
