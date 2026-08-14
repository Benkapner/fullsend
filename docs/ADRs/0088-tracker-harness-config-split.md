---
title: "88. Tracker/forge split in the harness config schema"
status: Accepted
relates_to:
  - agent-architecture
  - agent-infrastructure
topics:
  - harness
  - tracker
  - forge
  - jira
  - configuration
---

# 88. Tracker/forge split in the harness config schema

Date: 2026-08-14

## Status

Accepted

## Context

[ADR 0045](0045-forge-portable-harness-schema.md) added a `forge:` block to
the harness schema, keyed by platform (`github`, `gitlab`), so a single
harness file can carry platform-specific `pre_script`/`post_script`, `skills`,
`host_files`, `runner_env`, etc. `forge:` was designed around agents that
perform code-hosting operations (PRs/MRs, branches) — every currently
supported key is also a `forge.Client` implementation
([ADR 0005](0005-forge-abstraction-layer.md)).

This conflates two concerns that a single-key `forge:` cannot separate: the
platform an agent *reads its triggering issue from* and the platform an
agent *writes a code change to*. Those are the same platform today (a
GitHub issue triggers a GitHub PR), so one key has sufficed. It stops
working once a code agent needs to react to a JIRA issue — a pure issue
tracker, no branches, PRs, or repos, so it isn't a `forge.Client`
implementation ([ADR 0005](0005-forge-abstraction-layer.md)) and doesn't
belong as a `forge:` key — while still opening a PR against GitHub or an MR
against GitLab, an unrelated forge. `forge:` has no way to express "read
from tracker X, write to forge Y" for the same harness; only one axis
exists, and JIRA doesn't fit on it.

## Decision

Rename `internal/harness.ForgeConfig` to `PlatformConfig` — matching the
vocabulary the codebase already uses for these keys (`ResolveForge(platform
string)`, `ValidForgePlatform`, `detectForgePlatform`; ADR 0045 itself
describes "the runner selects the appropriate `forge.<platform>` block").
This is scoped to `internal/harness`; it is unrelated to and does not touch
`internal/repos.ForgeConfig`, a separate type for admin-manifest CI paths.

Add a `Tracker map[string]*PlatformConfig` field to the `Harness` struct,
sibling to `Forge`. Both are validated and resolved through the same
generalized functions rather than parallel copies:

- `forge.go`'s `validateForge`/`ResolveForge`/`mergeForgeConfig` become
  section-parameterized (taking a block name for error messages and a
  valid-key set), called once for `Forge` (keys: `github`, `gitlab`) and
  once for `Tracker` (keys: `github`, `gitlab`, `jira`).
- `compose.go`'s `mergeForgeBlocks`/`mergeForgeConfigInto` (the `base:`
  composition merge) are likewise generalized to operate on either map, so
  a `tracker:` block composes across `base:` the same way `forge:` already
  does, without a second implementation.

`forge:` and `tracker:` remain independently optional and independently
resolved — a harness may declare `forge:` only, `tracker:` only, or both,
and a harness combining `tracker.jira` with `forge.github` resolves each
against its own platform with no cross-talk between the two maps.

A new `ValidTrackerPlatform`/`TrackerKeyList` pair (analogous to
`ValidForgePlatform`/`ForgeKeyList`) enforces `tracker:`'s own key
allowlist, `github`, `gitlab`, `jira` — a superset of `forge:`'s
`github`/`gitlab`, since JIRA is a valid tracker but not a valid forge. A
new `ResolveTracker(platform)` method calls the generalized resolution
logic with `Tracker`, mirroring `ResolveForge`'s call with `Forge`: same
pipeline ordering, same error shape. `Load()` calls `ResolveForge` before
`ResolveTracker` — a fixed order, not an implementation detail — which is
what makes the scalar-field precedence below well-defined rather than
call-site accident.

