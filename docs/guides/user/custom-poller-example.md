# Custom Poller Example

This guide shows how to create a custom poller in your own repository that invokes fullsend harness agents directly, bypassing the standard GitHub event trigger flow.

## Use Case

Custom pollers are useful when you want to:
- Poll external systems (Jira, Linear, Slack, etc.) on a schedule
- Trigger harness agents based on custom logic
- Reuse fullsend's harness infrastructure without duplicating workflow code

## Example: Jira Polling Workflow

This example polls Jira for bugs and dispatches fullsend agents to process them:

```yaml
name: Fullsend Jira Poll

on:
  schedule:
    - cron: '*/30 * * * *'  # every 30 minutes
  workflow_dispatch:

jobs:
  poll:
    runs-on: ubuntu-24.04
    outputs:
      matrix: ${{ steps.dispatch.outputs.matrix }}
    steps:
      - name: Checkout
        uses: actions/checkout@v4

      - name: Install fullsend CLI
        run: |
          # Download and install fullsend CLI
          # (use your preferred installation method)

      - name: Poll Jira and build dispatch matrix
        id: dispatch
        env:
          JIRA_TOKEN: ${{ secrets.JIRA_TOKEN }}
          JIRA_USER_EMAIL: ${{ secrets.JIRA_USER_EMAIL }}
          JIRA_BASE_URL: ${{ vars.JIRA_BASE_URL }}
        run: |
          # Query Jira for relevant issues
          # Build a matrix in the format fullsend dispatch produces
          MATRIX=$(fullsend poll jira \
            --jql 'project=MYPROJECT and statusCategory != Done and updated > -1week and type=Bug' \
            --output-driver gha-matrix)

          echo "matrix<<EOF" >> $GITHUB_OUTPUT
          echo "$MATRIX" >> $GITHUB_OUTPUT
          echo "EOF" >> $GITHUB_OUTPUT

  harness:
    needs: poll
    permissions:
      actions: write
      contents: read
      id-token: write
      issues: write
      pull-requests: write
    uses: fullsend-ai/fullsend/.github/workflows/reusable-dispatch.yml@v0
    with:
      matrix: ${{ needs.poll.outputs.matrix }}
      mint_url: ${{ vars.FULLSEND_MINT_URL }}
      gcp_region: ${{ vars.FULLSEND_GCP_REGION }}
    secrets:
      FULLSEND_GCP_WIF_PROVIDER: ${{ secrets.FULLSEND_GCP_WIF_PROVIDER }}
      FULLSEND_GCP_PROJECT_ID: ${{ secrets.FULLSEND_GCP_PROJECT_ID }}
      OTEL_EXPORTER_OTLP_TRACES_HEADERS: ${{ secrets.OTEL_EXPORTER_OTLP_TRACES_HEADERS }}
      OTEL_EXPORTER_OTLP_HEADERS: ${{ secrets.OTEL_EXPORTER_OTLP_HEADERS }}
      JIRA_TOKEN: ${{ secrets.JIRA_TOKEN }}
      JIRA_USER_EMAIL: ${{ secrets.JIRA_USER_EMAIL }}
```

## Matrix Format

The `matrix` input must follow the format produced by `fullsend dispatch --output-driver gha-matrix`:

```json
{
  "include": [
    {
      "agent": "agent-name",
      "source_repo": "org/repo",
      "role": "harness",
      "event_payload": "{...}",
      "status_repo": "org/repo",
      "status_number": "123"
    }
  ]
}
```

## Required Configuration

Your external repository needs these variables and secrets configured:

**Variables:**
- `FULLSEND_MINT_URL` - Token mint service URL
- `FULLSEND_GCP_REGION` - GCP region for Vertex AI
- `OTEL_EXPORTER_OTLP_ENDPOINT` - OpenTelemetry endpoint (optional)
- `JIRA_BASE_URL` - Jira instance URL (if using Jira agents)

**Secrets:**
- `FULLSEND_GCP_WIF_PROVIDER` - GCP Workload Identity Federation provider
- `FULLSEND_GCP_PROJECT_ID` - GCP project ID
- `OTEL_EXPORTER_OTLP_TRACES_HEADERS` - OTEL auth headers (optional)
- `JIRA_TOKEN` - Jira API token (if using Jira agents)
- `JIRA_USER_EMAIL` - Jira user email (if using Jira agents)

## How It Works

1. **Poll job** runs your custom logic to query an external system and builds a dispatch matrix
2. **Harness job** calls `reusable-dispatch.yml` with the pre-computed matrix
3. `reusable-dispatch.yml` skips the routing and dispatch steps, directly invoking `harness-run` with your matrix
4. Harness agents execute according to your matrix configuration

## Authorization

When using a pre-computed matrix, the `.fullsend/config.yaml` agent-enablement checks are bypassed. The token mint service acts as the authorization boundary - ensure your mint service is properly configured to control which agents external callers can invoke.
