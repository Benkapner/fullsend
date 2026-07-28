---
title: "74. Local-path support for profiles and providers"
status: Accepted
supersedes:
  - "0070"
relates_to:
  - agent-architecture
  - agent-infrastructure
  - security-threat-model
topics:
  - harness
  - providers
  - profiles
  - portability
  - remote-resources
---

# 74. Local-path support for profiles and providers

Date: 2026-07-17

## Status

Accepted

Supersedes [ADR 0070](0070-portable-provider-profile-resolution.md)

## Context

[ADR 0070](0070-portable-provider-profile-resolution.md) introduced URL-based resolution
for `openshell.profiles` and `providers` in harness composition, but restricted profiles
to URL-only (no local-path form). Every other harness resource field — agent, policy,
skills, scripts, host_files — supports local file paths resolved relative to the harness
directory or inherited as absolute cache paths from `base:` composition.

This asymmetry means:
- A harness that uses `base:` composition cannot declare local profiles alongside
  URL-fetched ones
- Developers cannot iterate on profile definitions locally before publishing them
  to a remote repository
- The `ResolveRelativeTo` path that absolutizes local references for other fields
  has no effect on profiles

Providers had partial local support (bare names loaded from `providers/` dir) but
lacked local file-path resolution — a provider YAML at `providers/custom.yaml`
could not be referenced directly in the harness, only discovered by convention.

## Decision

Extend [ADR 0070](0070-portable-provider-profile-resolution.md)'s schema to allow
local file paths for both `openshell.profiles` and `providers`, matching the
resolution behavior of all other harness resource fields.

1. **`openshell.profiles`** — Accept HTTPS URLs with `#sha256=...` integrity hashes
   (existing), **and** local file paths resolved relative to the harness directory
   or inherited as absolute cache paths from `base:` composition.

2. **`providers`** — Accept local provider names (existing), **local file paths**
   resolved relative to the harness directory, and remote HTTPS URLs with
   `#sha256=...` hashes. Mixed forms allowed in the same list.

### Schema

```yaml
# openshell.profiles field (local paths or URLs)
openshell:
  profiles:
  - profiles/claude-code.yaml      # Local path (resolved relative to harness)
  - "https://github.com/org/profiles/tree/main/claude-code.yaml#sha256=abc..."

# Extended providers field (mixed local names, paths, and URLs)
providers:
  - "my-local-provider"  # Local name: loaded from providers/my-local-provider.yaml
  - providers/custom.yaml  # Local path (resolved relative to harness)
  - "https://github.com/org/repo/tree/main/providers/my-provider.yaml#sha256=789..."  # Remote
```

### Distinguishing provider entry forms

`IsProviderPath(s)` returns true when a string contains `/` or ends with `.yaml`/`.yml`,
distinguishing file paths from bare provider names. This heuristic is used by the
lock-file strip and the `hasLocalProviders` gate to determine which entries need
file-based resolution vs. directory-based lookup.

### Resolution flow

Unchanged from ADR 0070, with these additions:

**Phase 1 — Base composition (`compose.go`)**

Two new functions mirror existing resolution for other fields:
- `resolveBaseProfiles` — fetches relative profile paths from URL-referenced bases
- `resolveBaseProviders` — fetches relative provider paths from URL-referenced bases
  (bare provider names are skipped)

Both use `isFullsendCachePath` as the skip guard (matching sibling functions) and
`validateBaseRelPath` for path safety.

**Phase 2 — Resource resolution (`resolve.go`)**

`ResolveHarness` adds handling for local file paths:
- Profile entries that are absolute paths: read and parse as profile YAML
- Provider entries that pass `IsProviderPath`: read and parse as provider def YAML
- Bare provider names: left unchanged (resolved from `providers/` dir in `run.go`)

### Lock-file interaction

When a harness has both URL resources (with lock deps) and local-path
profiles/providers (without lock deps), the lock strip must preserve local-path
entries. The strip removes only URL entries (`!IsURL`), not path-shape entries.
The second `ResolveHarness` pass processes whatever remains, and
`dedupResolvedProfiles`/`dedupResolvedProviders` handle any overlap.

## Validation

### Schema validation (`ValidateResourceTypes`)

- `openshell.profiles[]`: if `IsURL()`, require a valid `#sha256=...` integrity
  hash. Otherwise, accept as a local file path.
- `providers[]`: if `IsURL()`, require `#sha256=...` integrity hash. If not URL,
  accept as local provider name or file path (no change to wire format).

### File existence validation (`ValidateFilesExist`)

- Local profile paths: validated at harness load time
- Local provider paths (entries matching `IsProviderPath`): validated at harness
  load time
- Bare provider names: not validated (resolved from directory at runtime)

### Content and referential integrity

Unchanged from ADR 0070.

## Security

Same controls as ADR 0070 and ADR 0038. Local file paths go through
`ValidateFilesExist` and are confined to the workspace by upstream guards
(`ResolveRelativeTo`, `validateBaseRelPath`).

## Backwards compatibility

Fully backwards-compatible with ADR 0070:

- Harnesses using URL-only profiles continue to work unchanged
- Harnesses using bare provider names continue to work unchanged
- The new local-path forms are opt-in additions to the existing `[]string` fields

## Consequences

- Profiles and providers now have the same resolution flexibility as every other
  harness resource field, eliminating the asymmetry from ADR 0070.
- Developers can iterate on profiles locally before publishing to remote
  repositories.
- Base-composed harnesses can mix local and URL-referenced profiles/providers.
- The `IsProviderPath` heuristic introduces a naming constraint: bare provider
  names must not contain `/` or end with `.yaml`/`.yml` (existing provider names
  already follow this convention).
