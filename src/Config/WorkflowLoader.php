<?php
declare(strict_types=1);

namespace Symphony\Config;

use Symphony\Domain\WorkflowDefinition;
use Symfony\Component\Yaml\Yaml;
use Symfony\Component\Yaml\Exception\ParseException;

class WorkflowLoader
{
    public function load(string $path): WorkflowDefinition
    {
        if (!file_exists($path)) {
            throw new \RuntimeException("missing_workflow_file: {$path}");
        }

        $content = file_get_contents($path);
        $lines = explode("\n", $content);
        
        $firstDash = -1;
        $secondDash = -1;

        for ($i = 0; $i < count($lines); $i++) {
            if (rtrim($lines[$i]) === '---') {
                if ($firstDash === -1) {
                    $firstDash = $i;
                } else {
                    $secondDash = $i;
                    break;
                }
            }
        }

        if ($firstDash === -1 || $secondDash === -1) {
            return new WorkflowDefinition([], trim($content));
        }

        $yamlLines = array_slice($lines, $firstDash + 1, $secondDash - $firstDash - 1);
        $yamlContent = implode("\n", $yamlLines);
        $templateLines = array_slice($lines, $secondDash + 1);
        $promptTemplate = trim(implode("\n", $templateLines));

        if (trim($yamlContent) === '') {
            return new WorkflowDefinition([], $promptTemplate);
        }

        try {
            $parsed = Yaml::parse($yamlContent);
        } catch (ParseException $e) {
            throw new \RuntimeException("workflow_parse_error: " . $e->getMessage(), 0, $e);
        }

        if (!is_array($parsed) || (count($parsed) > 0 && array_is_list($parsed))) {
            throw new \RuntimeException("workflow_front_matter_not_a_map: got " . (is_array($parsed) ? 'list' : gettype($parsed)));
        }

        return new WorkflowDefinition($parsed, $promptTemplate);
    }
}
