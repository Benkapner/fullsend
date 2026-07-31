# Jira Integration

> **Pre-alpha.** This feature is under active development and not ready for general use. If you're reading this, you're probably helping build it — thank you. Expect rough edges, breaking changes, and missing pieces.

Connect fullsend to a Jira project so that Jira issue activity — comments, label changes — triggers the same agents that run on GitHub and GitLab.

## How it works

A scheduled GitHub Actions workflow runs `fullsend poll --input-driver jira-poll` on a cron. Each cycle:

1. Queries Jira for recently updated issues in your project.
2. Detects new comments and label changes since the last poll.
3. Converts each change to a [NormalizedEvent](../../normative/normalized-event/v1/) — the forge-neutral event struct that all input drivers (GitHub, GitLab, Jira) produce.
4. Routes through the standard agent routing rules.
5. Writes dispatch records that trigger agent workflows.

For the architectural details of the polling protocol, see [ADR 0063 — Polling-based work discovery](../../ADRs/0063-polling-based-work-discovery.md).

The same conventions work across forges:

| Jira action | Agent triggered |
|---|---|
| Comment containing `/fs-triage` | triage |
| Comment containing `/fs-code` | code |
| Comment containing `/fs-review` | review |
| Label `ready-to-code` added | code |
| Label `ready-for-review` added | review |
| Comment on an issue with `needs-info` label | triage |

Slash commands follow the `/fs-{agent}` pattern — any registered agent name works.

## Prerequisites

