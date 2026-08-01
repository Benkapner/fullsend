# Implementation Plan: `fullsend repos init`

## Context

[ADR 0057](../ADRs/0057-repos-management.md) defines a declarative
`repos.yaml` manifest as the single source of truth for multi-repo
management. Without `repos init`, operators must author this manifest
from scratch.

The `repos init` command generates a manifest for both scenarios:

- **Greenfield onboarding:** The org has no fullsend installations.
  The operator selects which repos to include (interactively or via
  flags) and the command generates a manifest with default config.
  Running `repos install` afterward provisions everything.
- **Migration from existing installations:** The org has per-repo
  and/or per-org installations. The command discovers their state
  (variables, workflow refs, enrollment lists) and generates a manifest
  that reflects current reality. Existing installations are
  pre-selected in interactive mode. Per-org enrolled repos are included
  so they can be converted to per-repo via `repos install`.

In both cases the output is the same artifact — a `repos.yaml`
manifest. The per-repo and per-org discovery logic is needed only
during the transition period. Once all installations are managed via
`repos.yaml` and the per-org mode is removed (ADR 0044), the
discovery checks become dead code and can be removed, leaving
`repos init` as a simple repo-selection and manifest-generation tool.

This plan can be implemented in parallel with PRs 4–8 of the
[repos management plan](repos-management.md). It shares two
dependencies with that plan:

- **PR 2** (manifest parser): provides `Manifest` types for output.
- **PR 3** (forge methods): provides `ListRepoVariables` for efficient
  per-repo discovery.

### Dependency graph

```
PR 2 (manifest parser) ──┬──> repos init
                          │
PR 3 (forge methods) ─────┘
```

---

## PR: `fullsend repos init` (manifest bootstrapping)

**Scope:** New CLI command. Read-only discovery + manifest generation.

### `internal/cli/repos.go` (modify)

Add `newReposInitCmd()` to the repos subcommand group.

Flags:

- `--output` / `-o` (string, default `repos.yaml`): output path.
  Use `-` for stdout.
- `--repos` (string, comma-separated): explicit list of repo names to
  include. Skips interactive selection.
- `--all` (bool): include all eligible repos without prompting.
- `--forge` (string, **required**): forge type (`github` or `gitlab`).
- `--forge-url` (string): forge instance URL. Required for `gitlab`;
  defaults to `https://github.com` for `github`.
- `--mint-url` (string): token mint Cloud Run endpoint URL for the
  `forge.github.mint_url` field.
- `--inference-project` (string): default GCP project for inference.
- `--inference-region` (string): GCP region for inference (default:
  `us-central1`).
- `--inference-project-number` (string): GCP project number for the
  `forge.github.inference_project_number` field.
- `--fullsend-ref` (string): pin the fullsend workflow ref (e.g.
  `v0.42.0`).
- `--concurrency` (int, default 8): max parallel API calls.

Positional argument: `<target>` (org name or `owner/repo`). Detection
uses the same `strings.Contains(arg, "/")` pattern as
`fullsend github setup` (`internal/cli/admin.go:284`).

### `internal/repos/init.go` (new)

```go
type InitConfig struct {
    Target                 string   // org name or owner/repo
    Repos                  []string // explicit repo names (nil = interactive/all)
    All                    bool     // include all repos without prompting
    Forge                  string   // forge type ("github" or "gitlab")
    ForgeURL               string   // forge instance URL
    InferenceProject       string
    InferenceProjectNumber string
    MaxConcurrency         int
}

type DiscoveredRepo struct {
    Owner           string
    Repo            string
    Source          string // "per-repo", "per-org", or "new"
    MintURL         string
    InferenceRegion string
    FullsendRef     string
}

type InitResult struct {
    Manifest     *Manifest
    PerRepoCount int
    PerOrgCount  int
    NewCount     int
    TODOs        []string // fields requiring manual attention
}

func Init(ctx context.Context, cfg InitConfig,
    clients ForgeClientFactory,
    selectRepos RepoSelectFunc,
    progress ProgressFunc) (*InitResult, error)
```

