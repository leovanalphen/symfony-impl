<?php
declare(strict_types=1);

namespace Symphony\Command;

use Symphony\Config\Config;
use Symphony\Config\WorkflowLoader;
use Symphony\Http\ApiServer;
use Symphony\Orchestrator\Orchestrator;
use Symphony\Tracker\LinearClient;
use Symphony\Workspace\WorkspaceManager;
use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputArgument;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\Console\Output\OutputInterface;

#[AsCommand(name: 'symphony')]
class SymphonyCommand extends Command
{
    protected function configure(): void
    {
        $this
            ->setName('symphony')
            ->setDescription('Symphony automation service')
            ->addArgument('workflow', InputArgument::OPTIONAL, 'Path to WORKFLOW.md', './WORKFLOW.md')
            ->addOption('port', null, InputOption::VALUE_OPTIONAL, 'HTTP server port')
            ->addOption('logs-root', null, InputOption::VALUE_OPTIONAL, 'Logs directory');
    }

    protected function execute(InputInterface $input, OutputInterface $output): int
    {
        $workflowPath = $input->getArgument('workflow');
        $port = $input->getOption('port');
        $logsRoot = $input->getOption('logs-root');

        if (!file_exists($workflowPath)) {
            $workflowPath = getcwd() . '/WORKFLOW.md';
        }

        $loader = new WorkflowLoader();

        try {
            $definition = $loader->load($workflowPath);
        } catch (\RuntimeException $e) {
            $output->writeln('<error>Failed to load workflow: ' . $e->getMessage() . '</error>');
            return Command::FAILURE;
        }

        $config = new Config($definition->config);
        $promptTemplate = $definition->promptTemplate;

        $projectSlug = $config->getTrackerProjectSlug();
        if (empty($projectSlug)) {
            $output->writeln('<error>Missing required config: tracker.project_slug</error>');
            return Command::FAILURE;
        }

        $tracker = new LinearClient($config);
        $workspaceManager = new WorkspaceManager($config);

        try {
            $terminalIssues = $tracker->fetchIssuesByStates($config->getTerminalStates());
            foreach ($terminalIssues as $issue) {
                try {
                    $workspaceManager->cleanupWorkspace($issue->identifier);
                } catch (\Throwable) {}
            }
        } catch (\Throwable $e) {
            $output->writeln('<comment>Startup cleanup warning: ' . $e->getMessage() . '</comment>');
        }

        $stateFile = sys_get_temp_dir() . '/symphony_state_' . getmypid() . '.json';

        $apiServer = null;
        $serverPort = $port !== null ? (int)$port : $config->getServerPort();
        if ($serverPort !== null && $serverPort > 0) {
            $apiServer = new ApiServer($serverPort, $stateFile);
            try {
                $apiServer->start();
                $output->writeln("HTTP server started on port {$serverPort}");
            } catch (\Throwable $e) {
                $output->writeln('<comment>Failed to start HTTP server: ' . $e->getMessage() . '</comment>');
            }
        }

        $orchestrator = new Orchestrator($config, $tracker, $workspaceManager, $promptTemplate, $logsRoot);
        $orchestrator->setStateFile($stateFile);

        try {
            $orchestrator->run();
        } finally {
            if ($apiServer !== null) {
                $apiServer->stop();
            }
            if (file_exists($stateFile)) {
                unlink($stateFile);
            }
        }

        return Command::SUCCESS;
    }
}
