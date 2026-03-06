<?php
declare(strict_types=1);

// Standalone worker script for Symphony
// Invoked as: php WorkerScript.php <json_encoded_args>

$autoloadPaths = [
    __DIR__ . '/../../vendor/autoload.php',
    __DIR__ . '/../vendor/autoload.php',
    dirname(__DIR__, 3) . '/vendor/autoload.php',
];

$loaded = false;
foreach ($autoloadPaths as $autoload) {
    if (file_exists($autoload)) {
        require_once $autoload;
        $loaded = true;
        break;
    }
}

if (!$loaded) {
    fwrite(STDERR, "Cannot find autoloader\n");
    exit(1);
}

use Symphony\Agent\AgentRunner;
use Symphony\Config\Config;
use Symphony\Domain\BlockerRef;
use Symphony\Domain\Issue;

if ($argc < 2) {
    fwrite(STDERR, "Usage: WorkerScript.php <json_args>\n");
    exit(1);
}

$args = json_decode($argv[1], true);
if (!is_array($args)) {
    fwrite(STDERR, "Invalid JSON args\n");
    exit(1);
}

$configRaw = $args['config'] ?? [];
$issueData = $args['issue'] ?? [];
$workspacePath = $args['workspace_path'] ?? '';
$prompt = $args['prompt'] ?? '';
$attempt = (int)($args['attempt'] ?? 1);

$config = new Config($configRaw);

$blockedBy = [];
foreach ($issueData['blockedBy'] ?? [] as $b) {
    $blockedBy[] = new BlockerRef($b['id'] ?? null, $b['identifier'] ?? null, $b['state'] ?? null);
}

$createdAt = null;
if (!empty($issueData['createdAt'])) {
    try { $createdAt = new \DateTimeImmutable($issueData['createdAt']); } catch (\Exception) {}
}
$updatedAt = null;
if (!empty($issueData['updatedAt'])) {
    try { $updatedAt = new \DateTimeImmutable($issueData['updatedAt']); } catch (\Exception) {}
}

$issue = new Issue(
    id: $issueData['id'] ?? '',
    identifier: $issueData['identifier'] ?? '',
    title: $issueData['title'] ?? '',
    description: $issueData['description'] ?? null,
    priority: isset($issueData['priority']) ? (int)$issueData['priority'] : null,
    state: $issueData['state'] ?? '',
    branchName: $issueData['branchName'] ?? null,
    url: $issueData['url'] ?? null,
    labels: $issueData['labels'] ?? [],
    blockedBy: $blockedBy,
    createdAt: $createdAt,
    updatedAt: $updatedAt,
);

$runner = new AgentRunner($config, $workspacePath, $issue, $prompt, $attempt);
$runner->run();
