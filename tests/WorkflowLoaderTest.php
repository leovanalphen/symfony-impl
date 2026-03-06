<?php
declare(strict_types=1);

namespace Symphony\Tests;

use PHPUnit\Framework\TestCase;
use Symphony\Config\WorkflowLoader;

class WorkflowLoaderTest extends TestCase
{
    private string $tempDir;

    protected function setUp(): void
    {
        $this->tempDir = sys_get_temp_dir() . '/symphony_test_' . uniqid();
        mkdir($this->tempDir, 0755, true);
    }

    protected function tearDown(): void
    {
        $this->removeDir($this->tempDir);
    }

    public function testLoadWithFrontMatter(): void
    {
        $content = <<<MARKDOWN
---
tracker:
  kind: linear
  project_slug: my-project
agent:
  max_turns: 10
---

You are working on {{ issue.identifier }}.
MARKDOWN;
        $path = $this->tempDir . '/WORKFLOW.md';
        file_put_contents($path, $content);

        $loader = new WorkflowLoader();
        $def = $loader->load($path);

        $this->assertEquals('linear', $def->config['tracker']['kind']);
        $this->assertEquals('my-project', $def->config['tracker']['project_slug']);
        $this->assertEquals(10, $def->config['agent']['max_turns']);
        $this->assertStringContainsString('{{ issue.identifier }}', $def->promptTemplate);
    }

    public function testLoadWithoutFrontMatter(): void
    {
        $content = "Just a plain template without front matter.";
        $path = $this->tempDir . '/WORKFLOW.md';
        file_put_contents($path, $content);

        $loader = new WorkflowLoader();
        $def = $loader->load($path);

        $this->assertEquals([], $def->config);
        $this->assertEquals('Just a plain template without front matter.', $def->promptTemplate);
    }

    public function testMissingFile(): void
    {
        $this->expectException(\RuntimeException::class);
        $this->expectExceptionMessageMatches('/missing_workflow_file/');

        $loader = new WorkflowLoader();
        $loader->load('/nonexistent/path/WORKFLOW.md');
    }

    public function testInvalidYaml(): void
    {
        $content = <<<MARKDOWN
---
tracker:
  kind: [invalid yaml: {broken
---

Template here.
MARKDOWN;
        $path = $this->tempDir . '/WORKFLOW.md';
        file_put_contents($path, $content);

        $this->expectException(\RuntimeException::class);
        $this->expectExceptionMessageMatches('/workflow_parse_error/');

        $loader = new WorkflowLoader();
        $loader->load($path);
    }

    public function testNonMapFrontMatter(): void
    {
        $content = <<<MARKDOWN
---
- item1
- item2
---

Template here.
MARKDOWN;
        $path = $this->tempDir . '/WORKFLOW.md';
        file_put_contents($path, $content);

        $this->expectException(\RuntimeException::class);
        $this->expectExceptionMessageMatches('/workflow_front_matter_not_a_map/');

        $loader = new WorkflowLoader();
        $loader->load($path);
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
