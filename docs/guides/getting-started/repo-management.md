---
sidebar_position: 5
---

# Repo Management

Manage per-repo fullsend installations at scale using a declarative
`repos.yaml` manifest. The `fullsend repos` command group provides bulk
install, status checking, drift detection, configuration sync, and
version upgrades across multiple repos and GitHub orgs.

**Target audience:** Platform administrators (SRE/DevOps) managing
fullsend across an organization. Individual repo owners should use
`fullsend github setup` for single-repo installation (see
[Configuring GitHub](configuring-github.md)).

## Prerequisites

- **fullsend CLI** installed (see [releases](https://github.com/fullsend-ai/fullsend/releases))
- **GitHub access** — admin or write access to the target repositories
- **`gh` CLI** authenticated with the required OAuth scopes (see [OAuth scope reference](../infrastructure/advanced-setup.md#oauth-scope-reference))
- **GCP access** — for WIF provisioning and mint operations (see [Mint administration](../infrastructure/mint-administration.md))
- **Mint enrollment** — your orgs or repos must be enrolled in a fullsend token mint service (see [Getting Started](README.md))

## Getting started

### Creating a manifest

Generate a `repos.yaml` manifest by discovering existing installations:

```bash
fullsend repos init <org> --forge github --all \
  --mint-project <GCP_PROJECT> \
  --inference-project <GCP_PROJECT>
```

For a single repo:

```bash
fullsend repos init <owner/repo> --forge github --mint-project <GCP_PROJECT>
```

Instead of `--all`, specify a subset of repos with `--repos`:

```bash
fullsend repos init acme --forge github --repos acme/api,acme/web \
  --mint-project <GCP_PROJECT>
```

`--repos` and `--all` are mutually exclusive.

If a `repos.yaml` file already exists, pass `--force` to overwrite it:

```bash
fullsend repos init <org> --forge github --all --force --mint-project <GCP_PROJECT>
```

The command discovers per-repo and per-org installations, extracts
current configuration (WIF provider, workflow ref, mint URL), and writes
a manifest. Default values for `fullsend_ref` and `inference_region` are
computed using the mode (most common value) across discovered repos.

### Multi-forge manifests

Every repo entry in the manifest must declare its forge (`github` or
`gitlab`). Set `defaults.forge` to avoid repeating the forge on every
entry. Per-repo overrides are supported for mixed-forge manifests:

```yaml
version: 1
forge:
  github:
    mint_url: https://mint.example.com
    mint_project: my-project
    mint_region: us-central1
  gitlab:
    url: https://gitlab.example.com
defaults:
  forge: github
  fullsend_ref: v2.5.0
repos:
  - acme/api-server            # inherits forge: github from defaults
  - acme/web-frontend
  - repo: gitlab-group/project # per-entry override
    forge: gitlab
```

All repos under the same owner must use the same forge. A GitHub org
and a GitLab group with the same name are different entities, and
mixing forges under one owner would route API calls incorrectly.

For GitLab repos, set the `GITLAB_TOKEN` environment variable or pass
`--gitlab-token` to `fullsend repos` subcommands. Self-hosted GitLab
instances require `forge.gitlab.url` in the manifest (e.g.
`https://gitlab.example.com`). The `GITLAB_API_URL` environment variable
is kept as a fallback for callers without a manifest.

See `fullsend repos init --help` or the [CLI reference](../../cli/repos.md)
for all flags.

### Manifest paths and URLs

The `-f`/`--manifest` flag accepts either a local file path or an HTTPS
URL. Remote manifests are fetched with a 30-second timeout and a 1 MB
size limit. Most `repos` subcommands support this — see the
[CLI reference](../../cli/repos.md) for details.

```bash
fullsend repos status -f https://example.com/manifests/repos.yaml
```

### Concurrency

Most `repos` subcommands accept a `--concurrency` flag to control the
number of parallel API calls or operations. Defaults vary by command
(typically 4 for write operations, 8 for read-only operations). See the
[CLI reference](../../cli/repos.md) for per-command defaults and limits.

### Installing repos

Install fullsend on repos defined in the manifest that are not yet
installed:

```bash
fullsend repos install -f repos.yaml
```

Install runs in three phases:

1. **Parallel discovery** — check which repos are already installed by
   verifying the guard variable and all installation components
   (workflow file, variables, and secrets). Repos with a guard
   variable set but other components missing are flagged for
   partial-installation repair (see below).
2. **Sequential WIF** — register each unique org in the token mint
   (`EnsureOrgInMint`), then provision per-repo WIF infrastructure
   (`ProvisionWIF` + `RegisterPerRepoWIF`). These operations modify
   shared GCP state and are not concurrent-safe.
3. **Parallel scaffold** — commit scaffold files and write
   variables/secrets

> **Partial installation repair:** If a previous install was interrupted
> (guard variable set but workflow, variables, or secrets missing),
> `repos install` detects the incomplete state and repairs it
> automatically. No manual cleanup is needed — re-running install
> resumes where the previous run left off.

> **Note:** When your token does not have direct push access to a target
> repository, the install command creates a fork and submits the scaffold
> PR from the fork. To avoid fork-based delivery, ensure you have write
> (or admin) access to the target repositories before running install.

Preview what would be installed without making changes:

```bash
fullsend repos install -f repos.yaml --dry-run
```

Install specific repos (when orgs are already registered in the mint):

```bash
fullsend repos install acme/api acme/web --skip-mint-check
```

Glob patterns are supported:

```bash
fullsend repos install "acme/*" --direct --concurrency 8
```

Install a subset of agent roles (defaults to
`triage,coder,review,fix,retro,prioritize`):

```bash
fullsend repos install -f repos.yaml --roles triage,coder,review
```

## Day-2 operations

### Checking installation status

Compare the manifest against actual repo state:

```bash
fullsend repos status -f repos.yaml
```

Filter to specific repos:

```bash
fullsend repos status --repo acme/api --repo acme/web
```

The command reports per-repo status (installed, not installed, error) and
any configuration drift. Returns a non-zero exit code when drift or
errors exist, making it suitable for CI checks.

Use `--json` for machine-readable output:

```bash
fullsend repos status -f repos.yaml --json
```

### Detecting configuration drift

Show configuration differences between the manifest and actual forge
state:

```bash
fullsend repos diff -f repos.yaml
```

The `diff` command checks **variables and managed secrets only** — it
compares `FULLSEND_MINT_URL` and `FULLSEND_GCP_REGION` variables against
the manifest and checks that `FULLSEND_GCP_PROJECT_ID` (the only managed
secret) exists. Because GitHub secrets are not readable, diff can only
detect missing secrets — it cannot detect value mismatches. The WIF
provider secret (`FULLSEND_GCP_WIF_PROVIDER`) is write-once at install
time and is not managed by diff/sync. Diff does not check the scaffold
workflow ref (`@ref`). To detect ref drift, use `repos status` (which
includes the ref in its output) or run `repos upgrade --dry-run` to
preview which repos would be upgraded.

Use `--json` for machine-readable output:

```bash
fullsend repos diff --json
```

Returns a non-zero exit code when drift exists.

### Reconciling drift

Apply manifest values to repos where drift was detected:

```bash
fullsend repos sync -f repos.yaml
```

Preview changes first:

```bash
fullsend repos sync --dry-run
```

Use `--json` for machine-readable output:

```bash
fullsend repos sync -f repos.yaml --json
```

Sync reconciles variables and secrets. It does **not** touch the scaffold
shim version (`@ref`) — use `repos upgrade` for that.

### Adding repos

Add repos to the manifest and optionally install them:

```bash
fullsend repos add acme/new-api acme/new-web --forge github
fullsend repos add acme/new-api --forge github --install --direct
```

Preview what would be added:

```bash
fullsend repos add acme/new-api --forge github --dry-run
```

Specify which agent roles to install (defaults to
`triage,coder,review,fix,retro,prioritize`):

```bash
fullsend repos add acme/new-api --forge github --install --roles triage,coder,review
```

### Removing repos

Remove repos from the manifest:

```bash
fullsend repos remove acme/old-api
```

When targeting multiple repos (via globs or bulk lists), the command
prompts for confirmation:

```bash
fullsend repos remove "acme/*"
```

In non-interactive environments (CI, piped stdin), pass `--yes` to skip
the confirmation prompt:

```bash
fullsend repos remove "acme/*" --yes
```

To also tear down fullsend from the repos (delete workflow, variables,
secrets, and WIF) before removing them from the manifest:

```bash
fullsend repos remove acme/old-api --uninstall
fullsend repos remove acme/old-api --uninstall --skip-wif-cleanup
```

### Rolling out a new fullsend version

To upgrade the scaffold workflow ref across all manifest repos:

1. Update the `fullsend_ref` in `repos.yaml` to the new version.

2. Run the upgrade:

   ```bash
   fullsend repos upgrade -f repos.yaml
   ```

   Mint verification runs automatically as a pre-flight step. If the
   mint deployment URL does not match the manifest, the upgrade fails
   with a clear error. Pass `--skip-mint-check` to bypass:

   ```bash
   fullsend repos upgrade -f repos.yaml --skip-mint-check
   ```

3. Review and merge the scaffold PRs in each repo.

Preview what would change without modifying repos:

```bash
fullsend repos upgrade --dry-run
```

Override the manifest ref for a one-off upgrade:

```bash
fullsend repos upgrade --ref v2.4.0
```

Upgrade specific repos:

```bash
fullsend repos upgrade acme/api acme/web
```

Push the upgrade directly to the default branch instead of creating a PR:

```bash
fullsend repos upgrade -f repos.yaml --direct
```

Floating refs (`latest`, `main`, `v0`) are skipped. Downgrades are
blocked unless `--force` is set.

### Verifying mint configuration

The standalone `repos upgrade-mint` command verifies the token mint
deployment matches the manifest without triggering an upgrade:

```bash
fullsend repos upgrade-mint -f repos.yaml
```

Since `repos upgrade` now runs this verification automatically, this
command is primarily useful for one-off checks.

## Migrating from per-org mode to manifest management

Organizations migrating from per-org mode
([ADR 0044](../../ADRs/0044-deprecate-per-org-installation-mode.md)) to
per-repo manifest management can use the following workflow.

### Step 1: Generate a manifest from existing installations

```bash
fullsend repos init <org> --forge github --all \
  --mint-project <GCP_PROJECT> \
  --inference-project <GCP_PROJECT>
```

This discovers all enrolled repos and writes `repos.yaml`.

### Step 2: Install per-repo infrastructure

```bash
fullsend repos install -f repos.yaml
```

Each repo gets its own WIF provider, variables, secrets, and scaffold
workflow.

### Step 3: Verify per-repo installations

```bash
fullsend repos status -f repos.yaml
```

Confirm all repos show `installed` status with no drift.

### Step 4: Uninstall the per-org configuration

```bash
fullsend github uninstall "$ORG_NAME"
```

This removes the `.fullsend` config repo, org-level variables, and org
secrets. It also lists any installed GitHub Apps and provides links for
manual deletion.

> **Warning:** Do **not** delete the GitHub Apps listed by the uninstall
> command if you are migrating to per-repo mode. The agents still need
> these apps to function. The apps are shared between per-org and
> per-repo installations — only delete them if you are fully removing
> fullsend from the organization.

In non-interactive environments, pass `--yolo` to skip the confirmation
prompt:

```bash
fullsend github uninstall "$ORG_NAME" --yolo
```

> **Note:** `fullsend github unenroll` is only needed when keeping some
> repos on per-org mode while migrating others to per-repo. When
> migrating all repos, skip unenroll and go directly to uninstall.

## Tearing down

### Removing individual repos

Remove a repo from the manifest and tear down its fullsend installation:

```bash
fullsend repos remove acme/old-api --uninstall
```

Or tear down without modifying the manifest:

```bash
fullsend repos uninstall acme/old-api
```

When targeting multiple repos, a confirmation prompt appears. In
non-interactive environments (CI, piped stdin), pass `--yes`:

```bash
fullsend repos uninstall "acme/*" --yes
```

### Full teardown

To completely remove fullsend from all manifest repos and GCP
infrastructure, coordinate between roles:

| Step | Role | Command |
|------|------|---------|
| 1 | Platform Admin | `fullsend repos uninstall "org/*" --yes` |
| 2 | GCP Admin (Inference) | `fullsend inference deprovision <org>` |
| 3 | GCP Admin (Mint) | `fullsend mint unenroll <org>` |

Each `fullsend` command that prompts for confirmation accepts a skip
flag: `--yes` for `repos` commands, `--yolo` for `github` and `mint`
commands.

## See also

- [Operations](operations.md) — Day-2 per-repo administration and standalone commands
- [Per-Org Mode](org-mode.md) — Organization-mode installation (planned deprecation)
- [CLI Reference: fullsend repos](../../cli/repos.md) — Full flag and subcommand reference
- [Mint administration](../infrastructure/mint-administration.md) — Token mint deployment and management
- [ADR 0057](../../ADRs/0057-repos-management.md) — Design decision for repos management
