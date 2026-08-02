# Implementation Plan: Repos Management for Per-Repo Installations

## Context

[ADR 0057](../ADRs/0057-repos-management.md) adds a `fullsend repos`
subcommand group with a declarative `repos.yaml` manifest for managing
per-repo installations across multiple orgs.

The work is structured as 8 PRs across two phases. Phase 1 (PRs 1–4)
builds the foundation: extracting reusable install logic, the manifest
parser, a new forge method, and read-only status. Phase 2 (PRs 5–8)
adds write operations: bulk install, sync/diff, upgrade, add/remove/uninstall.

> **Consolidation (PR #5807):** The 9-command surface area was
> consolidated to 5 commands (`migrate`, `install`, `uninstall`, `status`, `set-default`).
> `repos install` became a convergence operator (provision + sync + upgrade).
> `repos uninstall` gained `--manifest-only` and `--uninstall-only` flags.
> See [repos-command-consolidation.md](repos-command-consolidation.md).

---

## Design

This section captures the manifest schema, behavioral specifications,
and design constraints that the ADR defers to this plan. The PR
sections below implement these specifications.

### Manifest schema (`repos.yaml`)

The manifest declares desired state for all managed repos:

```yaml
version: 1

# Per-forge infrastructure configuration.
forge:
  github:
    # Instance URL — defaults to https://github.com; set for GitHub Enterprise Server.
    # url: https://github.example.com
    # Shared mint infrastructure — one mint serves all repos.
    # mint_url: Cloud Run endpoint (contains a random hash, not derivable from project/region).
    mint_url: https://fullsend-mint-abc123-uc.a.run.app
    # GitHub-specific version settings.
    fullsend_ref: v2.3.0
  # gitlab:
  #   url: https://gitlab.example.com  # required, no default

# Default configuration applied to all repos.
defaults:
  forge: github
  allowed_remote_resources:
    - https://raw.githubusercontent.com/fullsend-ai/fullsend/
    - https://github.com/acme-corp/harness-library/

# Repos to manage. Simple strings inherit all defaults;
# objects can override the forge.
repos:
  # Simple form — inherits all defaults
  - acme-corp/api-server
  - acme-corp/web-frontend

  # Object form — forge override
  - repo: acme-platform/infra-tools
    forge: github

  # Glob pattern — all non-archived, non-fork repos in the org
  - acme-oss/*
```

#### Field-to-resource mapping

Manifest fields map to repo-level resources as follows:

| Manifest field | Repo resource | Type |
|---|---|---|
| `forge.github.fullsend_ref` | `@ref` in scaffold shim `uses:` line | Workflow file |
| `forge.github.mint_url` | `FULLSEND_MINT_URL` | Variable |
| `allowed_remote_resources` | `allowed_remote_resources` in org `config.yaml` | Config file ¹ |

¹ `allowed_remote_resources` is an org-level field from `config.yaml`,
not a per-repo resource. It is not managed by `repos sync`.

#### Field resolution

Infrastructure fields (`fullsend_ref`) live in the forge-specific
section (`forge.github`).
`defaults` holds only `forge` and `allowed_remote_resources`.
Repo entries may override `forge` but not infrastructure fields.

Empty-string and zero-value fields are treated as unset and fall
through to built-in defaults. To explicitly clear a field that has a default,
set it to YAML null (`~` or `null`). A null override stops the fallback
chain rather than inheriting the default.

#### Glob expansion

Entries containing `*` are expanded by calling `ListOrgRepos` on the
org portion and filtering by the glob pattern. Expansion happens at
command execution time. Glob-expanded repos inherit forge-level
settings and defaults. Explicit entries take precedence over globs.

> **Note:** `ListOrgRepos` accepts an `includePrivate` parameter.
> `ExpandGlobs` passes `includePrivate=true` because repos.yaml
> manifests operate in per-repo mode where private repos are valid
> targets. Per-org callers pass `false` to preserve the original
> exclusion. Archived and forked repos remain excluded by default.

#### Multi-org support

Each repo entry is an `owner/repo` pair. Repos from different GitHub
organizations coexist in the same manifest. The mint's `ALLOWED_ORGS`
supports multiple orgs, and `ROLE_APP_IDS` maps role names to app
IDs — the mint infrastructure is inherently multi-org.

Cross-org sharing works because:

- **Apps**: shared public apps can be installed on repos in any org.
- **WIF**: per-repo providers are scoped by `assertion.repository`
  (not by org), so repos in different orgs get independent providers.
- **Mint registration**: `EnsureOrgInMint` adds each repo's org to
  `ALLOWED_ORGS` (comma-separated list).

### Subcommand specifications

#### `fullsend repos migrate`

Generates a `repos.yaml` manifest. Discovers existing per-repo and
per-org installations. Covered by the
[repos migrate plan](repos-init.md).

#### `fullsend repos status`

Read-only discovery. Compares manifest against actual forge state.

For each repo: reads variables (`FULLSEND_MINT_URL`,
`FULLSEND_PER_REPO_INSTALL`) in a single API
call, reads the workflow file and extracts `@ref`, compares against
manifest-resolved config, reports drift.

```
$ fullsend repos status

REPO                          REF       STATUS         DRIFT
acme-corp/api-server          v2.3.0    installed      none
acme-corp/web-frontend        v2.1.0    installed      MINT_URL differs
acme-corp/ml-pipeline         v2.3.0    installed      none
acme-corp/mobile-app          —         not installed  —

2 installed, 1 drifted, 1 not installed
```

Supports `--json` for machine-readable output. Exit code 0 if all repos
match; 1 if drift or missing repos.

#### `fullsend repos install`

Installs fullsend on repos not yet installed. Three-phase execution:

1. **Phase 1 (parallel):** Discover current state, check guard
   variable and all installation components via
   `checkInstallComponents`, partition into `toInstall` and
   `alreadyInstalled`.
2. **Phase 2 (sequential):** `EnsureOrgInMint` once per unique org,
   then `RegisterPerRepoWIF` per repo. Re-checks the guard variable
   and all installation components before provisioning to narrow the
   TOCTOU window. Both operations are not concurrent-safe
   (read-modify-write on Cloud Run env vars).
3. **Phase 3 (parallel):** Scaffold commits, variable/secret writes.

Concurrent `repos install` and `fullsend github setup` targeting the
same repo are unsafe — no distributed lock is held.

Supports `--dry-run`, positional args (filter, supports globs), `--concurrency`.

#### `fullsend repos diff`

Previews what `repos sync` would change.

```
$ fullsend repos diff

REPO                     FIELD               CURRENT              DESIRED
acme-corp/web-frontend   FULLSEND_MINT_URL   https://old-mint...  https://fullsend-mint-abc123...
```

#### `fullsend repos sync`

Reconciles configuration drift for installed repos.

| Resource | Action |
|----------|--------|
| `FULLSEND_MINT_URL` variable | Upsert to match manifest `forge.github.mint_url` |
| `FULLSEND_GCP_PROJECT_ID` secret | Upsert (value passed via `--inference-project` CLI flag) |

Sync does **not** touch scaffold shim version (managed by `upgrade`),
harness files (managed via ADR 0045's `base` composition), or the
`FULLSEND_PER_REPO_INSTALL` guard variable (managed exclusively by
`repos install` — sync skips repos where the guard is not `"true"`).

#### `fullsend repos upgrade`

Upgrades scaffold shim ref across repos by replacing `@ref` in all
`fullsend-ai/fullsend` `uses:` lines within workflow files.

```
$ fullsend repos upgrade

Upgrading repos:
  acme-corp/api-server       v2.1.0 → v2.3.0  ✓
  acme-corp/web-frontend     v2.1.0 → v2.3.0  ✓
  acme-corp/legacy-service   v2.1.0            (pinned, already current)
  acme-corp/bleeding-edge    latest            floating ref "latest" (not eligible for upgrade)

2 upgraded, 1 current, 1 skipped
```

Version safety: blocks downgrades by default (`--force` to override),
skips floating refs (branch names, partial versions like `v0`, `v1.2`),
respects per-repo pinned versions. The `--ref` flag overrides the
manifest for one-off upgrades.

**SHA pinning:** When a workflow's current ref is a SHA, `repos
upgrade` resolves the target tag to its commit SHA via the forge API
and writes `@<sha> # <tag>`, preserving the SHA-pinning convention.
Tag-only repos remain tag-only. If tag-to-SHA resolution fails
(tag does not exist, API error), the upgrade fails for that repo
rather than falling back to tag-only format.

#### `fullsend repos add`

Adds repo entries to the `repos.yaml` manifest. Supports glob patterns
(e.g. `acme/*`) and validates repo name format (`owner/repo`). Skips
duplicates. With `--install`, also installs fullsend on the added repos.

Supports `--forge` (required), `--dry-run`, `--install`, `--concurrency`, `--direct`.

#### `fullsend repos remove`

Removes repo entries from the `repos.yaml` manifest. Glob patterns are
matched against manifest entries and prompt for confirmation unless
`--yes` is set.

With `--uninstall`, tears down fullsend from the matched repos before
removing them from the manifest (deletes workflow, variables, and secrets).
GCP infrastructure is managed separately via `inference provision`,
`mint deploy`, and `mint enroll`.

Supports `--dry-run`, `--uninstall`, `--yes`, `--concurrency`.

#### `fullsend repos uninstall`

Tears down fullsend from specific repos without modifying the manifest.
Glob patterns are matched against manifest entries.

For each repo: deletes workflow file, variables, and secrets. GCP
infrastructure is managed separately via `inference provision`,
`mint deploy`, and `mint enroll`.

Does **not** remove repos from the manifest — use `repos remove` for
that. Does **not** remove `.fullsend/` — it contains user-authored
config that may be version-controlled independently.

Supports `--dry-run`, `--yes`, `--concurrency`.

### Version management

The repos tool's version management builds on
[ADR 0048](../ADRs/0048-automatic-updates.md)'s `--upstream-ref`.

The manifest's `fullsend_ref` maps to `--upstream-ref`:
- `forge.github.fullsend_ref` — default for all GitHub repos

Mixed-version repos are a normal operating state. `repos status`
reports version health. `repos upgrade` changes versions explicitly.
`repos sync` never touches versions — this separation prevents
accidental upgrades during routine config reconciliation.

### Relationship to per-org deprecation (ADR 0044, pending)

The repos tool can be built and shipped **independently** of ADR 0044
(pending) — and ideally **before** it:

- `repos status` detects per-org enrolled repos and reports them
  distinctly.
- `repos install` respects the guard variable — it won't install
  per-repo on a repo that is already per-repo installed.
- When ADR 0044 is implemented, the repos tool serves as the migration
  path: operators write a `repos.yaml` and run `repos install` to
  convert per-org repos to per-repo.

Building the repos tool first de-risks deprecation by giving users the
replacement tooling before removing what it replaces.

### Future enhancements

**Unified install command:** `fullsend repos install` could subsume
`fullsend github setup` for installation — accepting a positional
`owner/repo` with no manifest for single-repo mode, or `--manifest`
for batch mode. This unification would be a significant UX shift and
should be proposed in its own ADR when pursued.

---

### Subsystems touched

| Subsystem | Key files | What changes |
|-----------|-----------|-------------|
| CLI | `internal/cli/repos.go`, `internal/cli/root.go` | New `repos` subcommand group |
| Repos logic | `internal/repos/` (new package) | Manifest parser, install, status, sync, upgrade, remove |
| CLI admin | `internal/cli/admin.go` | Delegates to extracted install logic |
| Forge interface | `internal/forge/forge.go`, `github/github.go`, `fake.go` | `ListRepoVariables`, `DeleteRepoVariable`, `DeleteRepoSecret` |

## PR Dependency Graph

```
PRs 1, 2, 3 ─────────> PR 5 (repos install)
PRs 2, 3 ────> PR 4 (repos status) ──┬──> PR 6 (sync/diff)
                                      └──> PR 7 (upgrade)
PRs 1, 3 ─────────> PR 8 (add/remove/uninstall)
```

PRs 1, 2, 3 are independent and can be developed in parallel.
PR 4 depends on PRs 2 and 3 (manifest resolution + variable listing).
PR 5 depends on PRs 1, 2, and 3 (extracted install + manifest +
`ListRepoVariables` for guard variable checks in Phase 1).
PR 6 depends on PR 4 (status logic is shared with diff).
PR 7 depends on PR 4 (reuses `extractWorkflowRef()` for reading
current refs from workflow files).
PR 8 depends on PRs 1 and 3 (reuses install types +
`DeleteRepoVariable`/`DeleteRepoSecret`) and can be developed in
parallel with PRs 4–7. Implements three commands: `repos add`,
`repos remove`, and `repos uninstall`.

The `repos migrate` command is covered by a
[separate implementation plan](repos-init.md) and can be developed
in parallel with PRs 4–8.

---

## Phase 1: Foundation

### PR 1: Extract per-repo install logic into reusable package ✓

**Status:** Implemented in [#3003](https://github.com/fullsend-ai/fullsend/pull/3003).

**Scope:** Refactor only. Preserves install semantics.

The existing `runPerRepoInstall()` in `internal/cli/admin.go` is ~450
lines mixing install logic with CLI concerns (interactive prompts,
progress spinners, flag parsing). Extract the core logic into a
reusable package so both `fullsend github setup` and `repos install`
can call it.

#### `internal/repos/install.go` (new)

Define the install interface as a pure function taking a config struct:

```go
type InstallConfig struct {
    Owner                   string
    Repo                    string
    MintURL                 string
    InferenceProject        string
    InferenceRegion         string
    UpstreamRef             string
    SkipAppSetup            bool
    SkipMintCheck           bool
    SkipMintDeploy          bool
    VendorBinary            bool
}

type InstallResult struct {
    Owner           string
    Repo            string
    Success         bool
    Error           error
    AlreadyInstalled bool
    ScaffoldPR      string
}

func Install(ctx context.Context, cfg InstallConfig,
    client forge.Client,
    progress ProgressFunc) (*InstallResult, error)
```

Extract from `runPerRepoInstall()`:

- Infrastructure discovery (mint check, app discovery)
- App creation (delegate to `appsetup.Run()`)
- Mint enrollment (delegate to mint client)
- Scaffold generation and commit
- Variable/secret writes

Keep in `admin.go`:

- Flag parsing and validation
- Interactive prompts (app name confirmation, etc.)
- Progress spinner rendering
- Error message formatting

Define `ProgressFunc` for progress reporting:

```go
type ProgressFunc func(repo, phase, message string)
```

#### `internal/cli/admin.go` (modify)

Replace the body of `runPerRepoInstall()` with a call to
`repos.Install()`, mapping CLI flags to `InstallConfig` fields and
wrapping the progress callback for spinner output.

#### `internal/repos/install_test.go` (new)

Test `Install()` with a fake forge client and fake WIF provisioner:

- Fresh install: verify scaffold committed, variables set, secrets set.
- Already installed (all installation components present — guard
  variable, workflow file, variables, and secrets): returns
  `AlreadyInstalled: true`, no writes.
- Partial install (guard variable present but other components
  missing): proceeds with repair.
- Skip app setup: verify `appsetup.Run()` not called.
- Skip mint check: verify mint discovery not called.
- Scaffold commit failure: returns error.

#### Test strategy

Unit tests with fakes. Run `make go-test` to verify no regressions in
`admin_test.go`.

---

### PR 2: Repos manifest parser and validation ✓

**Status:** Implemented in [#3002](https://github.com/fullsend-ai/fullsend/pull/3002).

**Scope:** New package code. No CLI wiring yet.

#### `internal/repos/manifest.go` (new)

```go
type Manifest struct {
    Version  int            `yaml:"version"`
    Forge    ForgeSection   `yaml:"forge"`
    Defaults DefaultsConfig `yaml:"defaults"`
    Repos    []RepoEntry    `yaml:"repos"`
}

type ForgeSection struct {
    GitHub GitHubForgeInfra `yaml:"github,omitempty"`
    GitLab GitLabForgeInfra `yaml:"gitlab,omitempty"`
}

type GitHubForgeInfra struct {
    URL         string `yaml:"url,omitempty"`
    MintURL     string `yaml:"mint_url,omitempty"`
    FullsendRef string `yaml:"fullsend_ref,omitempty"`
}

type GitLabForgeInfra struct {
    URL string `yaml:"url"`
}

type DefaultsConfig struct {
    Forge                  string   `yaml:"forge"`
    AllowedRemoteResources []string `yaml:"allowed_remote_resources,omitempty"`
}

type RepoEntry struct {
    Repo  string         `yaml:"repo"`
    Forge NullableString `yaml:"forge,omitempty"`
}

// NullableString distinguishes three YAML states:
// - omitted:       Set=false, Null=false, Value=""
// - explicit null:  Set=true,  Null=true,  Value=""
// - explicit value: Set=true,  Null=false,  Value="v2.3.0"
// Plain *string cannot distinguish omitted from null in yaml.v3
// (both unmarshal to nil). This wrapper inspects the yaml.Node tag.
type NullableString struct {
    Value string
    Set   bool
    Null  bool
}

func (n *NullableString) UnmarshalYAML(node *yaml.Node) error {
    if node.Tag == "!!null" {
        n.Set = true
        n.Null = true
        return nil
    }
    n.Set = true
    return node.Decode(&n.Value)
}

func (n NullableString) MarshalYAML() (interface{}, error) {
    if n.Null {
        return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null"}, nil
    }
    if !n.Set {
        return nil, nil
    }
    return n.Value, nil
}

func (n NullableString) IsZero() bool {
    return !n.Set
}
```

Key functions:

```go
func LoadManifest(pathOrURL string) (*Manifest, error)

func (m *Manifest) Validate() error

func (m *Manifest) ExpandGlobs(ctx context.Context,
    clients ForgeClientFactory) ([]ResolvedRepo, error)

func (m *Manifest) ResolveConfig(owner, repo string) ResolvedConfig
```

Custom YAML unmarshaling for `RepoEntry` — handle both string form
(`"acme-corp/api"`) and object form (`repo: acme-corp/api`). Uses the
yaml.v3 `*yaml.Node` signature (the project uses `gopkg.in/yaml.v3`):

```go
func (r *RepoEntry) UnmarshalYAML(node *yaml.Node) error {
    if node.Kind == yaml.ScalarNode {
        r.Repo = node.Value
        return nil
    }
    type raw RepoEntry
    return node.Decode((*raw)(r))
}
```

`LoadManifest()` accepts a local file path or an HTTPS URL. If the
argument starts with `https://`, fetch the content via HTTP GET before
parsing. This follows the ADR 0038 resource reference model and reuses
the URL fetching logic from the harness resource loader.

`Validate()` checks:

- `version` is 1 (only supported version).
- `forge.github.url` defaults to `https://github.com` when unset; must be a valid HTTPS URL with no path.
- `forge.github.mint_url` is a valid HTTPS URL (when GitHub repos are present).
- `forge.gitlab.url` is required and must be a valid HTTPS URL with no path (when GitLab repos are present).
- Each repo entry has a valid `owner/repo` format.
- No duplicate repos (after glob expansion).
- Glob patterns are valid `filepath.Match` patterns with an `org/`
  prefix (e.g., `acme-corp/*`, `acme-corp/api-*`).

`ExpandGlobs()`:

- For entries containing `*`, extract the org prefix.
- Call `ListOrgRepos(ctx, org, true)` to list eligible repos.
  `ExpandGlobs` passes `includePrivate=true` because repos.yaml
  manifests operate in per-repo mode where private repos are valid
  targets. Per-org callers pass `false` to preserve the original
  exclusion. Archived and forked repos remain excluded regardless of
  the flag.
- Filter by glob pattern using `filepath.Match`.
- Merge with explicit entries (explicit wins over glob).
- Return `[]ResolvedRepo` with resolved configuration per repo.

`ResolveConfig()`:

- Look up the repo in the manifest (explicit or glob-matched).
- Merge: per-repo override > `defaults` > built-in defaults.
- `RepoEntry` uses `NullableString` fields so that `ResolveConfig`
  can distinguish three states: omitted (`Set=false`, inherit
  default), explicitly null (`Null=true`, stops fallback chain), or
  set to a non-empty value (`Set=true, Value != ""`, overrides
  default). A fourth state — explicitly set to empty string
  (`Set=true, Value=""`) — is treated as unset and falls through to
  defaults, matching the ADR prose. Plain `*string` cannot make these
  distinctions because `yaml.v3` unmarshals both omitted and `null`
  as nil. `NullableString` uses a custom `UnmarshalYAML` that
  inspects the `yaml.Node` tag to detect explicit null.
- Resolution helper for a single field:

  ```go
  func resolveField(override NullableString, fallback string, builtinDefault string) string {
      if override.Null {
          return "" // explicit null stops fallback chain
      }
      if override.Set && override.Value != "" {
          return override.Value
      }
      if fallback != "" {
          return fallback
      }
      return builtinDefault
  }
  ```

  The `override` parameter is `NullableString` because the forge field
  on `RepoEntry` uses three-state semantics. The `fallback` parameter
  is plain `string` because defaults are either set or empty — no null
  distinction needed. Infrastructure fields (`FullsendRef`) are
  sourced from the forge-specific
  section (`forge.github`) rather than per-repo overrides.

- Return `ResolvedConfig` with all fields resolved.

```go
type ResolvedConfig struct {
    Owner                  string
    Repo                   string
    Forge                  string
    ForgeConfig            ForgeConfig
    MintURL                string
    FullsendRef            string
    AllowedRemoteResources []string
}
```

#### `internal/repos/manifest_test.go` (new)

- Parse simple manifest (all string repos).
- Parse manifest with mixed string and object repos.
- Parse manifest with glob patterns.
- Custom YAML unmarshaling: string form and object form.
- Validation: missing mint URL, invalid repo format, duplicate repos.
- Glob expansion with fake forge client.
- Config resolution: defaults only, forge-level settings, multi-org.
- Version validation: reject version != 1.
- URL loading: `httptest` server serving manifest YAML, verify parsed
  correctly.
- URL loading: non-200 response → error.

#### Test strategy

Unit tests. Glob expansion tested with `forge.FakeClient` pre-populated
with repo lists. URL loading tested with `httptest`.

---

### PR 3: Add `ListRepoVariables`, `DeleteRepoVariable`, `DeleteRepoSecret` to forge ✓

**Status:** Implemented in [#3001](https://github.com/fullsend-ai/fullsend/pull/3001).

**Scope:** Interface addition. No CLI changes.

#### `internal/forge/forge.go` (modify)

Add three methods to the `forge.Client` interface:

```go
ListRepoVariables(ctx context.Context, owner, repo string) (map[string]string, error)
DeleteRepoVariable(ctx context.Context, owner, repo, name string) error
DeleteRepoSecret(ctx context.Context, owner, repo, name string) error
```

`ListRepoVariables` returns all Actions variables as a name→value map.
`DeleteRepoVariable` and `DeleteRepoSecret` are needed by `repos remove`
(PR 8) and are cheaper to add here alongside `ListRepoVariables`.

`ListOrgRepos` now accepts an `includePrivate bool` parameter so that
glob expansion in per-repo mode includes private repos. Per-org callers
pass `false` to preserve the original exclusion. Archived and forked
repos remain excluded regardless of the flag.

#### `internal/forge/github/github.go` (modify)

Implement `ListRepoVariables`:

- Call `GET /repos/{owner}/{repo}/actions/variables` (paginated).
- Parse response: `{ variables: [{ name, value }], total_count }`.
- Return `map[string]string`.

Implement `DeleteRepoVariable`:

- Call `DELETE /repos/{owner}/{repo}/actions/variables/{name}`.
- Return nil on 204 or 404 (idempotent).

Implement `DeleteRepoSecret`:

- Call `DELETE /repos/{owner}/{repo}/actions/secrets/{name}`.
- Return nil on 204 or 404 (idempotent).

#### `internal/forge/fake.go` (modify)

Add implementations to `FakeClient`:

- `ListRepoVariables`: return from `VariableValues` map (existing
  field, keyed by `owner/repo/name`).
- `DeleteRepoVariable`: remove from `VariableValues`.
- `DeleteRepoSecret`: remove from `Secrets`.

Track deletions in new slices for test assertions:

```go
DeletedVariables []VariableRecord
DeletedSecrets   []SecretRecord
```

#### `internal/forge/github/github_test.go` (modify)

Add `httptest`-based tests:

- `ListRepoVariables`: paginated response (2 pages), empty repo,
  API error.
- `DeleteRepoVariable`: successful delete (204), already missing
  (404), API error.
- `DeleteRepoSecret`: successful delete (204), already missing (404),
  API error.

#### Test strategy

Unit tests with `httptest` for GitHub implementation. Fake client
tested via consumers in later PRs.

---

### PR 4: `fullsend repos status` (read-only discovery) ✓

**Status:** Implemented in [#3031](https://github.com/fullsend-ai/fullsend/pull/3031).

**Scope:** New CLI command. Read-only.

**Depends on:** PR 2 (manifest parser), PR 3 (`ListRepoVariables`).

#### `internal/cli/repos.go` (new)

Add the `repos` subcommand group under the root command:

```go
func newReposCmd() *cobra.Command {
    cmd := &cobra.Command{
        Use:   "repos",
        Short: "Manage per-repo installations across multiple orgs",
    }
    cmd.AddCommand(newReposStatusCmd())
    return cmd
}
```

Flags for `repos status`:

- `--manifest` / `-f` (string, default `repos.yaml`): path or URL to
  manifest file. URLs are fetched following the ADR 0038 resource
  reference model.
- `--json` (bool): emit JSON output instead of table.
- `--repo` (string, repeatable): filter to specific repos.
- `--concurrency` (int, default 8): max parallel API calls.

#### `internal/cli/root.go` (modify)

Wire `newReposCmd()` into the root command.

#### `internal/repos/status.go` (new)

```go
type RepoStatus struct {
    Owner           string
    Repo            string
    Installed       bool
    CurrentRef      string
    ExpectedRef     string
    MintURL         string
    ExpectedMintURL string
    Region          string
    ExpectedRegion  string
    Drifts          []Drift
}

type Drift struct {
    Field    string
    Expected string
    Actual   string
}

func Status(ctx context.Context, manifest *Manifest,
    clients ForgeClientFactory, maxConcurrency int) ([]RepoStatus, error)
```

Per-repo discovery (parallelizable, read-only):

1. Call `ListRepoVariables(ctx, owner, repo)` to read guard variable,
   mint URL, region.
2. Call `GetFileContent` for each path in `ForgeConfig.WorkflowPaths`
   (forge-specific; GitHub uses `.github/workflows/fullsend.yml`/`.yaml`,
   GitLab uses `.gitlab/workflows/fullsend-dispatch.yml`) to extract the
   current ref.
3. Compare against manifest-resolved config.
4. Build `RepoStatus` with drift entries.

Ref extraction from workflow file:

```go
// Patterns and extraction are now forge-specific via ForgeConfig.
// See internal/repos/forge_config.go for the per-forge definitions.

func extractWorkflowRef(content []byte, fc ForgeConfig) string
```

Exit code: 0 if all repos match; 1 if any drift or missing.

#### `internal/repos/status_test.go` (new)

- All repos installed, no drift → exit 0.
- One repo not installed → exit 1.
- Drift in mint URL → correct drift entry.
- Drift in ref → correct drift entry.
- Multiple drifts on one repo.
- Workflow file missing → not installed.
- API error → partial results with error.
- Glob-expanded repos.

#### Test strategy

Unit tests with `forge.FakeClient`. Pre-populate `FileContents` and
variable values to simulate installed/non-installed repos.

---

## Phase 2: Write Operations

### PR 5: `fullsend repos install` (bulk install with WIF serialization) ✓

**Status:** Implemented in [#3033](https://github.com/fullsend-ai/fullsend/pull/3033).

**Scope:** New CLI command. Creates infrastructure.

**Depends on:** PR 1 (extracted install logic), PR 2 (manifest parser).

#### `internal/cli/repos.go` (modify)

Add `newReposInstallCmd()` to the repos subcommand group.

Flags:

- `--manifest` / `-f` (string, default `repos.yaml`): path or URL.
- `--dry-run` (bool).
- Positional args: install specific repos only (supports globs).
- `--skip-app-setup` (bool).
- `--concurrency` (int, default 4): max parallel scaffold writes.

#### `internal/repos/batch_install.go` (new)

```go
type BatchInstallConfig struct {
    Manifest       *Manifest
    DryRun         bool
    RepoFilter     []string
    MaxConcurrency int
    Roles          []string
    UpstreamRef    string
    UpstreamTag    string
    Direct         bool
}

type BatchInstallResult struct {
    Installed []InstallResult
    Skipped   []InstallResult
    Failed    []InstallResult
}

func BatchInstall(ctx context.Context, cfg BatchInstallConfig,
    clients ForgeClientFactory,
    progress ProgressFunc) (*BatchInstallResult, error)
```

Two-phase execution:

**Phase 1 (parallel):** For each repo (or filtered subset), check the
guard variable. When the guard is set, verify all installation
components (workflow file, variables, and secrets) via
`checkInstallComponents`. A repo is only classified as
`alreadyInstalled` when every component is present; partial installs
proceed to Phase 2 for repair. Repos without the guard go to
`toInstall`.

**Phase 2 (sequential):**

First, call `EnsureOrgInMint(ctx, mintURL, org)` once per unique org
in `toInstall` — validates the mint exists at the expected URL and
ensures each org is in `ALLOWED_ORGS`. This is an org-level operation;
calling it per repo would be redundant and add unnecessary latency
from repeated read-modify-write cycles on Cloud Run env vars. If
`EnsureOrgInMint` fails for an org, all repos in `toInstall`
belonging to that org are moved to `BatchInstallResult.Failed` with
the error and excluded from scaffold writes.

Then, for each remaining repo in `toInstall`:

- Re-check the guard variable and all installation components via
  `checkInstallComponents`. If the guard is now `"true"` and all
  components are present (another process fully installed between
  Phase 1 and Phase 2), move the repo to `alreadyInstalled` and skip
  installation. If the guard is set but components are missing, proceed
  with repair. This narrows the TOCTOU window documented in the ADR.
- Call `RegisterPerRepoWIF(ctx, repo)` — adds repo to mint's
  `PER_REPO_WIF_REPOS`.
- Call `Install()` (from PR 1). Commits scaffold, writes variables/secrets.

Errors on individual repos do not abort the batch. Failed repos are
collected in `BatchInstallResult.Failed`.

#### `internal/repos/batch_install_test.go` (new)

- Fresh repos: all repos uninstalled → all installed.
- Partial repos: some already installed → only new repos installed.
- Mint enrollment serialization: verify `RegisterPerRepoWIF` calls are sequential
  (mutex-checking fake).
- Repo filter: only filtered repos installed.
- Error on one repo: others still installed, failed in `Failed` list.
- Dry-run: no write operations.

#### Test strategy

Unit tests with `forge.FakeClient`. Verify call ordering via recorded
method calls.

---

### PR 6: `fullsend repos sync` + `fullsend repos diff` ✓

**Status:** Implemented in [#4079](https://github.com/fullsend-ai/fullsend/pull/4079).

**Scope:** New CLI commands. Writes variables/secrets.

**Depends on:** PR 4 (status logic shared with diff).

#### `internal/cli/repos.go` (modify)

Add `newReposDiffCmd()` and `newReposSyncCmd()`.

Flags for both:

- `--manifest` / `-f`.
- `--repo` (repeatable).

`sync` additionally:

- `--dry-run` (equivalent to `diff`).
- `--concurrency` (int, default 4).

#### `internal/repos/sync.go` (new)

```go
type Change struct {
    Owner    string
    Repo     string
    Field    string
    Type     string // "variable" or "secret"
    Action   string // "create", "update"
    OldValue string // empty for secrets (not readable)
    NewValue string // empty for secrets
}

func Diff(ctx context.Context, manifest *Manifest,
    clients ForgeClientFactory, maxConcurrency int) ([]Change, error)

func Sync(ctx context.Context, manifest *Manifest,
    clients ForgeClientFactory, maxConcurrency int,
    progress ProgressFunc) ([]Change, error)
```

What sync reconciles:

| Resource | Action |
|----------|--------|
| `FULLSEND_MINT_URL` | Upsert to match `forge.github.mint_url` |
| `FULLSEND_PER_REPO_INSTALL` | Ensure `"true"` |
| `FULLSEND_GCP_PROJECT_ID` | Upsert (value passed via `--inference-project` CLI flag) |

What sync does NOT touch:

- Scaffold shim version (`@ref`).
- Harness files.
- Repos not in the manifest (warns about extras found).

Secret handling: secrets cannot be read via the API (only existence
checked via `RepoSecretExists`). For secrets, diff reports "exists"
or "missing". Sync always writes the manifest value (idempotent via
`CreateRepoSecret` overwrite).

`Diff` reuses the discovery logic from `Status` (PR 4) to find
current state, then computes the change set.

#### `internal/repos/sync_test.go` (new)

- No drift → empty change list.
- Variable drift (mint URL, region) → correct change entries.
- Missing guard variable → create action.
- Secret missing → create action.
- Secret exists but project changed in manifest → update action.
- Extra installed repos not in manifest → warning.
- Sync applies changes → verify forge writes called.
- Dry-run → no writes.

#### Test strategy

Unit tests with `forge.FakeClient`.

---

### PR 7: `fullsend repos upgrade` ✓

**Status:** Implemented in [#4080](https://github.com/fullsend-ai/fullsend/pull/4080).

**Scope:** New CLI command. Writes workflow files.

**Depends on:** PR 4 (reuses `extractWorkflowRef()` for reading
current refs from workflow files).

#### `internal/cli/repos.go` (modify)

Add `newReposUpgradeCmd()`.

`upgrade` flags:

- `--manifest` / `-f`.
- `--ref` (string): override manifest `fullsend_ref`.
- Positional args `[repos...]`: filter to specific repos (supports globs via `filepath.Match`).
- `--dry-run`.
- `--force`: upgrade even if current ref is newer.
- `--concurrency` (int, default 4).
- `--direct`: push scaffold directly to default branch (skip PR).

#### `internal/repos/upgrade.go` (new)

```go
type UpgradeConfig struct {
    Manifest       *Manifest
    RefOverride    string
    RepoFilter     []string
    DryRun         bool
    Force          bool
    MaxConcurrency int
}

type UpgradeResult struct {
    Owner      string
    Repo       string
    OldRef     string
    NewRef     string
    Upgraded   bool
    Skipped    bool
    SkipReason string
    Error      error
}

func Upgrade(ctx context.Context, cfg UpgradeConfig,
    clients ForgeClientFactory,
    progress ProgressFunc) ([]UpgradeResult, error)
```

Upgrade logic per repo:

1. Read workflow file, extract current `@ref` via
   `extractWorkflowRef()`.
2. Determine target ref: `--ref` flag >
   `forge.github.fullsend_ref`.
3. Skip if target is a floating ref (`latest`, `main`, `master`,
   or partial version tags like `v0`, `v1.2`).
4. Skip if current ref is floating (same criteria as step 3).
5. If both current and target are valid semver: skip if current >
   target unless `--force` is set (prevents accidental downgrade).
   If current is not valid semver (e.g., a branch name or SHA),
   proceed with the upgrade — the current ref cannot be compared.
6. Replace `@ref` in all `fullsend-ai/fullsend` `uses:` lines via
   `replaceShimRef()` regex. Skip if no lines matched.
7. Commit via `ScaffoldCommitFunc` (injected; the CLI layer wires it
   to `CommitFilesToBranch` or direct push depending on `--direct`).

Ref replacement in scaffold:

```go
func replaceShimRef(content []byte, newRef, newTag string, fc ForgeConfig) ([]byte, bool)
```

Replaces all `@<oldRef>` occurrences (and optional trailing `# tag`
comments) in `uses:` lines referencing `fullsend-ai/fullsend`. The
regex uses `[ \t]*` (not `\s*`) for the comment group to avoid
matching across newlines.

In-place replacement is chosen over full scaffold regeneration to
preserve any user customizations in the shim workflow.

**SHA-pinning preservation:** When the current ref is a SHA,
`upgradeRepo` resolves the target tag to its commit SHA via
`GetRef` and calls `replaceShimRef(content, sha, tag)` to
produce `@<sha> # <tag>`. Tag-only repos keep tag-only format.
If `GetRef` fails, the upgrade returns an error for that repo
rather than falling back to tag-only format.

#### `internal/repos/upgrade_test.go` (new)

- All repos at target → all skipped.
- All repos behind target → all upgraded, verify workflow content.
- Mixed: some current, some behind, some ahead.
- `--force` overrides "ahead" skip.
- `--ref` overrides manifest ref.
- Positional args filter.
- Dry-run → no writes.
- Semver comparison: table-driven tests (including prerelease §11).
- Floating ref detection (partial versions, branch names).
- Standalone comment preservation across newlines.
- `--direct` flag passed through to commit function.

#### Test strategy

Unit tests with `forge.FakeClient`. Semver comparison as table-driven tests.

---

### PR 8: repos management (add, remove, uninstall) ✓

**Status:** Implemented in [#4081](https://github.com/fullsend-ai/fullsend/pull/4081).

**Scope:** Three new CLI commands for managing per-repo installations.

**Depends on:** PR 1 (reuses types), PR 3 (`DeleteRepoVariable`,
`DeleteRepoSecret`).

Can be developed in parallel with PRs 4–7.

#### `internal/cli/repos.go` (modify)

Add `newReposAddCmd()`, `newReposRemoveCmd()`, `newReposUninstallCmd()`.

`repos add` — adds repo entries to the manifest:
- Positional args: repos to add (supports globs).
- `--forge` (required): forge type (`github` or `gitlab`). Omits per-entry override when matching `defaults.forge`.
- `--manifest` / `-f`.
- `--dry-run`.
- `--install`: also install fullsend on added repos.
- `--concurrency`, `--direct` (used with `--install`).

`repos remove` — removes repo entries from the manifest:
- Positional args: repos to remove (supports globs).
- `--manifest` / `-f`.
- `--dry-run`.
- `--uninstall`: tear down fullsend before removing from manifest.
- `--yes`: skip confirmation for glob patterns.
- `--concurrency` (used with `--uninstall`).

`repos uninstall` — tears down fullsend without modifying manifest:
- Positional args: repos to uninstall (supports globs).
- `--manifest` / `-f`: used to resolve mint config.
- `--dry-run`.
- `--yes`: skip confirmation for glob patterns.
- `--concurrency` (int, default 4): max parallel Phase 1 cleanup
  operations.

#### `internal/repos/remove.go` (new)

```go
type RemoveConfig struct {
    Manifest       *Manifest
    Repos          []string
    DryRun         bool
    MaxConcurrency int
}

type RemoveResult struct {
    Owner           string
    Repo            string
    Success         bool
    Error           error
    WorkflowDeleted bool
    VarsDeleted     int
    SecretsDeleted  int
}

func Remove(ctx context.Context, cfg RemoveConfig,
    clients ForgeClientFactory,
    progress ProgressFunc) ([]RemoveResult, error)
```

Removal is a single-phase operation (parallel across repos, bounded by
`MaxConcurrency`):

For each repo:

1. Delete workflow file (forge-specific paths from
   `ForgeConfig.WorkflowPaths`). Try `DeleteFile` first. A 404 means the file is
   already absent — treat as success (`WorkflowDeleted = true`).
   If it returns HTTP 403 or 422 (branch protection), fall back to
   `CommitFilesToBranch` + PR creation (same pattern as `repos
   upgrade` uses for scaffold commits). Other errors (network,
   unexpected permissions) are not retried via the fallback.
2. Only if step 1 succeeds: delete repo variables
   (`FULLSEND_MINT_URL`, `FULLSEND_GCP_REGION`,
   `FULLSEND_PER_REPO_INSTALL`) via `DeleteRepoVariable`.
3. Only if step 1 succeeds: delete repo secrets
   (`FULLSEND_GCP_PROJECT_ID`, `FULLSEND_GCP_WIF_PROVIDER`) via
   `DeleteRepoSecret`.

Steps 2 and 3 are independent and run concurrently within each repo.
If workflow deletion fails, the repo is marked as failed and
variables/secrets are left intact — this avoids leaving the repo in a
broken state where the workflow exists but its required variables are
gone.

Does NOT remove repos from the manifest — operator edits `repos.yaml`
manually.

GCP infrastructure (WIF providers, mint enrollment) is managed
separately via `inference provision`, `mint deploy`, and `mint enroll`.

#### `internal/repos/remove_test.go` (new)

- Remove installed repo → all resources deleted.
- Remove non-installed repo → no errors (delete calls return 404).
- Dry-run → no writes.
- Multiple repos → all removed.
- Partial failure → one repo errors, others still removed.

#### Test strategy

Unit tests with `forge.FakeClient` and fake GCF client.

---

## File Summary

| File | PR | Action |
|------|-----|--------|
| `internal/repos/install.go` | 1 | Create |
| `internal/repos/install_test.go` | 1 | Create |
| `internal/cli/admin.go` | 1 | Modify |
| `internal/repos/manifest.go` | 2 | Create |
| `internal/repos/manifest_test.go` | 2 | Create |
| `internal/forge/forge.go` | 3 | Modify |
| `internal/forge/github/github.go` | 3 | Modify |
| `internal/forge/fake.go` | 3 | Modify |
| `internal/forge/github/github_test.go` | 3 | Modify |
| `internal/cli/repos.go` | 4 | Create |
| `internal/cli/root.go` | 4 | Modify |
| `internal/repos/status.go` | 4 | Create |
| `internal/repos/status_test.go` | 4 | Create |
| `internal/repos/batch_install.go` | 5 | Create |
| `internal/repos/batch_install_test.go` | 5 | Create |
| `internal/cli/repos.go` | 5 | Modify |
| `internal/repos/sync.go` | 6 | Create |
| `internal/repos/sync_test.go` | 6 | Create |
| `internal/cli/repos.go` | 6 | Modify |
| `internal/repos/upgrade.go` | 7 | Create |
| `internal/repos/upgrade_test.go` | 7 | Create |
| `internal/cli/repos.go` | 7 | Modify |
| `internal/repos/remove.go` | 8 | Create |
| `internal/repos/remove_test.go` | 8 | Create |
| `internal/cli/repos.go` | 8 | Modify |
