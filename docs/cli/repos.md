---
sidebar_label: fullsend repos
---

# fullsend repos

Manage per-repo installations across multiple orgs via a declarative `repos.yaml` manifest. Compare the manifest's desired state against actual forge state and report installation status and configuration drift.

## Global flags

These flags are inherited by all `repos` subcommands:

| Flag | Default | Description |
|------|---------|-------------|
| `--gitlab-token` | | GitLab personal or project access token (overrides `GITLAB_TOKEN` env var) |

## Commands

| Command | Description |
|---------|-------------|
| `fullsend repos init <org\|owner/repo>` | Generate a repos.yaml manifest by discovering existing installations |
| `fullsend repos install [repos...]` | Converge repos to the desired state defined in a manifest |
| `fullsend repos uninstall <repos...>` | Tear down fullsend from repos and remove from manifest |
| `fullsend repos status` | Compare manifest against actual repo state |

## `repos init`

Discovers existing fullsend installations (per-repo and per-org) and generates a `repos.yaml` manifest reflecting their current state. Supports greenfield onboarding and migration from existing installations.

```bash
fullsend repos init <org> --forge github --all --mint-url <MINT_URL>
```

Single-repo mode:

```bash
fullsend repos init <owner/repo> --forge github
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--output`, `-o` | `repos.yaml` | Output path (use `-` for stdout) |
| `--repos` | | Comma-separated list of repos to include |
| `--all` | `false` | Include all eligible repos without prompting |
| `--mint-url` | | Token mint Cloud Run endpoint URL |
| `--inference-region` | | GCP region for inference (default: `us-central1`) |
| `--fullsend-ref` | | Pin the fullsend workflow ref (e.g. `v0.42.0`) |
| `--concurrency` | `8` | Max parallel API calls (capped at 64) |
| `--forge` | **(required)** | Forge type for discovered repos (`github` or `gitlab`) |
| `--forge-url` | | Forge instance URL (required for `gitlab`; defaults to `https://github.com` for `github`) |
| `--force` | `false` | Overwrite output file if it already exists |

### Discovery

The command discovers repos by checking:

1. **Per-repo guard variable** (`FULLSEND_PER_REPO_INSTALL`) — identifies per-repo installations
2. **Per-org config enrollment** (`config.yaml` in `.fullsend` repo) — identifies per-org installations; if no mint URL is set in the org config, falls back to the `FULLSEND_MINT_URL` org-level Actions variable
3. **Workflow ref** — extracts the `@ref` from scaffold shim workflow files

### Defaults computation

Values for `fullsend_ref` and `inference_region` in the `forge.github` section are computed using the mode (most common value) across discovered repos.

### Selection modes

For org targets, one of `--all` or `--repos` is required:

- `--all`: include all discovered repos
- `--repos`: include only the specified repos (comma-separated `owner/repo` names)

## `repos install`

Converge repos to the desired state defined in a manifest. This is the primary command for managing per-repo installations — it handles adding repos to the manifest, provisioning new repos, syncing variable drift, and upgrading scaffold refs.

Runs in three phases:

1. **Manifest add** — repos specified as positional arguments that are not already in the manifest are added (requires `--forge`). Per-repo overrides (`--inference-region`, `--fullsend-ref`, `--mint-url`, `--allowed-remote-resources`) are written to the manifest entry.
2. **Provision** — repos in the manifest that are not yet provisioned are installed (scaffold files, variables, secrets). Repos with a guard variable set but other components missing are repaired automatically.
3. **Convergence** — repos that are already installed are checked for variable drift (synced automatically) and scaffold ref drift (upgraded automatically).

> **Note:** GCP infrastructure (WIF pools/providers, mint registration) must be
> provisioned separately via `inference provision` and `mint enroll` before
> running `repos install`. The `--inference-project-number` flag (numeric GCP
> project number) is required for GitHub repos — it is used to compute WIF
> provider resource names deterministically. The `--inference-project` flag
> (GCP project ID) is also required for GitHub repos and is written as the
> `FULLSEND_GCP_PROJECT_ID` repo secret.

```bash
fullsend repos install -f repos.yaml
fullsend repos install --dry-run
fullsend repos install acme/api acme/web
fullsend repos install "acme/*" --direct --concurrency 8
fullsend repos install acme/new-repo --forge github --direct
```

