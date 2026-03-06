<?php
declare(strict_types=1);

namespace Symphony\Tests;

use PHPUnit\Framework\TestCase;
use Symphony\Config\Config;
use Symphony\Workspace\WorkspaceManager;

class WorkspaceManagerTest extends TestCase
{
    private string $tempRoot;
    private WorkspaceManager $manager;

    protected function setUp(): void
    {
        $this->tempRoot = sys_get_temp_dir() . '/symphony_ws_test_' . uniqid();
        mkdir($this->tempRoot, 0755, true);

        $config = new Config([
            'workspace' => ['root' => $this->tempRoot],
        ]);
        $this->manager = new WorkspaceManager($config);
    }

    protected function tearDown(): void
    {
        $this->removeDir($this->tempRoot);
    }

    public function testWorkspaceKeySanitization(): void
    {
        $this->assertEquals('ABC-123', $this->manager->sanitizeKey('ABC-123'));
        $this->assertEquals('ABC__123', $this->manager->sanitizeKey('ABC/123'));
        $this->assertEquals('abc__def', $this->manager->sanitizeKey('abc::def'));
    }

    public function testPathSafetyInvariant(): void
    {
        $identifier = '../../etc/passwd';
        $path = $this->manager->getWorkspacePath($identifier);

        $this->assertStringStartsWith($this->tempRoot, $path);
        $this->assertStringNotContainsString('..', basename($path));
    }

    public function testCreateWorkspace(): void
    {
        $result = $this->manager->createForIssue('ABC-123');

        $this->assertArrayHasKey('path', $result);
        $this->assertArrayHasKey('created_now', $result);
        $this->assertTrue($result['created_now']);
        $this->assertTrue(is_dir($result['path']));
        $this->assertStringStartsWith($this->tempRoot, $result['path']);
    }

    public function testReuseWorkspace(): void
    {
        $result1 = $this->manager->createForIssue('ABC-456');
        $this->assertTrue($result1['created_now']);

        $result2 = $this->manager->createForIssue('ABC-456');
        $this->assertFalse($result2['created_now']);
        $this->assertEquals($result1['path'], $result2['path']);
    }

    private function removeDir(string $dir): void
    {
        if (!is_dir($dir)) return;
        foreach (scandir($dir) as $item) {
            if ($item === '.' || $item === '..') continue;
            $path = $dir . '/' . $item;
            is_dir($path) ? $this->removeDir($path) : unlink($path);
        }
        rmdir($dir);
    }
}
