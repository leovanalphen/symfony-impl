---
tracker:
  kind: linear
  api_key: $LINEAR_API_KEY
  project_slug: my-project
  active_states:
    - Todo
    - In Progress
  terminal_states:
    - Done
    - Cancelled
    - Canceled
    - Closed
    - Duplicate

polling:
  interval_ms: 30000

workspace:
  root: ~/symphony_workspaces

agent:
  max_concurrent_agents: 5

codex:
  command: codex app-server
  turn_timeout_ms: 3600000

server:
  port: 0
---

You are a software engineer working on issue {{ issue.identifier }}: {{ issue.title }}.

## Issue Details

- **ID**: {{ issue.identifier }}
- **Title**: {{ issue.title }}
- **State**: {{ issue.state }}
- **Priority**: {{ issue.priority }}
- **Branch**: {{ issue.branch_name }}
- **URL**: {{ issue.url }}

## Description

{{ issue.description }}

## Labels

{% for label in issue.labels %}
- {{ label }}
{% endfor %}

## Instructions

Please implement the changes described in this issue. Follow the project's coding conventions, write tests, and ensure all existing tests continue to pass.

When you are done, provide a summary of the changes you made.
