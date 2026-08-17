---
title: "88. CEL-guarded overlays in the harness schema"
status: Accepted
relates_to:
  - agent-architecture
  - agent-infrastructure
topics:
  - harness
  - overlays
  - cel
  - forge
  - configuration
---

# 88. CEL-guarded overlays in the harness schema

Date: 2026-08-14

## Status

Accepted

## Context

[ADR 0045](0045-forge-portable-harness-schema.md) added a `forge:` block to
the harness schema, keyed by platform (`github`, `gitlab`), so a single
harness file can carry platform-specific overrides for `pre_script`,
`post_script`, `skills`, `host_files`, `runner_env`, and other fields.
`ResolveForge(platform)` merges the selected platform's block into the
harness's top-level fields and nils out the map.

This design is rigid in two ways:

1. **Fixed conditioning axis.** The only thing you can condition on is the
   forge platform — a value detected from the CI environment or passed via
   `--forge`. There is no way to condition config on the event source
   system (e.g. JIRA vs GitHub), the event type, or any other property of
   the triggering event. This blocks
   [#2264](https://github.com/fullsend-ai/fullsend/issues/2264) and
   [#5989](https://github.com/fullsend-ai/fullsend/issues/5989), where a
   code agent needs JIRA-specific setup scripts when triggered by a JIRA
   issue but GitHub-specific scripts when triggered by a GitHub issue —
   and in both cases writes to the same forge.

2. **Closed key set.** Adding a new conditioning value (e.g. `jira`)
   requires modifying `validForgeKeys` in Go code, even though JIRA is
   not a forge and doesn't belong in `forge:`. Any future conditioning
   axis (event type, repo language, deployment target) would need its own
   top-level block and its own parallel resolution/merge/compose code.

The harness already has a CEL expression engine for `trigger:` expressions
([ADR 0061](0061-harness-cel-dispatch.md)), evaluated against the same
`NormalizedEvent` that carries `source.system`, `entity.kind`, and other
event properties. A general conditional-config mechanism can reuse this
infrastructure.

### Related work

- [ADR 0045](0045-forge-portable-harness-schema.md): Forge-portable harness
  schema — established the forge block, merge rules, and base composition.
- [ADR 0061](0061-harness-cel-dispatch.md): Harness CEL dispatch — CEL
  trigger expressions and `NormalizedEvent` schema.

## Decision

Add an `overlays:` list field to the harness schema. Each entry
has a `when:` CEL expression (same environment as `trigger:`, evaluated
against the `event` variable) and the same override fields as
`ForgeConfig`. At resolution time, all entries whose `when` evaluates to
true are merged into the harness in declaration order, using the same
merge semantics as `mergeForgeConfig` (ADR 0045):

```yaml
overlays:
- when: event.source.system == "github"
  pre_script: scripts/pre-gh.sh
  skills:
    - skills/github-issue-triage
  runner_env:
    GH_TOKEN: ${GH_TOKEN}
- when: event.source.system == "jira"
  pre_script: scripts/pre-jira.sh
  skills:
    - skills/jira-issue-read
  runner_env:
    JIRA_TOKEN: ${JIRA_TOKEN}
```

Multiple entries may match a single event. They are applied sequentially
(first to last). For scalar fields (`pre_script`, `post_script`,
`policy`), later matching entries override earlier ones. For list and map
fields (`skills`, `runner_env`, `providers`, `host_files`, etc.), the same
concatenate/merge semantics from ADR 0045 apply: each matching entry's
values are merged into the accumulating result. This includes
field-specific deduplication where ADR 0045 defines it — for example,
`mergeSkills` deduplicates by basename (a later entry with the same
basename overrides an earlier one). Fields without explicit dedup rules
concatenate without deduplication.

`forge:` and `overlays:` must not coexist in the same harness —
`Validate()` rejects a harness that declares both. `forge:` continues to
work unchanged as a deprecated feature. `Lint()` emits a deprecation
warning when `forge:` is present, recommending migration to
`overlays:`.

### Resolution pipeline

`LoadWithOpts` and `LoadWithBase` gain an `Event map[string]any` field in
their options structs. The pipeline becomes:

```
Unmarshal → validateForge → validateOverlays →
ResolveForge(platform) → ResolveOverlays(event) → Validate
```

`ResolveOverlays(event)` evaluates each entry's `when` against
the event data. Like `ResolveForge`, it nils out the field after
resolution (consumed). When `Event` is nil, `ResolveOverlays` is
a no-op (no entries match), paralleling `ResolveForge` when
`ForgePlatform` is empty.

### Base composition

`mergeBaseIntoChild` concatenates `overlays` lists (base entries
first, child entries appended), the same way it handles `plugins`,
`providers`, and `api_servers`. Since resolution applies entries in
order and later matching entries override earlier ones for scalars,
child entries naturally take precedence over base entries.

The mutual exclusion between `forge:` and `overlays:` applies to the
**post-merge** result — the harness as seen by `Validate()` after
`mergeBaseIntoChild` runs. This means a base harness using `forge:`
and a child using `overlays:` (or vice versa) would produce a merged
harness containing both, which `Validate()` rejects. To migrate
incrementally, the base harness must convert from `forge:` to
`overlays:` before any child can adopt `overlays:`. This is a
deliberate constraint: mixing the two mechanisms in a composed
harness would create ambiguous resolution order between
`ResolveForge` and `ResolveOverlays`.

### Validation

Each `overlays` entry requires:

- A non-empty `when` field that compiles as a CEL expression returning
  bool (same rules as `trigger:`).
- Valid override fields, checked by the same validation logic as
  `validateForge` (script paths are local, URL fields have integrity
  hashes, etc.).

### Deprecation of `forge:`

`forge:` is not removed by this ADR. It continues to work, is validated
and resolved exactly as before, and existing harnesses are unaffected.
The deprecation is advisory:

- `Lint()` warns when `forge:` is present.
- New harnesses should use `overlays:` instead.
- A future ADR may remove `forge:` once all harnesses have migrated.

A `forge:` block like:

```yaml
forge:
  github:
    pre_script: scripts/pre-gh.sh
```

maps to an overlay that conditions on the forge platform:

```yaml
overlays:
- when: forge.platform == "github"
  pre_script: scripts/pre-gh.sh
```

The mapping is mechanical — each forge key becomes a `when` expression
checking `forge.platform` — but note the conditioning axis differs from
`event.source.system`. `forge.platform` reflects the detected forge
platform (from the CI environment or `--forge` flag), while
`event.source.system` identifies the event origin. These diverge for
cross-system events: a JIRA issue triggering work on GitHub Actions has
`forge.platform == "github"` but `event.source.system == "jira"`.

To support this, the overlay CEL environment exposes `forge.platform`
alongside the existing `event` variable, so overlays can faithfully
replicate `forge:` conditioning when needed.

## Consequences

- Harness authors can condition config on any event property, not just
  the forge platform — enabling JIRA-triggered agents, event-type-specific
  setup, and future conditioning axes without schema changes.
- `forge:` is deprecated but remains functional, so existing harnesses
  need no immediate migration.
- The CEL expression engine is already present (`trigger.go`,
  `github.com/google/cel-go`); `overlays` reuses it rather than
  introducing a new expression language or matching mechanism.
- Multiple matching entries compose naturally: a harness can have a
  "GitHub setup" entry and a "JIRA read" entry that both match when a
  JIRA issue triggers a GitHub PR, layering both sets of config onto the
  base harness.
- The harness composition guide (`docs/contributing/harness-composition.md`)
  gains `validateOverlays`, `ResolveOverlays`, and the
  `overlays` concatenation in `mergeBaseIntoChild` as new
  counterpart functions to keep in sync.
