# Plan: Repos Command Consolidation (9 → 5)

## Context

The `fullsend repos` subcommand group was built incrementally across 8
PRs (see [repos-management.md](repos-management.md)). The result is 9
commands with significant overlap:

- `repos diff` calls the same Go function as `repos sync --dry-run`.
- `repos add`/`repos remove` are manifest-editing wrappers around
  `repos install`/`repos uninstall`.
- `repos install` silently skips already-installed repos instead of
  verifying or repairing them.
- `repos sync` reconciles variable/secret drift — a subset of what a
  convergent `install` would do.
- `repos upgrade` bumps scaffold refs — another subset of convergent
  install behavior.

The per-repo path in `fullsend github setup` also overlaps: it
provisions the same variables, secrets, scaffold files, and
`.fullsend/config.yaml` that `repos install` does. The only feature
`fullsend github setup` has that repos management lacks is secret
reuse detection (checking if secrets already exist before requiring
CLI flags), which is exactly the convergence behavior this redesign
adds.

## Proposed Command Set

| Command | What it does |
|---------|-------------|
| `repos migrate` | Org discovery and migration import (unchanged) |
| `repos install` | Full convergence — provision, verify, repair, sync variables/secrets, update scaffold refs. Idempotent. |
| `repos uninstall` | Tear down and remove from manifest |
| `repos status` | Dashboard — current state vs desired state |
| `repos set-default` | Set or remove a forge-level default in `repos.yaml` |

### What gets dropped

| Old command | Absorbed by | Rationale |
|-------------|-------------|-----------|
| `repos add` | `repos install` | Install adds to manifest when repo is not present |
| `repos remove` | `repos uninstall` | Uninstall removes from manifest after teardown |
| `repos diff` | `repos install --dry-run` / `repos status` | 100% redundant with `sync --dry-run` (same function); status shows drift |
| `repos sync` | `repos install` | Variable/secret sync is part of convergence |
| `repos upgrade` | `repos install` | Ref updates are part of convergence |

## `repos install` — Convergence Scenarios

### Scenario 1: No manifest exists

Create a minimal manifest, add the specified repos, provision them.
User provides `--forge`, `--mint-url`, and inference parameters.

### Scenario 2: Repo not in manifest

Add it to the manifest (inheriting forge-level defaults or using
per-repo overrides), then provision: scaffold workflow files (PR or
direct push with `--direct`), set variables (`FULLSEND_MINT_URL`,
`FULLSEND_GCP_REGION` (install-time only), guard var), write secrets
(`FULLSEND_GCP_PROJECT_ID`, WIF provider).

### Scenario 3: Repo in manifest, not provisioned

Provision it. Same as scenario 2 but skip the manifest write.

### Scenario 4: Repo in manifest, provisioned, everything matches

Report healthy. No changes.

### Scenario 5: Repo in manifest, provisioned, variable drift

Update variables via API. No PR needed. Example: user changed
`mint_url` in the manifest.

### Scenario 6: Repo in manifest, provisioned, scaffold file drift

Open a PR (or direct push with `--direct`) to update workflow files.

### Scenario 7: Repo in manifest, provisioned, ref is behind latest

Update scaffold files to the latest release ref. PR or direct push.

### Scenario 8: Repo in manifest, provisioned, missing components

Partial installation — guard var exists but some variables, secrets,
or workflow files are missing. Repair by writing the missing pieces.

### Scenario 9: Repo in manifest, provisioned, secret reuse

Secrets already exist and user didn't pass inference flags. Reuse
existing secrets (check via `RepoSecretExists`), don't require the
flags on re-run. This closes the gap with `fullsend github setup`.

### Scenario 10: Dry run (`--dry-run`)

Walk through all scenarios but make no changes. Report what would
happen.

### Scenario 11: Bulk operation (no `--repo` filter)

Run convergence for every repo in the manifest, in parallel (bounded
by `--concurrency`).

### Scenario 12: GitLab repo

Not yet implemented. Return a clear error.

## `repos install` — Flags

| Flag | Purpose |
|------|---------|
| `--repo` | Filter to specific repos |
| `--dry-run` | Preview only |
| `--direct` | Push scaffold to default branch instead of PR |
| `--concurrency` | Parallel limit |
| `--inference-project` | GCP project ID (install-time) |
| `--inference-project-number` | GCP project number (install-time) |
| `--forge` | Required when creating a new manifest |
| `--mint-url` | Mint endpoint |

## `repos uninstall` — Behavior Change

Currently `repos uninstall` tears down without touching the manifest,
and `repos remove` removes from the manifest with an optional
`--uninstall` flag. The consolidated `repos uninstall` combines both.

### Flag refinement

| Flag | Teardown | Manifest removal |
|------|----------|------------------|
| *(default)* | Yes | Yes, but only if teardown succeeds |
| `--manifest-only` | No | Yes |
| `--uninstall-only` | Yes | No |

- **Default behavior (no flag):** tear down + remove from manifest,
  but only remove the manifest entry if teardown succeeds. Partial
  failures leave the entry so the user can retry.
- **`--manifest-only`:** remove the manifest entry without tearing
  down the installation. Use case: repo is already deleted/transferred,
  or the entry was added but never successfully installed.
- **`--uninstall-only`:** tear down the installation but keep the
  manifest entry. Use case: temporary teardown with intent to reinstall
  later without re-specifying per-repo overrides. (Replaces the earlier
  `--keep-in-manifest` name for clarity.)

## `repos status` — Absorbs `repos diff`

Status already reports drift. The detailed old/new value output that
`repos diff` provided can be incorporated as a `--changes` or
`--verbose` flag on status if needed.

## Relationship to `fullsend github setup`

Once `repos install` gains secret reuse detection (scenario 9), the
per-repo path in `fullsend github setup` becomes fully redundant.
Both paths provision the same resources: scaffold files,
`.fullsend/config.yaml`, variables, and secrets. A future follow-up
can deprecate the per-repo path in `fullsend github setup` in favor
of `repos install`.

## Out of Scope

- Agent roles (`--agents` flag): not part of this consolidation. Can
  be added as a follow-up once the command structure stabilizes.
- GitLab install implementation: tracked separately.
- Deprecation of `fullsend github setup` per-repo path: separate
  issue after this ships.