#### Repo selection

`RepoSelectFunc` is a callback the CLI layer provides to handle
interactive selection. The init logic calls it with the full repo
list and each repo's discovered status (per-repo / per-org / new).
The callback returns the subset the operator selected.

```go
type RepoCandidate struct {
    Owner     string
    Repo      string
    Status    string // "per-repo", "per-org", "new"
    Ref       string // discovered ref, empty for new
}

type RepoSelectFunc func(candidates []RepoCandidate) ([]string, error)
```

Selection modes:

- **Explicit** (`cfg.Repos` non-nil): skip selection, use provided
  list. Validate that all names exist in the org.
- **All** (`cfg.All`): skip selection, include everything returned
  by `ListOrgRepos`.
- **Interactive** (default): call `selectRepos` with candidates.
  Repos with existing installations are pre-selected. The CLI layer
  renders this as a new multi-select checklist (arrow keys, space to
  toggle, enter to confirm). This is a new UX pattern — the existing
  `promptEnrollment` in `fullsend github setup`
  (`internal/cli/admin.go:2070`) is a binary all-or-none prompt, not
  a per-repo selector.

For single-repo targets (`owner/repo`), selection is skipped — the
manifest contains one entry.

#### Discovery algorithm

**Org-level** (target has no `/`):

1. Call `ListOrgRepos(ctx, org, false)` to enumerate eligible repos (the `includePrivate` parameter is `false` for per-org mode).
2. Check for per-org config repo (`{org}/.fullsend`):
   - Read `config.yaml` via `GetFileContent`
     (`internal/forge/forge.go:199`).
   - Parse with `config.ParseOrgConfig()`
     (`internal/config/config.go:147`).
   - Extract: enrollment map (`Repos` field,
     `internal/config/config.go:79`), mint URL
     (`Dispatch.MintURL`, `internal/config/config.go:23`),
     `AllowedRemoteResources`.
3. For each repo (parallel, bounded by `MaxConcurrency`):
   - Call `ListRepoVariables(ctx, owner, repo)` to read guard
     variable, mint URL, and region in one API call.
   - If `FULLSEND_PER_REPO_INSTALL == "true"`
     (`forge.PerRepoGuardVar`, `internal/forge/forge.go:17`):
     - Read workflow file via `GetFileContent` using forge-specific
       paths from `ForgeConfig.WorkflowPaths`.
     - Extract ref using `ForgeConfig.WorkflowRefPattern`.
     - Mark `source: per-repo`.
   - Else if repo appears in per-org enrollment with
     `enabled: true`:
     - Use mint URL and config from per-org `config.yaml`. If no
       mint URL is set in `config.yaml`, fall back to reading the
       `FULLSEND_MINT_URL` org-level variable via `GetOrgVariable`.
     - Read the per-org shim workflow file and extract `@ref` from
       the `uses:` line (same as per-repo discovery). Do not use
       `config.DefaultUpstreamRef` — it is `v0`, a major-version
       floating tag for workflow-call resolution, not a concrete
       release version. If no workflow file exists, omit the ref
       and let it inherit from `forge.github.fullsend_ref`.
     - Mark `source: per-org`.
   - Otherwise:
     - Mark `source: new` (not yet installed).
4. Present candidates to repo selection (explicit / all /
   interactive).
5. Run manifest generation on the selected subset.

**Single-repo** (target has `/`):

1. Check guard variable on the repo.
2. If per-repo: read variables and workflow file.
3. If not: check org config repo (`{owner}/.fullsend`,
   `forge.ConfigRepoName`, `internal/forge/forge.go:13`) for
   enrollment.
4. If neither: mark as `source: new`.
5. Generate single-entry manifest.

#### Manifest generation

```go
func buildManifest(repos []DiscoveredRepo,
    cfg InitConfig) (*Manifest, []string)
```

