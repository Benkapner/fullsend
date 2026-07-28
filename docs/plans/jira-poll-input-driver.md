# Implementation Plan: Jira Poll Input Driver

**Context:** [ADR 0063](../ADRs/0063-polling-based-work-discovery.md) decides a polling-based work discovery model with Jira as the first input driver. This document contains the implementation plan for the `jira-poll` input driver, targeting per-repo mode with GitHub Actions as the output driver.

**Assumptions:** Jira credentials (API token) are made available as target repo secrets. Credential management (ADR 0069, issue #2269) and privacy allowlists (PR #3428) are deferred — this implementation assumes a `JIRA_TOKEN` secret and `JIRA_BASE_URL` variable are configured in the target GitHub repo.

## Table of Contents

1. [Dependency Graph](#dependency-graph)
2. [Phase 1: Jira REST API Client](#phase-1-jira-rest-api-client)
3. [Phase 2: Jira Poll Driver](#phase-2-jira-poll-driver)
4. [Phase 3: CLI Wiring](#phase-3-cli-wiring)
5. [Verification Checklist](#verification-checklist)

## Dependency Graph

```
Phase 1 (Jira API client) ──> Phase 2 (Jira poll driver) ──> Phase 3 (CLI wiring)
```

Phase 1 has no dependencies on existing code beyond `net/http`. Phase 2 depends on Phase 1 and reuses `dispatch.NormalizedEvent`, `dispatch.HarnessRouter`. Phase 3 depends on Phase 2.

## Phase 1: Jira REST API Client

**Goal:** HTTP client for Jira Cloud REST API v3, covering the API surface needed by the poll driver.

**Package:** `internal/forge/jira/`

### Files

- `client.go` — Client struct, constructor, HTTP plumbing (auth, base URL, error handling, pagination)
- `types.go` — Jira API response types
- `client_test.go` — httptest-based tests

### Client methods

```go
type Client struct {
    baseURL    string
    token      string
    httpClient *http.Client
}

// Issue discovery
SearchIssues(ctx, jql string, startAt int) (*SearchResult, error)
GetIssue(ctx, issueIDOrKey string) (*Issue, error)

// Change detection
ListComments(ctx, issueIDOrKey string) ([]Comment, error)
ListChangelog(ctx, issueIDOrKey string) ([]ChangelogEntry, error)

// Entity properties (lock + lastCheck state)
GetEntityProperty(ctx, issueIDOrKey, propertyKey string) (json.RawMessage, error)
SetEntityProperty(ctx, issueIDOrKey, propertyKey string, value any) error
DeleteEntityProperty(ctx, issueIDOrKey, propertyKey string) error

// Actor resolution
GetProjectRoleMembers(ctx, projectKeyOrID, roleName string) ([]RoleMember, error)
GetMyself(ctx) (*User, error)
```

### Types

```go
type Issue struct {
    ID     string      `json:"id"`
    Key    string      `json:"key"`
    Self   string      `json:"self"`
    Fields IssueFields `json:"fields"`
}

type IssueFields struct {
    Summary     string    `json:"summary"`
    Description string    `json:"description"`
    Status      Status    `json:"status"`
    Labels      []string  `json:"labels"`
    Reporter    User      `json:"reporter"`
    Created     string    `json:"created"`
    Updated     string    `json:"updated"`
    Comment     *CommentPage `json:"comment,omitempty"`
}

type Comment struct {
    ID      string `json:"id"`
    Body    any    `json:"body"` // ADF or string
    Author  User   `json:"author"`
    Created string `json:"created"`
    Updated string `json:"updated"`
}

type ChangelogEntry struct {
    ID      string         `json:"id"`
    Author  User           `json:"author"`
    Created string         `json:"created"`
    Items   []ChangeItem   `json:"items"`
}

type ChangeItem struct {
    Field      string `json:"field"`
    FromString string `json:"fromString"`
    ToString   string `json:"toString"`
}

type User struct {
    AccountID   string `json:"accountId"`
    DisplayName string `json:"displayName"`
    AccountType string `json:"accountType"` // "atlassian", "app", "customer"
    Active      bool   `json:"active"`
}

type SearchResult struct {
    Issues     []Issue `json:"issues"`
    Total      int     `json:"total"`
    MaxResults int     `json:"maxResults"`
    StartAt    int     `json:"startAt"`
}
```

### HTTP plumbing

- Auth: `Authorization: Basic base64(email:token)` for Jira Cloud, or `Authorization: Bearer <PAT>` for Jira Data Center
- Base URL: `https://{instance}.atlassian.net` (Cloud) or custom (Data Center)
- Pagination: `startAt` + `maxResults` loop for search; comment and changelog APIs paginate similarly
- Error handling: structured Jira error responses, rate limit headers (`X-RateLimit-*`, `Retry-After`)
- All methods must exhaust pagination and return complete result sets

### Test plan

- Auth header verification (Basic and Bearer)
- Pagination (multi-page search results)
- Error responses (401, 403, 404, 429 rate limit)
- Entity property CRUD round-trip
- Comment and changelog ordering

## Phase 2: Jira Poll Driver

**Goal:** Poll cycle that discovers Jira issue changes, converts them to `NormalizedEvent`, routes through the dispatch core, and outputs dispatch records.

**Package:** `internal/jirapoll/`

### Files

- `poller.go` — Poll cycle orchestrator
- `discover.go` — JQL search + per-issue change detection
- `convert.go` — Jira event → `dispatch.NormalizedEvent` per [jira-poll-adapter.md](../normative/normalized-event/v1/jira-poll-adapter.md)
- `lock.go` — Write-then-verify lock coordination on Jira entity properties
- `types.go` — JiraClient interface, JiraEvent intermediate type, Options
- `poller_test.go` — Mock client, full cycle tests
- `convert_test.go` — Table-driven conversion tests against spec + example fixture

### JiraClient interface

```go
type JiraClient interface {
    SearchIssues(ctx context.Context, jql string, startAt int) (*jira.SearchResult, error)
    GetIssue(ctx context.Context, issueIDOrKey string) (*jira.Issue, error)
    ListComments(ctx context.Context, issueIDOrKey string) ([]jira.Comment, error)
    ListChangelog(ctx context.Context, issueIDOrKey string) ([]jira.ChangelogEntry, error)
    GetEntityProperty(ctx context.Context, issueIDOrKey, propertyKey string) (json.RawMessage, error)
    SetEntityProperty(ctx context.Context, issueIDOrKey, propertyKey string, value any) error
    DeleteEntityProperty(ctx context.Context, issueIDOrKey, propertyKey string) error
    GetMyself(ctx context.Context) (*jira.User, error)
}
```

### Poll cycle (`Poller.Run`)

Per ADR 0063 §"Jira poll input driver — write-then-verify coordination":

```
1. Assign UUID for this poll cycle
2. Execute JQL query(s) → up to M candidate issues
3. For each candidate, read lock property (client-side filter)
   - Skip if locked by another poller (unless stale)
   - Remove stale locks (threshold exceeded)
4. Randomly select N unlocked candidates
5. For each selected issue:
   a. Write lock (UUID + timestamp) to entity property
   b. Wait 500-1500ms (jitter)
   c. Re-read lock property
   d. If lock UUID matches:
      - Read lastCheck from entity property
      - Detect changes since lastCheck (comments, changelog)
      - Convert each change to NormalizedEvent
      - Route through HarnessRouter
      - For matched stages: build Dispatch record
      - Advance lastCheck to latest processed change timestamp
   e. If lock UUID doesn't match: skip (lost race)
6. Write dispatch records to output (JSON file for MVP)
7. Log cycle summary
```

### Change detection (per-issue)

```go
func (p *Poller) detectChanges(ctx context.Context, issue jira.Issue, lastCheck time.Time) ([]JiraEvent, error)
```

For each issue since `lastCheck`:
- **Comments:** List comments, filter by `created > lastCheck`, emit `comment_added` events
- **Changelog:** List changelog entries, filter by `created > lastCheck`:
  - `labels` field change → `label_changed` event (one per label added/removed)
  - `status` field change → `opened`/`reopened`/`closed` as appropriate
  - `summary`/`description` change → `edited`
- **New issue:** If `lastCheck` is zero (first poll), emit `opened`

### NormalizedEvent conversion

Per [jira-poll-adapter.md](../normative/normalized-event/v1/jira-poll-adapter.md):

| Jira field | NormalizedEvent field |
|---|---|
| Target repo (from config/env) | `repo` |
| `"jira"` | `source.system` |
| Issue numeric ID | `entity.id` |
| Issue key (`PROJ-123`) | `entity.key` |
| `{baseURL}/browse/{key}` | `entity.url` |
| `"work_item"` | `entity.kind` |
| Comment author `accountId` | `actor.id` |
| `accountType == "app"` → `"bot"` | `actor.kind` |
| Jira project role → ADR 0054 role | `actor.role` |
| Author == reporter | `actor.is_entity_author` |
| `fields.labels` snapshot | `state.labels` |

### Entity property keys

Namespaced per ADR 0063:
- Lock: `fullsend.poll.{owner}.{repo}.lock`
- LastCheck: `fullsend.poll.{owner}.{repo}.lastCheck`

Lock property value:
```json
{
  "id": "<uuid>",
  "ts": "<RFC3339>",
  "phase": "pending|running",
  "run_id": "<workflow-run-id>"
}
```

### Dispatch output (MVP)

For MVP, write dispatch records to JSON file (same `poll.Dispatch` type as GitLab poller). A GitHub Actions workflow step processes the file and triggers agent runner workflows via `gh workflow run`. Direct `gha-dispatch` API integration is a follow-up.

### Actor role mapping

Per jira-poll-adapter.md:
- Jira "Administrators" → `admin`
- Jira "Developers" / project role with write access → `write`
- Jira "Reporter" / "Viewer" → `read`
- Cannot resolve → `external`

MVP simplification: if the actor has a Jira project role, map it. Do not cross-reference with GitHub repo permissions (that requires identity linking, which is out of scope).

### Test plan

- **Mock client tests:**
  - Poll cycle discovers new comments → dispatches triage
  - Poll cycle discovers `/fs-triage` slash command → dispatches triage
  - Poll cycle discovers `ready-to-code` label → dispatches code
  - Deduplication: second poll with same events → no re-dispatch
  - Lock contention: lost lock → skip issue
  - Stale lock: expired lock → clean up and proceed
  - Retry budget: failed events tracked, exhausted budget skipped
  - Bot filtering: `accountType: app` events filtered
- **Conversion tests:**
  - Validate against `jira-fs-triage-comment.json` example fixture
  - Comment with `/fs-triage` → correct command + instruction extraction
  - Label change → correct `transition.label` fields
  - Status transition → correct `transition.kind`

## Phase 3: CLI Wiring

**Goal:** Extend `fullsend poll` to support Jira as an input driver.

**File:** `internal/cli/poll.go`

### Changes

Extend the `fullsend poll` command to accept `--input-driver jira-poll` alongside the existing `--forge gitlab` path:

```
fullsend poll --input-driver jira-poll \
  --jira-url https://acme.atlassian.net \
  --jira-project PROJ \
  --target-repo acme/platform \
  --output dispatches.json \
  --fullsend-dir .fullsend
```

New flags:
- `--input-driver` — poll input driver type (`jira-poll`); when set, `--forge` is not required
- `--jira-url` — Jira instance base URL (default: `$JIRA_BASE_URL`)
- `--jira-project` — Jira project key for JQL scoping
- `--jql` — optional override JQL (default: `project = {project} AND status != Done ORDER BY updated DESC`)
- `--target-repo` — GitHub repo slug where agents run (default: `$GITHUB_REPOSITORY`)

Env vars:
- `JIRA_TOKEN` — Jira API token (required)
- `JIRA_USER_EMAIL` — Jira user email for Basic auth (required for Cloud)
- `JIRA_BASE_URL` — fallback for `--jira-url`
- `GITHUB_REPOSITORY` — fallback for `--target-repo`

### Wiring

```go
case "jira-poll":
    jiraClient := jira.New(jiraToken, jira.WithBaseURL(jiraURL), jira.WithEmail(jiraEmail))
    router, err := buildRouter(fullsendDir)
    poller := jirapoll.New(jiraClient, router, jirapoll.Options{
        TargetRepo:  targetRepo,
        JiraProject: jiraProject,
        JQL:         jql,
        OutputPath:  outputPath,
    })
    return poller.Run(cmd.Context())
```

## Pre-merge: actor.role hardcoded to "write"

**Status:** Resolved. `actor.role` now uses Jira project role lookup via `GET /rest/api/3/project/{key}/role`. Administrators map to `admin`, Developers to `write`, other named roles to `read`, and actors not in any project role to `external`.

The current implementation hardcodes `actor.role = "write"` for all actors (see `convert.go` `// MVP default`). The [jira-poll-adapter spec](../normative/normalized-event/v1/jira-poll-adapter.md) requires mapping Jira project roles to ADR 0054 roles and using `"external"` when membership cannot be resolved.

This means any Jira user — including external collaborators — will be treated as having `write` permission when the authorization gate in `fullsend dispatch` evaluates their role. Depending on how harness triggers are configured, this could allow unauthorized agent dispatch.

**Options:**
1. Query Jira project roles via `/rest/api/3/project/{key}/role` and map to ADR 0054 roles (Administrators→admin, Developers→write, Reporter/Viewer→read, unknown→external).
2. Default to `"external"` instead of `"write"` and require harness triggers to explicitly opt in to external actors, which fails closed.
3. Accept `"write"` as MVP with a documented risk note, gated behind a per-repo opt-in flag.

Option 2 is the safest short-term fix if full role resolution is deferred.

## Pre-merge: search/jql API migration

**Status:** In progress. Partially fixed, needs completion.

The old `GET /rest/api/3/search` endpoint has been removed by Atlassian (410 Gone). The replacement is `POST /rest/api/3/search/jql` with these breaking changes discovered during live testing against `stage-redhat.atlassian.net`:

1. **`expand` is a comma-delimited string, not an array.** Sending `["changelog"]` returns 400. Must send `"changelog"`. Fixed in client but tests need updating.
2. **Cursor-based pagination.** The new endpoint returns `nextPageToken` + `isLast` instead of `startAt`/`total`/`maxResults`. The client's `SearchIssues` loop uses `startAt`-based pagination which won't work. Must switch to passing `nextPageToken` in subsequent requests.
3. **Default response is IDs only.** Unlike the old endpoint, `fields` defaults to `["id"]`. Must explicitly request `["*all"]` or specific fields to get issue data back.

These need to be fixed before the poller works against any live Jira Cloud instance.

## Verification Checklist

- [ ] `go test ./internal/forge/jira/...` passes
- [ ] `go test ./internal/jirapoll/...` passes
- [ ] `go vet ./...` clean
- [ ] `golangci-lint run` clean
- [ ] Jira poll adapter spec compliance: conversion tests match `jira-fs-triage-comment.json` fixture
- [ ] Lock coordination: write-then-verify with jitter tested (race simulation)
- [ ] Stale lock cleanup tested
- [ ] Bot event filtering tested (`accountType: app`)
- [ ] Slash command extraction tested (`/fs-triage`, `/fs-code`, etc.)
- [ ] Label change detection tested (added and removed)
- [ ] CLI `--input-driver jira-poll` flag accepted and routes to Jira poller
- [ ] Dispatch records written to JSON output file

## Behaviour Test Design

### Overview

The Jira poll behaviour tests validate the poll-to-dispatch-record path: mock Jira server serving realistic API responses, the real `fullsend poll --input-driver jira-poll` binary consuming them, harness CEL routing producing dispatch records. This is a narrower slice than the GitHub dispatch behaviour tests (which go all the way through workflow dispatch and artifact collection) but covers the critical integration seam between the Jira API client, the poll driver, and the harness router.

### Mock Jira server

An `httptest.Server` implementing the Jira REST API endpoints that `jira.Client` calls:

| Endpoint | Purpose |
|----------|---------|
| `POST /rest/api/3/search` | Return canned issues matching JQL |
| `GET /rest/api/3/issue/{key}` | Return a single issue |
| `GET /rest/api/3/issue/{key}/comment` | Return comments (including slash commands) |
| `GET /rest/api/3/issue/{key}/changelog` | Return changelog entries (label changes, status transitions) |
| `GET /rest/api/3/issue/{key}/properties/{key}` | Read entity properties (lock, lastCheck) |
| `PUT /rest/api/3/issue/{key}/properties/{key}` | Write entity properties |
| `DELETE /rest/api/3/issue/{key}/properties/{key}` | Delete entity properties |
| `GET /rest/api/3/myself` | Return bot user identity |

The mock server holds mutable state (issues, comments, changelog entries, entity properties) that step definitions manipulate before running the poller. For example, `When a comment "/fs-triage ..." is added to Jira issue "PROJ-101"` appends a `jira.Comment` to the mock's in-memory store. The mock validates auth headers (Basic or Bearer) to catch misconfiguration early.

This is the same pattern used by `internal/forge/jira/client_test.go` (httptest-based tests for the Jira client), but lifted into the behaviour test framework as a shared driver.

### New step definitions

These steps would live in `pkg/behaviourtest/steps/jirapoll.go` and be registered via `registerJiraPollSteps(sc)` in `registry.go`:

| Step | Implementation |
|------|----------------|
| `Given a mock Jira server` | Start an `httptest.Server`, store its URL and a `*mockJiraState` in `World`. Set `JIRA_TOKEN`, `JIRA_BASE_URL`, and `JIRA_USER_EMAIL` env vars (or pass via CLI flags). |
| `Given a Jira issue {key} with labels {labels}` | Add an issue to the mock state with the given key and comma/space-separated labels. Assigns a synthetic numeric ID. |
| `When a comment {body} is added to Jira issue {key}` | Append a `jira.Comment` to the mock's comment store for the issue. Author is a canned human user (not bot). |
| `When the label {label} is added to Jira issue {key}` | Append a `jira.ChangelogEntry` with a `labels` field change to the mock's changelog store. Update the issue's label list. |
| `And the Jira poller runs` | Build and exec `fullsend poll --input-driver jira-poll --jira-url {mock URL} --jira-project PROJ --target-repo {test org/repo} --output {tmpfile} --fullsend-dir {config dir}`. Parse the output file into `[]poll.Dispatch` and store in `World`. |
| `Then the dispatch output contains a {stage} stage for issue {key}` | Assert that the stored dispatches include a record with the expected stage and a `ResourceKey` matching `issue-{key}`. |
| `Then the dispatch output does not contain a stage for issue {key}` | Assert absence. |

### Test wiring

```
  httptest.Server (mock Jira API)
        ^
        |  HTTP
        |
  fullsend poll --input-driver jira-poll \
    --jira-url http://127.0.0.1:{port} \
    --jira-project PROJ \
    --target-repo {org}/{repo} \
    --output /tmp/dispatches.json \
    --fullsend-dir {harness config dir}
        |
        v
  dispatches.json  -->  step assertions
```

The `--fullsend-dir` points to a temporary directory where the `Given a custom harness` step has written `.fullsend/harness/*.yaml` and `.fullsend/config.yaml`. This is the same config layout the existing dispatch scenarios use (committed via SCM driver to GitHub), but for the Jira poll test the config is written locally — the poller reads config from disk, not from a remote repo.

This means the `Given a custom harness` step needs a local-filesystem variant (or the test pre-seeds the directory before the poller runs). The simplest approach: write harness YAML to a temp dir in the `Given a custom harness` step when the scenario is tagged `@requires:jira-mock`, and pass that dir as `--fullsend-dir`.

Environment variables required by `runJiraPoll`:
- `JIRA_TOKEN` — set to a dummy value; the mock server accepts any token.
- `JIRA_USER_EMAIL` — set to `test@example.com`.
- `GITHUB_REPOSITORY` — set to `{org}/{repo}` (or pass `--target-repo`).

### What the test proves

- The Jira client correctly parses mock API responses (search, comments, changelog, entity properties).
- Change detection identifies new comments and label changes since lastCheck.
- Slash command extraction (`/fs-triage`) populates `transition.comment.command`.
- The `NormalizedEvent` conversion sets `source.system == "jira"` and `entity.kind == "work_item"`.
- The `HarnessRouter` evaluates harness CEL triggers against Jira-sourced events and selects the correct stage.
- Dispatch records are written with the correct stage and resource key.
- Bot events (author with `accountType: "app"`) are filtered out (testable via a negative scenario).

### What the test does not prove

- Actual GitHub Actions workflow dispatch (that requires a real GHA environment).
- Real Jira API pagination, rate limiting, or auth flows against a live instance.
- Lock contention between concurrent pollers (covered by unit tests in `poller_test.go`).
- End-to-end agent execution or artifact collection.
- Credential provisioning (`JIRA_TOKEN` as a repo secret).

### Dependencies for CI execution

1. **Mock Jira server implementation** — `pkg/behaviourtest/drivers/jiramock/server.go` or similar. This is the main new code.
2. **Local-filesystem harness config** — a variant of `givenCustomHarness` that writes to a temp dir instead of committing via SCM. Alternatively, the Jira poll steps can build the `.fullsend/` layout directly.
3. **`fullsend` binary** — built via `e2etest.BuildCLIBinary(t)` or `go build`, same as existing behaviour tests.
4. **No external services** — the `@requires:jira-mock` tag signals that the scenario runs entirely locally. No Jira instance, no GitHub API calls (beyond the enrolled-repo background step, which could be skipped or stubbed for pure-poll tests).
5. **Gherkin tag filtering** — the behaviour test runner must recognize `@requires:jira-mock` and skip these scenarios until the mock server is implemented. Once implemented, the tag can be dropped or kept as documentation.

### Incremental implementation order

1. Implement the mock Jira server (httptest-based, stateful).
2. Implement the new step definitions (`jirapoll.go`).
3. Wire registration into `registry.go`.
4. Remove the `@requires:jira-mock` skip tag once steps are functional.
5. Optionally: add a CI job that runs only `@requires:jira-mock` scenarios (fast, no GitHub API needed).

## OAuth 2.0 Client Credentials for Jira Cloud

### Problem

Personal API tokens on managed Atlassian Cloud instances (e.g. `stage-redhat.atlassian.net`) are often restricted by organization-level security policies. The token authenticates successfully (`/myself` returns 200) but cannot access project or issue data. This is an Atlassian org admin setting ("API token access") that blocks personal API tokens from reading project-scoped data while still allowing browser SSO access.

Service accounts like the one used by `konflux-ci/refinement` work because they have explicit project-level access or use a different auth method. The `release-engineering/sync2jira` project supports both Basic auth (PAT) and OAuth 2.0 client credentials (2LO) for exactly this reason.

### Solution: OAuth 2.0 Client Credentials Grant (2LO)

Add support for OAuth 2.0 two-legged (client credentials) auth as an alternative to Basic auth with API tokens. This is the standard Atlassian Cloud app auth mechanism.

#### How it works

1. Register an OAuth 2.0 app at https://developer.atlassian.com/console/myapps/
2. Configure scopes: `read:jira-work`, `read:jira-user` (and `write:jira-work` for entity properties)
3. Get `client_id` and `client_secret` from the app settings
4. At runtime, POST to `https://auth.atlassian.com/oauth/token`:
   ```
   grant_type=client_credentials
   client_id=<client_id>
   client_secret=<client_secret>
   ```
5. Receive an access token (typically 1-hour TTL)
6. Use `Authorization: Bearer <access_token>` on all Jira API calls
7. Cache the token and refresh ~5 minutes before expiry

#### Implementation plan

**Phase 1: Client auth layer** (`internal/forge/jira/`)

- Add `AuthMethod` type: `"basic"` (default, current behavior) or `"oauth2"`
- Add options: `WithOAuth2(clientID, clientSecret string)`, optionally `WithTokenURL(url string)` (default `https://auth.atlassian.com/oauth/token`)
- Add `oauth2TokenSource` that:
  - Holds `clientID`, `clientSecret`, `tokenURL`
  - Caches the token with a `sync.Mutex`
  - Refreshes when token is within 5 minutes of expiry
  - Returns cached token otherwise
- Modify `setAuth(req)` to use Bearer token when auth method is oauth2
- Token refresh errors should be surfaced clearly (not silently retried)

**Phase 2: CLI wiring** (`internal/cli/poll.go`)

New env vars / flags:
- `JIRA_AUTH_METHOD` — `basic` (default) or `oauth2`
- `JIRA_CLIENT_ID` — OAuth 2.0 client ID
- `JIRA_CLIENT_SECRET` — OAuth 2.0 client secret

When `JIRA_AUTH_METHOD=oauth2`, use `jira.WithOAuth2(clientID, clientSecret)` instead of `jira.WithEmail(email)`.

**Phase 3: Tests**

- Unit test for `oauth2TokenSource`: mock token endpoint, verify caching, verify refresh before expiry
- Unit test for `setAuth`: verify Bearer header when oauth2
- Integration test: httptest server acting as both token endpoint and Jira API

#### Credentials for functional test

To test against `stage-redhat.atlassian.net`:
1. Register an OAuth 2.0 app in the Atlassian developer console
2. Request access to the `stage-redhat.atlassian.net` site
3. Configure the required scopes
4. Store `JIRA_CLIENT_ID` and `JIRA_CLIENT_SECRET` as repo secrets
5. Set `JIRA_AUTH_METHOD=oauth2` in the workflow

#### Existing art

- `release-engineering/sync2jira` (`sync2jira/jira_auth.py`) — Python implementation of exactly this pattern: token caching, refresh, fallback. Good reference for the token exchange flow.
- `konflux-ci/refinement` — production Jira poller using Basic auth with a service account. May benefit from migration to OAuth 2.0 if the org further restricts API tokens.

### Open questions

- Does the `stage-redhat.atlassian.net` org admin allow OAuth 2.0 app registration, or is that also restricted?
- Do we need `write:jira-work` scope for entity property writes, or is there a narrower scope?
- Should the Jira client auto-detect auth method based on which credentials are provided (email → basic, client_id → oauth2)?

## Getting-started documentation

**Status:** Required before merge.

Add a user-facing guide at `docs/guides/user/jira-integration.md` covering:

- **Prerequisites:** Users must request an OAuth 2.0 service account (OpenID client credentials) from their Atlassian org admin to use the Jira integration. Personal API tokens are typically restricted on managed Atlassian Cloud instances and will not work.
- **Credential setup:** How to obtain `JIRA_CLIENT_ID` and `JIRA_CLIENT_SECRET`, configure scopes (`read:jira-work`, `read:jira-user`, `write:jira-work`), and store them as repo secrets.
- **Repo configuration:** Minimal `.fullsend/config.yaml` and harness YAML for Jira-triggered agents, including `trigger` CEL expressions that filter on `event.source.system == "jira"`.
- **Scheduled workflow:** Example `.github/workflows/fullsend-poll.yml` that runs `fullsend poll --input-driver jira-poll` on a cron schedule.
- **Troubleshooting:** Common failure modes (API token access restricted, wrong auth method, project not visible).

## References

- [ADR 0063 — Polling-based work discovery via dispatch drivers](../ADRs/0063-polling-based-work-discovery.md)
- [ADR 0054 — Require authorization on all agent dispatch paths](../ADRs/0054-require-authorization-on-all-agent-dispatch-paths.md)
- [ADR 0061 — Harness CEL triggers and fullsend dispatch drivers](../ADRs/0061-harness-cel-dispatch.md)
- [Jira poll adapter (NormalizedEvent extension)](../normative/normalized-event/v1/jira-poll-adapter.md)
- [NormalizedEvent v1 schema](../normative/normalized-event/v1/normalized-event.schema.json)
