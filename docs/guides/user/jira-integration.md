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
- A Jira Cloud instance. **Jira Data Center is not currently supported** — the client is hard-wired to Cloud-only APIs (REST v3, cursor-based search pagination, `groupId`-based group lookup), so requests against a Data Center instance will fail. Tracked as future work.
- A Jira API token ([Create API token](https://id.atlassian.com/manage-profile/security/api-tokens)).
- The Jira user must have read access to the target project and write access to issue entity properties (used for poll coordination state).

## Credential setup

1. Open your GitHub repo's **Settings > Secrets and variables > Actions**.
2. Add the following secrets and variables:

| Secret / variable | Value |
|---|---|
| `JIRA_TOKEN` | Your Jira API token |
| `JIRA_USER_EMAIL` | Email associated with the token |
| `JIRA_BASE_URL` | Jira instance URL, e.g. `https://myteam.atlassian.net` |

## Repo configuration

No special harness or config changes are needed to *receive* Jira-sourced dispatches: the Jira poller produces the same [NormalizedEvents](../../normative/normalized-event/v1/) that GitHub and GitLab do, so routing and triggers work unchanged. However, the built-in agents' pre/post scripts do not yet understand Jira work-item payloads (they expect a GitHub issue number, not a Jira key — see [#2264](https://github.com/fullsend-ai/fullsend/issues/2264)), so dispatched agent runs will not complete successfully until that follow-up lands. See the Troubleshooting section below.

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
        env:
          GH_TOKEN: ${{ github.token }}
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
  --jql 'project = PROJ AND labels = "fullsend" AND statusCategory != Done ORDER BY updated DESC' \
  --target-repo "${{ github.repository }}" \
  --output dispatches.json \
  --fullsend-dir .fullsend
```

When `--jql` is provided, `--jira-project` is not required. Note that without `--jira-project`, the poller cannot resolve Jira project roles — all actors default to the `external` role. If your routing rules depend on actor roles (e.g., requiring `write` for slash commands), provide `--jira-project` alongside `--jql`.

## Actor role resolution

> **Known limitation.** Roles are resolved by Jira project **role name**, not by actual granted permissions. This is intentional for the MVP — see below.

The poller maps each event actor to an [ADR 0054](../../ADRs/0054-require-authorization-on-all-agent-dispatch-paths.md) role (`read`, `write`, `admin`) by looking up their Jira project role membership and matching on the role's **name**:

| Jira project role name (case-insensitive) | ADR 0054 role |
|---|---|
| `Administrators` | `admin` |
| `Developers` | `write` |
| anything else (including custom role names) | `read` |

This does **not** check the project's permission scheme, so it can be wrong in both directions:

- An org with a custom role literally named `Developers` that has *not* been granted edit permissions will be over-privileged for write-gated dispatch (e.g. `/fs-code`).
- An org using differently-named roles (e.g. `Contributors`, `Engineering`) for people who *do* have edit access will be silently downgraded to `read`, and their slash commands will be ignored.

If your project uses Jira's default role names ("Administrators"/"Developers") with their default permissions, this works as expected. If you use custom role names, expect actors to resolve to `read` regardless of their real permissions until real permission-scheme resolution is implemented (tracked as future work, not planned for the MVP).

### Jira membership is not GitHub membership

> **Known limitation.** No cross-system identity check is performed between Jira and GitHub. The Jira project is the entire authorization boundary for Jira-sourced events.

The role resolved above feeds directly into ADR 0054's dispatch authorization gate — there is no separate check against the target GitHub repo's actual collaborators. This means anyone holding a Jira project role that maps to `write` (Jira's default "Developers" role, by default) can trigger write-gated slash commands like `/fs-code` against your repo, **even if that person has no GitHub access to it at all**.

If your Jira project's membership is broader than your GitHub repo's collaborator list — which is common, since the two are usually administered separately — treat that gap as real: anyone in that gap can use their Jira membership alone to induce agent-proposed changes to your repository. Before enabling this driver, check who holds `write`-mapped roles in the target Jira project and make sure you're comfortable with each of them being able to do that.

## Poll coordination

The poller uses Jira entity properties for distributed lock coordination and checkpoint tracking, following the write-then-verify protocol defined in [ADR 0063](../../ADRs/0063-polling-based-work-discovery.md). Two properties are stored on each processed issue:

- **Lock** (`fullsend.poll.{owner}.{repo}.lock`) — prevents concurrent pollers from *detecting changes on* the same issue simultaneously. The lock covers the change-detection window only: it is released when an issue finishes processing, before the dispatch records are written and consumed by the downstream dispatch step, and it is not renewed while an issue is being processed — a cycle that stalls longer than the stale threshold (15 minutes) can have its lock reclaimed by a concurrent poller, which may produce duplicate dispatches. Lock ownership through dispatch scheduling is part of the same tracked follow-up as dispatch confirmation.
- **Last check** (`fullsend.poll.{owner}.{repo}.lastCheck`) — tracks the timestamp of the most recent processed change. Only changes newer than this timestamp trigger agent dispatch.

These properties are namespaced per target repo, so multiple repos can poll the same Jira project without interference. The properties are visible in Jira's issue properties API but do not appear in the issue UI.

Two edge cases of checkpoint tracking are worth knowing:

- **First enable on an existing backlog.** Every issue starts with an unset `lastCheck`, so on the first poll of an issue the poller has no checkpoint to filter by. Activity is instead bounded by a fixed 24-hour backfill window (not currently configurable): an `opened` event is emitted only for issues created within the window, and only comments and changelog entries from within the window produce events. Backlog issues with no activity (including comment edits) inside the window are silently checkpointed without dispatching. If you want the initial rollout scoped even tighter, narrow the candidate set with a custom `--jql`.
- **Comment edits count as new activity.** The poller filters comments on the later of their created and updated timestamps, so editing a comment (for example, adding a slash command to an old comment) is detected on the next cycle. Jira bumps a comment's updated timestamp on modifications other than body edits too (such as visibility changes), and any such bump counts as activity. The flip side: modifying a comment whose slash command was already dispatched makes it look new again and can re-dispatch it.

Because each cron run is a fresh process, per-cycle Jira API cost is worth sizing for: every cycle that selects at least one issue re-fetches the project's role membership, including paginating the members of any group backing a role (in-process caches do not survive between cron runs). For projects whose roles are backed by large groups, budget Jira rate limits for a full role/group walk every poll interval.

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| 401 on all Jira API calls | Invalid token | Regenerate the API token and update the `JIRA_TOKEN` secret |
| 200 on `/myself` but 403 on issue search | Org restricts personal API tokens for project data | Ask your Atlassian org admin to allow API token access for project data |
| No dispatches produced | No changes since last poll | Check the `lastCheck` entity property on the issue — the poller only dispatches for changes newer than this timestamp |
| Slash command ignored | Actor lacks `write` role in Jira project | The actor must be a member of a Jira project role named exactly "Developers" or "Administrators" — see [Actor role resolution](#actor-role-resolution) if you use custom role names |
| Duplicate dispatches | `lastCheck` was cleared or missing | The poller treats a missing `lastCheck` as "never polled" and processes all recent changes. This is self-correcting — the next cycle advances `lastCheck` past the duplicates |
| Old comment or label change on a newly polled issue never dispatches | Activity predates the first-poll backfill window | On an issue's first poll, activity older than the 24-hour backfill window is permanently skipped (the checkpoint advances past it). To pick up older activity, scope the initial `--jql` to recently updated issues and widen it gradually, or have someone re-trigger by commenting again |
| Dispatched agent workflow fails immediately | Agent pre/post scripts don't understand Jira-keyed payloads yet | Known limitation, tracked in [#2264](https://github.com/fullsend-ai/fullsend/issues/2264). The dispatch step above still runs the workflow and produces a `NormalizedEvent`, but built-in agent scripts expect a GitHub issue number, not a Jira key |

## See also

- [CEL Triggers Reference](cel-triggers-reference.md) — NormalizedEvent fields and routing rules
- [Bring Your Own Agent](bring-your-own-agent.md) — adding custom agents
- [Configuring agent behavior](customizing-agents.md) — harness configuration