- A GitHub repo with fullsend installed (`fullsend github setup` completed).
- A Jira Cloud or Data Center instance.
- A Jira personal API token (Cloud: [Create API token](https://id.atlassian.com/manage-profile/security/api-tokens); Data Center: generate a PAT in your profile settings).
- The Jira user must have read access to the target project and write access to issue entity properties (used for poll coordination state).

## Credential setup

1. Open your GitHub repo's **Settings > Secrets and variables > Actions**.
2. Add the following secrets and variables:

| Secret / variable | Value |
|---|---|
| `JIRA_TOKEN` | Your Jira API token or PAT |
| `JIRA_USER_EMAIL` | Email associated with the token (required for Jira Cloud; omit for Data Center) |
| `JIRA_BASE_URL` | Jira instance URL, e.g. `https://myteam.atlassian.net` |

### OAuth 2.0 (alternative)

If your Atlassian org restricts personal API tokens, fullsend also supports OAuth 2.0 client credentials auth. This path is **untested in production** — use it only if token-based auth does not work for your instance.

Set these secrets instead of `JIRA_TOKEN` and `JIRA_USER_EMAIL`:

| Secret / variable | Value |
|---|---|
| `JIRA_AUTH_METHOD` | `oauth2` |
| `JIRA_CLIENT_ID` | OAuth 2.0 client ID from your [Atlassian developer app](https://developer.atlassian.com/console/myapps/) |
| `JIRA_CLIENT_SECRET` | OAuth 2.0 client secret |
| `JIRA_BASE_URL` | Jira instance URL |

Required scopes: `read:jira-work`, `read:jira-user`, `write:jira-work`.

## Repo configuration

No special harness or config changes are needed for built-in agents. The Jira poller produces the same [NormalizedEvents](../../normative/normalized-event/v1/) that GitHub and GitLab do, so the standard triage, code, review, fix, and retro agents work without modification.

If your repo already has a `.fullsend/config.yaml` from `fullsend github setup`, you are ready to go.

## Scheduled workflow

1. Create `.github/workflows/fullsend-poll-jira.yml` with the following content:

```yaml
name: fullsend Jira poll

on:
  schedule:
    - cron: "*/5 * * * *"  # every 5 minutes
  workflow_dispatch: {}     # allow manual runs

permissions:
  actions: write
  contents: read

jobs:
  poll:
    runs-on: ubuntu-24.04
    concurrency:
      group: fullsend-jira-poll
      cancel-in-progress: false
    steps:
      - uses: actions/checkout@v4

      - name: Install fullsend
        run: |
          gh release download --repo fullsend-ai/fullsend -p 'fullsend_*_linux_amd64.tar.gz' -O - | tar xz
          sudo mv fullsend /usr/local/bin/

      - name: Poll Jira
        env:
          JIRA_TOKEN: ${{ secrets.JIRA_TOKEN }}
          JIRA_USER_EMAIL: ${{ secrets.JIRA_USER_EMAIL }}
          JIRA_BASE_URL: ${{ vars.JIRA_BASE_URL }}
        run: |
          fullsend poll \
            --input-driver jira-poll \
            --jira-url "${JIRA_BASE_URL}" \
            --jira-project PROJ \
            --target-repo "${{ github.repository }}" \
            --output dispatches.json \
            --fullsend-dir .fullsend

      - name: Dispatch agent workflows
        env:
          GH_TOKEN: ${{ github.token }}
          JIRA_BASE_URL: ${{ vars.JIRA_BASE_URL }}
        run: |
          set -euo pipefail

          if ! jq -e 'length > 0' dispatches.json > /dev/null 2>&1; then
            echo "No dispatches to process."
            exit 0
          fi

          dispatched=0
          count=$(jq 'length' dispatches.json)

          for i in $(seq 0 $((count - 1))); do
            record=$(jq -c ".[$i]" dispatches.json)
            STAGE=$(echo "$record" | jq -r '.stage')
            RESOURCE_KEY=$(echo "$record" | jq -r '.resource_key')
            EVENT_TYPE=$(echo "$record" | jq -r '.event_type')

            # Extract the Jira issue key from the resource key (e.g. "issue-PROJ-101" → "PROJ-101").
            ISSUE_KEY="${RESOURCE_KEY#issue-}"
            ISSUE_URL="${JIRA_BASE_URL}/browse/${ISSUE_KEY}"

            # Build a minimal event payload compatible with the scaffold agent workflows.
            # The concurrency group uses fromJSON(event_payload).issue.number.
            EVENT_PAYLOAD=$(jq -nc \
              --arg key "$ISSUE_KEY" \
              --arg url "$ISSUE_URL" \
              '{issue: {number: $key, html_url: $url}}')

            # Find the workflow file for this stage by scanning for the
            # "# fullsend-stage: <stage>" marker in workflow files.
            WORKFLOW_NAME=""
            for wf in .github/workflows/*.yml .github/workflows/*.yaml; do
              [[ -f "$wf" ]] || continue
              if grep -qxF "# fullsend-stage: ${STAGE}" "$wf"; then
                WORKFLOW_NAME=$(basename "$wf")
                break
              fi
            done
            if [[ -z "$WORKFLOW_NAME" ]]; then
              echo "::warning::No workflow found for stage ${STAGE}, skipping ${RESOURCE_KEY}"
              continue
            fi

            echo "Dispatching ${WORKFLOW_NAME} for ${ISSUE_KEY} (${STAGE})"
            gh workflow run "$WORKFLOW_NAME" \
              -f event_type="$EVENT_TYPE" \
              -f source_repo="${{ github.repository }}" \
              -f event_payload="$EVENT_PAYLOAD"

            dispatched=$((dispatched + 1))
          done

          echo "::notice::Dispatched ${dispatched} agent workflow(s)"
```

2. Replace `PROJ` with your Jira project key.
3. Commit and push the workflow file.

**`concurrency.cancel-in-progress: false`** ensures overlapping poll cycles queue rather than cancel each other. The poller uses Jira entity properties as distributed locks, so concurrent runs are safe but wasteful.

### Custom JQL

By default the poller searches for all non-done issues in the project, ordered by most recently updated. To narrow the scope, use `--jql`:

```bash
fullsend poll \
  --input-driver jira-poll \
  --jira-url "${JIRA_BASE_URL}" \
  --jql 'project = PROJ AND labels = "fullsend" AND status != Done ORDER BY updated DESC' \
  --target-repo "${{ github.repository }}" \
  --output dispatches.json \
  --fullsend-dir .fullsend
```

When `--jql` is provided, `--jira-project` is not required. Note that without `--jira-project`, the poller cannot resolve Jira project roles — all actors default to the `external` role. If your routing rules depend on actor roles (e.g., requiring `write` for slash commands), provide `--jira-project` alongside `--jql`.

## Poll coordination

The poller uses Jira entity properties for distributed lock coordination and checkpoint tracking, following the write-then-verify protocol defined in [ADR 0063](../../ADRs/0063-polling-based-work-discovery.md). Two properties are stored on each processed issue:

- **Lock** (`fullsend.poll.{owner}.{repo}.lock`) — prevents concurrent pollers from processing the same issue simultaneously.
- **Last check** (`fullsend.poll.{owner}.{repo}.lastCheck`) — tracks the timestamp of the most recent processed change. Only changes newer than this timestamp trigger agent dispatch.

These properties are namespaced per target repo, so multiple repos can poll the same Jira project without interference. The properties are visible in Jira's issue properties API but do not appear in the issue UI.

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| 401 on all Jira API calls | Invalid token | Regenerate the API token and update the `JIRA_TOKEN` secret |
| 200 on `/myself` but 403 on issue search | Org restricts personal API tokens for project data | Try OAuth 2.0 auth (see [OAuth 2.0](#oauth-20-alternative)), or ask your Atlassian org admin to allow API token access |
| No dispatches produced | No changes since last poll | Check the `lastCheck` entity property on the issue — the poller only dispatches for changes newer than this timestamp |
| Slash command ignored | Actor lacks `write` role in Jira project | The actor must be a member of a Jira project role that maps to `write` (typically "Developers") |
| Duplicate dispatches | `lastCheck` was cleared or missing | The poller treats a missing `lastCheck` as "never polled" and processes all recent changes. This is self-correcting — the next cycle advances `lastCheck` past the duplicates |
| `JIRA_USER_EMAIL` error on Data Center | Data Center uses Bearer auth, not Basic | Remove `JIRA_USER_EMAIL` — it is only needed for Jira Cloud |

## See also

- [CEL Triggers Reference](cel-triggers-reference.md) — NormalizedEvent fields and routing rules
- [Bring Your Own Agent](bring-your-own-agent.md) — adding custom agents
- [Configuring agent behavior](customizing-agents.md) — harness configuration
