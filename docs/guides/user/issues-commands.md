# Issue Commands

Read and write issue content across GitHub, GitLab, and Jira from
custom agent scripts using `fullsend issues get` and
`fullsend issues post-comment`.

## When to use

The built-in agent pipeline handles comment posting automatically for
GitHub-sourced events. Use these commands when:

- A custom agent needs to post results back to **Jira** or **GitLab**.
- An agent script needs to **read** issue content (title, body, labels,
  comments) from any tracker as structured JSON.
- You need **sticky comments** (find-and-update-by-marker) on a
  non-GitHub tracker.

## `fullsend issues get`

Reads an issue's title, body, labels, and comments from the specified
tracker and prints them as JSON.

```bash
fullsend issues get \
  --tracker github \
  --project owner/repo \
  --number 42
```

For Jira, `--project` is the project key and `--number` is the numeric
issue ID (not the human-readable key):

```bash
fullsend issues get \
  --tracker jira \
  --project PROJ \
  --number 101 \
  --jira-url https://myteam.atlassian.net \
  --jira-email you@example.com
```

### Flags

| Flag | Required | Description |
|------|----------|-------------|
| `--tracker` | Yes (unless config default set) | Tracker backend: `github`, `gitlab`, or `jira` |
| `--project` | Yes | Project identifier: `owner/repo` (GitHub/GitLab) or project key (Jira) |
| `--number` | Yes | Issue number (must be a positive integer) |
| `--token` | No | API token (default: env var per tracker) |
| `--jira-url` | Jira only | Jira instance URL (default: `$JIRA_BASE_URL`) |
| `--jira-email` | Jira only | Jira user email for auth (default: `$JIRA_USER_EMAIL`) |
| `--fullsend-dir` | No | Path to `.fullsend` config directory (sources a default `--tracker` from its `config.yaml`) |

## `fullsend issues post-comment`

Posts a comment with a hidden HTML marker on an issue. On re-runs,
finds the existing comment by its marker and edits in-place, collapsing
old content into `<details>` blocks. This prevents comment flooding
on re-runs.

```bash
echo "Triage complete. See PR #99." | fullsend issues post-comment \
  --tracker jira \
  --project PROJ \
  --number 101 \
  --marker "<!-- fullsend:triage-agent -->" \
  --jira-url https://myteam.atlassian.net \
  --jira-email you@example.com
```

### Flags

| Flag | Required | Description |
|------|----------|-------------|
| `--tracker` | Yes (unless config default set) | Tracker backend: `github`, `gitlab`, or `jira` |
| `--project` | Yes | Project identifier: `owner/repo` (GitHub/GitLab) or project key (Jira) |
| `--number` | Yes | Issue number (must be a positive integer) |
| `--marker` | Yes | Hidden HTML marker for idempotent updates (e.g. `<!-- fullsend:my-agent -->`) |
| `--result` | No | Path to comment body file, or `-` for stdin (default: `-`) |
| `--token` | No | API token (default: env var per tracker) |
| `--jira-url` | Jira only | Jira instance URL (default: `$JIRA_BASE_URL`) |
| `--jira-email` | Jira only | Jira user email for auth (default: `$JIRA_USER_EMAIL`) |
| `--dry-run` | No | Print what would be posted without making API calls |
| `--fullsend-dir` | No | Path to `.fullsend` config directory (sources a default `--tracker` from its `config.yaml`) |

### Jira marker constraints

For `--tracker jira`, the `--marker` value must not contain `\`, `*`,
`_`, `` ` ``, `[`, `]`, or `&`. Jira's markdown round-trip escapes
these characters, which would break marker re-detection on later runs.

## Config-based default tracker

Set a default tracker in `.fullsend/config.yaml` to avoid passing
`--tracker` on every invocation:

```yaml
tracker: jira
```

Then pass `--fullsend-dir .fullsend` (or let the agent pipeline supply
it). An explicit `--tracker` flag overrides the config default. See
[Layered Config Reference](../infrastructure/layered-config-reference.md)
for how the `tracker` field resolves through the config overlay chain.

## Trust model

Marker-based comment lookup does not verify the comment author. In a
trusted CI environment (the intended deployment) this is safe because
only the bot writes marker-bearing comments. If untrusted users can
post issue comments containing your marker string, they could cause
the bot to edit their comment instead of creating its own. Do not use
this command in environments where untrusted users can write arbitrary
issue comments bearing your marker.

## Environment variables

| Variable | Tracker | Description |
|----------|---------|-------------|
| `GH_TOKEN` or `GITHUB_TOKEN` | GitHub | GitHub API token |
| `GITLAB_TOKEN` | GitLab | GitLab API token |
| `JIRA_TOKEN` | Jira | Jira API token |
| `JIRA_BASE_URL` | Jira | Jira instance URL |
| `JIRA_USER_EMAIL` | Jira | Email for Jira Cloud Basic auth |

## See also

- [Jira Integration](jira-integration.md) -- polling and dispatch setup
- [Bring Your Own Agent](bring-your-own-agent.md) -- adding custom agents
- [Layered Config Reference](../infrastructure/layered-config-reference.md) -- config field documentation
