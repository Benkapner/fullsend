# Forge Abstraction

All git forge operations (GitHub API calls, PR comments, issue creation, workflow dispatch, etc.) **must** go through the `forge.Client` interface defined in `internal/forge/forge.go`. This is a fundamental architectural rule — the codebase supports multiple forges (GitHub, GitLab, Forgejo) and direct coupling to any single forge breaks the abstraction.

**Prohibited outside `internal/forge/github/`:**

- `exec.Command("gh", ...)` — shelling out to the GitHub CLI
- Direct GitHub REST or GraphQL API calls (e.g., raw `net/http` to `api.github.com`)
- Any other forge-specific operation that bypasses `forge.Client`

**Where forge-specific code belongs:** Only the `internal/forge/github/` package (the GitHub implementation of `forge.Client`) should contain GitHub-specific logic. All other packages must use the `forge.Client` interface, which is injected as a dependency.

**When writing code:** If you need a forge operation that `forge.Client` does not yet support, add a new method to the interface and implement it in the GitHub client — do not work around the interface.

**When reviewing PRs:** Flag any direct `exec.Command("gh", ...)`, raw GitHub API calls, or other forge-specific operations outside `internal/forge/github/` as a medium-severity or higher finding. This is an architectural violation, not a style preference.

**Composite action (`action.yml`):** The forge abstraction extends to `action.yml` bash scripts. New GitHub API operations in action steps should be implemented as `fullsend` CLI subcommands (under `internal/cli/`) that use `forge.Client`, not as inline `gh api` calls. Existing `gh api` calls in `action.yml` that predate this rule are grandfathered but should be migrated when touched.

## Security considerations for destructive operations

Forge methods that modify or destroy resources — closing PRs/MRs, deleting branches/refs, merging, force-pushing — require ownership or authorization verification at the **call site**, not inside the forge method itself. The forge method is a thin transport layer; it should not encode policy about who is allowed to act. The caller has the context to decide whether the operation is safe.

**1. Verify ownership before acting.** Before invoking a destructive forge operation, the caller must verify that the authenticated user has a legitimate relationship to the target resource. For example, before closing a PR, confirm the PR was authored by the authenticated user (or by a known bot identity the caller controls). Do not assume that the ability to call a forge method implies authorization to use it on any resource.

**2. Prefer fail-closed defaults.** When ownership or authorization data is missing or ambiguous, skip the operation rather than proceeding. If an API response omits the author field, treat it as "not ours" and do not close the PR. A missed cleanup is recoverable; silently closing someone else's PR is not.

**3. Guard against predictable-name attacks.** Well-known branch names (e.g., `fullsend/onboard`, `fullsend/scaffold-install`) are predictable. An external contributor could open a PR on one of these branches before the automation runs. Without an ownership check, the automation would close a legitimate external PR. Always filter by author or other ownership signal before acting on resources identified by predictable names.

**Positive example:** `closeStaleScaffoldPRs` in `internal/layers/commit.go` demonstrates all three principles. It closes stale scaffold PRs only when the PR author matches the authenticated user (`strings.EqualFold(pr.Author, authenticatedUser)`), skips PRs with an empty author field (fail-closed), and operates on well-known scaffold branch names with full awareness that external actors could create PRs on those paths.

**When reviewing PRs:** Flag any new destructive forge operation (close, delete, merge, force-push via `forge.Client`) that lacks ownership or authorization verification at the call site. This is a medium-severity or higher finding — the same class as a forge abstraction violation.