At runtime, the effective tracker platform follows: explicit `--tracker` CLI
flag, else the resolved forge platform (the same value `detectForgePlatform`
already computes from `GITHUB_ACTIONS`/`GITLAB_CI`). This mirrors the
existing flag-then-CI-env precedence pattern rather than introducing a new
one, and means an unmodified GitHub/GitLab run gets an equivalent
`FULLSEND_TRACKER` value for free. For JIRA, the flag is not something an
operator hand-configures per setup: `fullsend poll --input-driver jira-poll`
([ADR 0063](0063-polling-based-work-discovery.md)) already dispatches
Jira-sourced runs today, and each dispatch record already carries a
`NormalizedEvent` with `source.system: "jira"`. The dispatch step should
derive `--tracker`/`FULLSEND_TRACKER` from that field when invoking
`fullsend run`, the same way `detectForgePlatform` derives forge from CI
environment, rather than requiring a manually-set flag per Jira install.
The runner injects the result as `FULLSEND_TRACKER` into both the runner
and sandbox environment, alongside the existing `FULLSEND_FORGE`.


### Precedence Rules

`ResolveForge` and `ResolveTracker` both merge into the same top-level
`Harness` scalar fields (`PreScript`, `PostScript`, `Policy`,
`ValidationLoop`) — there is one pre_script slot per run, not one per axis.
Because resolution order is fixed (forge, then tracker), tracker's
non-empty value always wins when a harness sets both, e.g.
`forge.github.pre_script` and `tracker.jira.pre_script` — a designed
precedence agent authors can depend on, not an artifact of call order.
List- and map-shaped fields (`Skills`, `Providers`, `HostFiles`,
`RunnerEnv`, `Env`) are unaffected by this ordering: both forge's and
tracker's contributions are concatenated/merged per ADR 0045's existing
rules regardless of which resolves first.

In practice this scalar precedence rarely needs to be exercised: the
existing forge-dispatch pattern already handles per-platform behavior
behind a single script by branching internally on the injected platform
variable (e.g. `triage-ops.lib.sh` dispatches on `FULLSEND_FORGE` to source
a per-platform library). The same script gains a second, independent
dispatch on `FULLSEND_TRACKER`, so a harness needing both tracker-side setup
(fetch the JIRA issue) and forge-side setup (git checkout) does both from
one resolved script. Because `FULLSEND_TRACKER` falls back to the resolved
forge platform when `tracker:` isn't set, an unmodified script with only a
`FULLSEND_FORGE` dispatch keeps working unchanged — the tracker dispatch is
additive.

## Consequences

- A code agent harness can declare `tracker.jira` (how to read/comment on
  the triggering issue) alongside `forge.github` (how to open the resulting
  PR) in one file — the two resolve independently against their own
  platform values, with no `forge.jira` entry required or possible.
- Issue-tracking-only harnesses (e.g. triage) can also adopt `tracker:`
  instead of `forge:` for their platform config, dropping the code-hosting
  connotation `forge:` carries even though they never open a PR — but that
  migration is not required by this ADR and can happen per-harness on its
  own schedule.
- The `PlatformConfig` rename and the merge-pipeline generalization are a
  pure internal Go refactor: the YAML surface is unaffected (`forge:` keeps
  its existing field name and shape; only the Go type backing it is
  renamed). Every reference to `harness.ForgeConfig` within
  `internal/harness` needs updating, but this is mechanical and confined to
  this package.
- One shared, section-parameterized implementation backs both `forge:` and
  `tracker:` resolution and composition, so a future bug fix or merge-rule
  change (e.g. the open nil-vs-empty questions from ADR 0045) is made once
  and applies to both, rather than needing to be ported across two
  near-identical code paths.
- Downstream harnesses (e.g. `fullsend-ai/agents`) can add `tracker.jira`
  blocks once this lands; `tracker:` is additive and backward compatible —
  harnesses without it behave exactly as before.
- The Jira dispatch path already exists and works end-to-end today
  ([ADR 0063](0063-polling-based-work-discovery.md), `fullsend poll
  --input-driver jira-poll`) — this ADR does not need to build one.
  Jira-sourced runs currently execute against a synthesized fake GitHub
  issue payload with no tracker awareness at all, tracked as
  [#2264](https://github.com/fullsend-ai/fullsend/issues/2264) and
  [#5989](https://github.com/fullsend-ai/fullsend/issues/5989); a
  `tracker.jira` harness block and a `FULLSEND_TRACKER` derived from the
  dispatch record's `source.system` are the pieces those issues need to
  close.
