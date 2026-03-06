package tracker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/leovanalphen/symfony-impl/internal/config"
)

const pageSize = 50

type graphQLRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

type graphQLResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// LinearClient is a GraphQL client for Linear.
type LinearClient struct {
	httpClient *http.Client
}

// NewLinearClient creates a new LinearClient.
func NewLinearClient() *LinearClient {
	return &LinearClient{httpClient: &http.Client{Timeout: 30 * time.Second}}
}

func (c *LinearClient) doQuery(ctx context.Context, endpoint, apiKey, query string, variables map[string]any, result any) error {
	reqBody := graphQLRequest{Query: query, Variables: variables}
	b, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("linear API returned status %d", resp.StatusCode)
	}

	var gqlResp graphQLResponse
	if err := json.NewDecoder(resp.Body).Decode(&gqlResp); err != nil {
		return err
	}
	if len(gqlResp.Errors) > 0 {
		msgs := make([]string, len(gqlResp.Errors))
		for i, e := range gqlResp.Errors {
			msgs[i] = e.Message
		}
		return fmt.Errorf("linear GraphQL errors: %s", strings.Join(msgs, "; "))
	}

	return json.Unmarshal(gqlResp.Data, result)
}

const candidateIssuesQuery = `
query FetchCandidateIssues($projectSlug: String!, $states: [String!]!, $after: String, $first: Int!) {
  issues(
    filter: {
      project: { slugId: { eq: $projectSlug } }
      state: { name: { in: $states } }
    }
    first: $first
    after: $after
  ) {
    pageInfo { hasNextPage endCursor }
    nodes {
      id identifier title description priority
      state { name }
      branchName url
      createdAt updatedAt
      labels { nodes { name } }
      relations(filter: { type: { eq: "blocks" } }) {
        nodes {
          relatedIssue { id identifier state { name } }
        }
      }
    }
  }
}
`

