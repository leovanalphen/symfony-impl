<?php
declare(strict_types=1);

namespace Symphony\Tracker;

use Symphony\Config\Config;
use Symphony\Domain\BlockerRef;
use Symphony\Domain\Issue;
use Symfony\Contracts\HttpClient\HttpClientInterface;
use Symfony\Component\HttpClient\HttpClient;

class LinearClient
{
    private HttpClientInterface $httpClient;

    public function __construct(
        private readonly Config $config,
        ?HttpClientInterface $httpClient = null
    ) {
        $this->httpClient = $httpClient ?? HttpClient::create([
            'timeout' => 30,
        ]);
    }

    private function query(string $query, array $variables = []): array
    {
        $endpoint = $this->config->getTrackerEndpoint();
        $apiKey = $this->config->getTrackerApiKey();

        $response = $this->httpClient->request('POST', $endpoint, [
            'headers' => [
                'Authorization' => $apiKey,
                'Content-Type' => 'application/json',
            ],
            'json' => [
                'query' => $query,
                'variables' => $variables,
            ],
            'timeout' => 30,
        ]);

        $data = $response->toArray();

        if (isset($data['errors'])) {
            throw new \RuntimeException('GraphQL error: ' . json_encode($data['errors']));
        }

        return $data['data'] ?? [];
    }

    public function fetchCandidateIssues(): array
    {
        $projectSlug = $this->config->getTrackerProjectSlug();
        $activeStates = $this->config->getActiveStates();

        $gql = <<<'GQL'
query FetchCandidates($projectSlug: String!, $states: [String!]!, $after: String) {
    issues(
        filter: {
            project: { slugId: { eq: $projectSlug } }
            state: { name: { in: $states } }
        }
        first: 50
        after: $after
        orderBy: updatedAt
    ) {
        pageInfo {
            hasNextPage
            endCursor
        }
        nodes {
            id
            identifier
            title
            description
            priority
            branchName
            url
            createdAt
            updatedAt
            state {
                name
            }
            labels {
                nodes {
                    name
                }
            }
            relations {
                nodes {
                    type
                    relatedIssue {
                        id
                        identifier
                        state {
                            name
                        }
                    }
                }
            }
        }
    }
}
GQL;

        $issues = [];
        $after = null;

        do {
            $vars = [
                'projectSlug' => $projectSlug,
                'states' => $activeStates,
            ];
            if ($after !== null) {
                $vars['after'] = $after;
            }
            $data = $this->query($gql, $vars);
            $connection = $data['issues'] ?? ['nodes' => [], 'pageInfo' => ['hasNextPage' => false]];

            foreach ($connection['nodes'] as $node) {
                $issues[] = $this->mapNode($node);
            }

            $pageInfo = $connection['pageInfo'];
            $hasNext = $pageInfo['hasNextPage'] ?? false;
            $after = $pageInfo['endCursor'] ?? null;
        } while ($hasNext && $after !== null);

        return $issues;
    }

    public function fetchIssuesByStates(array $stateNames): array
    {
        if (empty($stateNames)) {
            return [];
        }

        $projectSlug = $this->config->getTrackerProjectSlug();

        $gql = <<<'GQL'
query FetchByStates($projectSlug: String!, $states: [String!]!, $after: String) {
    issues(
        filter: {
            project: { slugId: { eq: $projectSlug } }
            state: { name: { in: $states } }
        }
        first: 50
        after: $after
        orderBy: updatedAt
    ) {
        pageInfo {
            hasNextPage
            endCursor
        }
        nodes {
            id
            identifier
            title
            description
            priority
            branchName
            url
            createdAt
            updatedAt
            state {
                name
            }
            labels {
                nodes {
                    name
                }
            }
            relations {
                nodes {
                    type
                    relatedIssue {
                        id
                        identifier
                        state {
                            name
                        }
                    }
                }
            }
        }
    }
}
GQL;

        $issues = [];
        $after = null;

        do {
            $vars = [
                'projectSlug' => $projectSlug,
                'states' => $stateNames,
            ];
            if ($after !== null) {
                $vars['after'] = $after;
            }
            $data = $this->query($gql, $vars);
            $connection = $data['issues'] ?? ['nodes' => [], 'pageInfo' => ['hasNextPage' => false]];

            foreach ($connection['nodes'] as $node) {
                $issues[] = $this->mapNode($node);
            }

            $pageInfo = $connection['pageInfo'];
            $hasNext = $pageInfo['hasNextPage'] ?? false;
            $after = $pageInfo['endCursor'] ?? null;
        } while ($hasNext && $after !== null);

        return $issues;
    }

    public function fetchIssueStatesByIds(array $issueIds): array
    {
        if (empty($issueIds)) {
            return [];
        }

        $gql = <<<'GQL'
query FetchStatesByIds($ids: [ID!]!) {
    issues(filter: { id: { in: $ids } } first: 50) {
        nodes {
            id
            state {
                name
            }
        }
    }
}
GQL;

        $result = [];
        foreach (array_chunk($issueIds, 50) as $batch) {
            $data = $this->query($gql, ['ids' => $batch]);
            $nodes = $data['issues']['nodes'] ?? [];
            foreach ($nodes as $node) {
                $result[$node['id']] = $node['state']['name'] ?? 'Unknown';
            }
        }
        return $result;
    }

    private function mapNode(array $node): Issue
    {
        $labels = [];
        foreach ($node['labels']['nodes'] ?? [] as $label) {
            $labels[] = strtolower((string)($label['name'] ?? ''));
        }

        $blockedBy = [];
        foreach ($node['relations']['nodes'] ?? [] as $rel) {
            if (($rel['type'] ?? '') === 'blocks') {
                $related = $rel['relatedIssue'] ?? [];
                $blockedBy[] = new BlockerRef(
                    $related['id'] ?? null,
                    $related['identifier'] ?? null,
                    $related['state']['name'] ?? null,
                );
            }
        }

        $priority = null;
        if (isset($node['priority']) && is_numeric($node['priority'])) {
            $p = (int)$node['priority'];
            if ($p > 0) {
                $priority = $p;
            }
        }

        $createdAt = null;
        if (!empty($node['createdAt'])) {
            try {
                $createdAt = new \DateTimeImmutable($node['createdAt']);
            } catch (\Exception) {}
        }

        $updatedAt = null;
        if (!empty($node['updatedAt'])) {
            try {
                $updatedAt = new \DateTimeImmutable($node['updatedAt']);
            } catch (\Exception) {}
        }

        return new Issue(
            id: $node['id'] ?? '',
            identifier: $node['identifier'] ?? '',
            title: $node['title'] ?? '',
            description: $node['description'] ?? null,
            priority: $priority,
            state: $node['state']['name'] ?? '',
            branchName: $node['branchName'] ?? null,
            url: $node['url'] ?? null,
            labels: $labels,
            blockedBy: $blockedBy,
            createdAt: $createdAt,
            updatedAt: $updatedAt,
        );
    }
}