1. **Compute `forge.github:` block** (only when `--forge=github`):
   - `mint_url`: from discovered `FULLSEND_MINT_URL` (should be uniform
     across repos sharing a mint). If repos report different mint
     URLs, use the most common and add a TODO. For greenfield (no
     discovered URLs), leave as TODO (Cloud Run URLs contain a random
     hash and cannot be derived from the project name alone).

2. **Compute `forge.github:` infrastructure fields** by finding the
   mode (most common value) across discovered repos. For greenfield
   repos (`source: new`) with no discovered values, use the CLI
   version as `fullsend_ref` and flags:
   - `fullsend_ref`: `--fullsend-ref` flag > mode of discovered refs > CLI version.
   - `inference_project`: from `--inference-project` flag, or TODO.
   - `inference_project_number`: from `--inference-project-number` flag, or TODO.
   - `defaults.allowed_remote_resources`: from per-org config if present.

3. **Build repo entries:**
   - All repos use simple string entries (`acme-corp/api-server`)
     or object entries with a `forge` override.
   - Per-org enrolled repos are included as normal entries. A YAML
     comment group header notes they are currently per-org and will
     be converted to per-repo on `repos install`.
   - New repos (not yet installed) are included as normal entries.

4. **Return TODO list** for fields that could not be discovered (e.g.,
   `inference_project` when no flag provided, `inference_project_number` when
   omitted).

**Secret limitation:** `FULLSEND_GCP_PROJECT_ID` and
`FULLSEND_GCP_WIF_PROVIDER` are GitHub secrets — values are not
readable via the API. The `inference_project` field must come from
`--inference-project` flag, from the per-org config, or be left as a
TODO. The WIF provider is not stored in the manifest (it is
provisioned by `repos install`).

#### Ref extraction

```go
// Patterns and extraction are now forge-specific via ForgeConfig.
// GitHub uses a `uses:` action ref pattern; GitLab uses a `ref:` include pattern.
// See internal/repos/forge_config.go for the per-forge definitions.

func extractWorkflowRef(content []byte, fc ForgeConfig) string
```

The ref pattern is now part of `ForgeConfig` rather than a standalone
regex, allowing each forge to define its own extraction logic.

#### Manifest serialization

```go
func (m *Manifest) Marshal() ([]byte, error)
```

Add `Marshal()` to the `Manifest` type defined in PR 2. Uses
`yaml.Marshal` with a descriptive header comment:

```
# Generated by fullsend repos init on <date>.
# Review and adjust before running fullsend repos install.
```

### `internal/repos/init_test.go` (new)

- **Greenfield:** Org with no installations, `--repos` flag → manifest
  with all repos as new entries, defaults from flags/CLI version.
- **Greenfield:** Org with no installations, `--all` flag → all repos
  included.
- **Migration:** Org with mixed per-repo and per-org → correct
  manifest with both types, existing repos pre-selected.
- **Migration:** Org with only per-repo installations → no per-org
  config read.
- **Migration:** Org with only per-org enrollments → uses config.yaml
  enrollment list and dispatch mint URL.
- Single repo (per-repo installed) → minimal manifest.
- Single repo (per-org enrolled) → reads org config for enrollment.
- Single repo (not installed) → manifest with one new entry.
- Infrastructure field computation: most common ref and region become
  `forge.github` values.
- Interactive selection callback: verify candidates include status
  labels, pre-selected repos match existing installations.
- Secret limitation: `inference_project` left as TODO when no flag
  provided.
- Multiple mint URLs discovered → most common used, TODO generated.

### Test strategy

Unit tests with `forge.FakeClient`. Pre-populate `FileContents` for
per-org `config.yaml` and workflow files. Pre-populate variable values
for guard variables and config. `RepoSelectFunc` implemented as a
test double that records candidates and returns a predetermined
selection.

---

## File Summary

| File | Action |
|------|--------|
| `internal/repos/init.go` | Create |
| `internal/repos/init_test.go` | Create |
| `internal/cli/repos.go` | Modify |
| `internal/repos/manifest.go` | Modify (add `Marshal()`) |