type linearIssueNode struct {
	ID          string `json:"id"`
	Identifier  string `json:"identifier"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Priority    *int   `json:"priority"`
	State       struct {
		Name string `json:"name"`
	} `json:"state"`
	BranchName string `json:"branchName"`
	URL        string `json:"url"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
	Labels     struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`
	Relations struct {
		Nodes []struct {
			RelatedIssue struct {
				ID         string `json:"id"`
				Identifier string `json:"identifier"`
				State      struct {
					Name string `json:"name"`
				} `json:"state"`
			} `json:"relatedIssue"`
		} `json:"nodes"`
	} `json:"relations"`
}

type issuesPage struct {
	Issues struct {
		PageInfo struct {
			HasNextPage bool   `json:"hasNextPage"`
			EndCursor   string `json:"endCursor"`
		} `json:"pageInfo"`
		Nodes []linearIssueNode `json:"nodes"`
	} `json:"issues"`
}

func normalizeIssue(n linearIssueNode) Issue {
	issue := Issue{
		ID:          n.ID,
		Identifier:  n.Identifier,
		Title:       n.Title,
		Description: n.Description,
		Priority:    n.Priority,
		State:       n.State.Name,
		BranchName:  n.BranchName,
		URL:         n.URL,
	}

	for _, lbl := range n.Labels.Nodes {
		issue.Labels = append(issue.Labels, strings.ToLower(lbl.Name))
	}

	for _, rel := range n.Relations.Nodes {
		issue.BlockedBy = append(issue.BlockedBy, BlockerRef{
			ID:         rel.RelatedIssue.ID,
			Identifier: rel.RelatedIssue.Identifier,
			State:      rel.RelatedIssue.State.Name,
		})
	}

	if n.CreatedAt != "" {
		if t, err := time.Parse(time.RFC3339, n.CreatedAt); err == nil {
			issue.CreatedAt = &t
		}
	}
	if n.UpdatedAt != "" {
		if t, err := time.Parse(time.RFC3339, n.UpdatedAt); err == nil {
			issue.UpdatedAt = &t
		}
	}

	return issue
}

// FetchCandidateIssues fetches paginated issues matching active states.
func (c *LinearClient) FetchCandidateIssues(ctx context.Context, cfg *config.Config) ([]Issue, error) {
	var all []Issue
	var after string

	for {
		vars := map[string]any{
			"projectSlug": cfg.TrackerProjectSlug(),
			"states":      cfg.TrackerActiveStates(),
			"first":       pageSize,
		}
		if after != "" {
			vars["after"] = after
		}

		var page issuesPage
		if err := c.doQuery(ctx, cfg.TrackerEndpoint(), cfg.TrackerAPIKey(), candidateIssuesQuery, vars, &page); err != nil {
			return nil, err
		}

		for _, n := range page.Issues.Nodes {
			all = append(all, normalizeIssue(n))
		}

		if !page.Issues.PageInfo.HasNextPage {
			break
		}
		after = page.Issues.PageInfo.EndCursor
	}

	return all, nil
}

const issuesByStatesQuery = `
query FetchIssuesByStates($projectSlug: String!, $states: [String!]!, $after: String, $first: Int!) {
  issues(
    filter: {
      project: { slugId: { eq: $projectSlug } }
      state: { name: { in: $states } }
    }
    first: $first
    after: $after
  ) {
    pageInfo { hasNextPage endCursor }
    nodes {
      id identifier title description priority
      state { name }
      branchName url
      createdAt updatedAt
      labels { nodes { name } }
      relations(filter: { type: { eq: "blocks" } }) {
        nodes {
          relatedIssue { id identifier state { name } }
        }
      }
    }
  }
}
`

// FetchIssuesByStates fetches issues matching any of the given state names.
func (c *LinearClient) FetchIssuesByStates(ctx context.Context, cfg *config.Config, stateNames []string) ([]Issue, error) {
	var all []Issue
	var after string

	for {
		vars := map[string]any{
			"projectSlug": cfg.TrackerProjectSlug(),
			"states":      stateNames,
			"first":       pageSize,
		}
		if after != "" {
			vars["after"] = after
		}

		var page issuesPage
		if err := c.doQuery(ctx, cfg.TrackerEndpoint(), cfg.TrackerAPIKey(), issuesByStatesQuery, vars, &page); err != nil {
			return nil, err
		}

		for _, n := range page.Issues.Nodes {
			all = append(all, normalizeIssue(n))
		}

		if !page.Issues.PageInfo.HasNextPage {
			break
		}
		after = page.Issues.PageInfo.EndCursor
	}

	return all, nil
}

const issuesByIDsQuery = `
query FetchIssuesByIDs($ids: [ID!]!) {
  issues(filter: { id: { in: $ids } }, first: 100) {
    nodes {
      id identifier
      state { name }
    }
  }
}
`

type issueStateResult struct {
	Issues struct {
		Nodes []struct {
			ID         string `json:"id"`
			Identifier string `json:"identifier"`
			State      struct {
				Name string `json:"name"`
			} `json:"state"`
		} `json:"nodes"`
	} `json:"issues"`
}

// FetchIssueStatesByIDs fetches current states for the given issue IDs.
func (c *LinearClient) FetchIssueStatesByIDs(ctx context.Context, cfg *config.Config, issueIDs []string) (map[string]string, error) {
	if len(issueIDs) == 0 {
		return map[string]string{}, nil
	}

	vars := map[string]any{"ids": issueIDs}
	var result issueStateResult
	if err := c.doQuery(ctx, cfg.TrackerEndpoint(), cfg.TrackerAPIKey(), issuesByIDsQuery, vars, &result); err != nil {
		return nil, err
	}

	out := make(map[string]string, len(result.Issues.Nodes))
	for _, n := range result.Issues.Nodes {
		out[n.ID] = n.State.Name
	}
	return out, nil
}