When repos are specified as positional arguments, only those repos are processed. Glob patterns (e.g. `acme/*`) are matched against manifest entries. When no repos are specified, all manifest repos are converged.

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-f`, `--manifest` | `repos.yaml` | Path or URL to repos.yaml manifest |
| `--dry-run` | `false` | Preview what would change without making modifications |
| `--concurrency` | `4` | Max parallel operations (1-32) |
| `--roles` | `triage,coder,review,fix,retro,prioritize` | Agent roles to install |
| `--direct` | `false` | Push scaffold directly to default branch (skip PR) |
| `--inference-project` | | GCP project ID for inference (written as `FULLSEND_GCP_PROJECT_ID` secret; required for GitHub repos) |
| `--inference-project-number` | | Numeric GCP project number for WIF provider computation (required for GitHub repos) |
| `--forge` | | Forge type for new repos (`github` or `gitlab`). Required when adding repos not already in the manifest; falls back to `defaults.forge` if set. |
| `--inference-region` | | Per-repo GCP inference region override |
| `--fullsend-ref` | | Per-repo fullsend workflow ref override |
| `--mint-url` | | Per-repo mint URL override |
| `--allowed-remote-resources` | | Per-repo allowed remote resources override |

### Common workflows

Converge all repos from a manifest (provision new, sync drift, upgrade refs):

```bash
fullsend repos install -f repos.yaml
```

Preview changes without modifying infrastructure:

```bash
fullsend repos install -f repos.yaml --dry-run
```

Add a new repo to the manifest and install it:

```bash
fullsend repos install acme/new-repo --forge github --direct
```

Install specific repos:

```bash
fullsend repos install acme/api acme/web
```

## `repos status`

Read-only comparison of the `repos.yaml` manifest against actual forge state. Reports installation status and configuration drift for each repo.

```bash
fullsend repos status
fullsend repos status -f path/to/repos.yaml
fullsend repos status --repo acme/api --repo acme/web
fullsend repos status --repo "acme/*" --json
```

### Flags

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--manifest` | `-f` | `repos.yaml` | Path or HTTPS URL to manifest file |
| `--repo` | | | Filter to specific repos (repeatable, supports globs) |
| `--json` | | `false` | Emit JSON output instead of table |
| `--concurrency` | | `8` | Max parallel API calls |

### Output

**Table output** (default) shows per-repo status with columns:

- **REPO** — `owner/repo` name
- **REF** — Current workflow ref (`@v2.3.0`, `@main`, etc.)
- **STATUS** — `installed`, `not installed`, or `error`
- **DRIFT** — Fields that differ from the manifest, or `none`

**JSON output** (`--json`) returns the full `StatusResult` object with per-repo details and aggregate summary counts.

### Exit codes

The command returns a non-zero exit code when any repo has drift, is not installed, or encountered an error. This makes it suitable for CI checks.

### Authentication

Requires a GitHub token via `GH_TOKEN`, `GITHUB_TOKEN`, or `gh auth token`. For GitLab repos, set the `GITLAB_TOKEN` environment variable or pass `--gitlab-token` to the `repos` command group.

## `repos uninstall`

Tear down fullsend from the specified repos and remove them from the manifest. By default, the command tears down first (deleting workflow files, variables, and secrets), then removes successfully-torn-down repos from the manifest. Partial failures leave the manifest entry intact so the user can retry.

GCP WIF cleanup is handled separately via `inference deprovision`.

When multiple repos are targeted (via globs or explicit bulk lists), the command prompts for confirmation unless `--yes` is set.

```bash
fullsend repos uninstall acme/old-api
fullsend repos uninstall "acme/*" --yes
fullsend repos uninstall acme/old-api --dry-run
fullsend repos uninstall acme/old-api --manifest-only
fullsend repos uninstall acme/old-api --uninstall-only
```

### Modes

| Flag | Teardown | Manifest removal |
|------|----------|------------------|
| *(default)* | Yes | Yes (only if teardown succeeds) |
| `--manifest-only` | No | Yes |
| `--uninstall-only` | Yes | No |

- **Default:** tear down + remove from manifest. Only repos whose teardown succeeds are removed from the manifest.
- **`--manifest-only`:** remove the manifest entry without tearing down the installation. Use when the repo is already deleted/transferred or was never successfully installed.
- **`--uninstall-only`:** tear down the installation but keep the manifest entry. Use for temporary teardown with intent to reinstall later.

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-f`, `--manifest` | `repos.yaml` | Path or URL to repos.yaml manifest |
| `--dry-run` | `false` | Preview what would be uninstalled without making changes |
| `--yes` | `false` | Skip confirmation prompt when multiple repos are targeted |
| `--concurrency` | `4` | Max parallel operations (1-32) |
| `--manifest-only` | `false` | Remove from manifest without tearing down |
| `--uninstall-only` | `false` | Tear down without removing from manifest |

## See also

- [Getting Started](../guides/getting-started/) — Standard per-repo installation
- [Operations](../guides/getting-started/operations.md) — Day-2 administration
- [CLI Internals](../guides/dev/cli-internals.md) — Command structure and implementation details
