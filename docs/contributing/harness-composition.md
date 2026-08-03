# Harness Composition

When modifying merge or path-rewriting functions in the harness
package, you must update **all** counterpart functions that operate on the
same set of fields. These functions form a bidirectional invariant: they
must agree on which harness fields exist and how each field type is
handled. Adding a field to one side without updating the other silently
corrupts harness data during composition or migration.

## Why this matters

The `migrate-customizations` CLI command (ADR-0064) rewrites
path-bearing fields when moving harness files out of `customized/`
directories. If a merge function adds handling for a new field but
the rewrite function is not updated, migrated harnesses lose that
field's path prefix — silently producing a broken harness that
references nonexistent files.

PR #5450 demonstrated the cost of this gap: field-level merge for
`validation_loop` was added to `compose.go` and `forge.go` without
a corresponding update to the path-rewriting side, requiring 6 fix
iterations over 8 days before the PR was closed.

## The invariant

**Any change to a merge function that adds, removes, or changes
field-level handling must be mirrored in the corresponding
path-rewriting functions.**

This applies in both directions: a new field in a rewrite function
also requires a matching update to the merge function if the field
participates in composition.

## Paired functions

The following functions must stay in sync. When you modify one, check
and update the others as needed.

### Merge side (harness composition)

| Function | File | Purpose |
|----------|------|---------|
| `mergeBaseIntoChild` | `internal/harness/compose.go` | Merges base harness fields into child during `base:` composition |
| `mergeForgeConfig` | `internal/harness/forge.go` | Applies `forge.<platform>` overrides onto top-level harness fields |
| `mergeForgeConfigInto` | `internal/harness/compose.go` | Merges base `ForgeConfig` fields into child `ForgeConfig` during `base:` composition |
| `mergeSkills` | `internal/harness/compose.go` | Deduplicates skills by basename (base + child) |
| `mergeHostFiles` | `internal/harness/compose.go` | Deduplicates host files by dest path (base + child) |
| `mergeForgeBlocks` | `internal/harness/compose.go` | Merges `forge:` maps key-by-key across base and child |

### Path-rewriting side (migration)

| Function | File | Purpose |
|----------|------|---------|
| `rewriteCustomizedPaths` | `internal/cli/migrate.go` | Strips `customized/` prefix from all path-bearing fields during migration |
| `rewriteEnvMap` | `internal/cli/migrate.go` | Strips `customized/` path segments from env map values |
| `rewriteHarnessContent` | `internal/cli/migrate.go` | Entry point: parses YAML, calls `rewriteCustomizedPaths`, re-marshals |

### How they correspond

The merge functions define which fields participate in harness
composition and how each field type is merged (scalar override, list
append, map merge, struct replace). The path-rewriting functions must
know about the same set of path-bearing fields so they can transform
paths correctly during migration.

For example, if `mergeBaseIntoChild` gains handling for a new
`foo_script` scalar field, then `rewriteCustomizedPaths` must also
strip the `customized/` prefix from `h.FooScript`. If
`mergeForgeConfigInto` gains handling for a new field inside
`ForgeConfig`, then the `for _, fc := range h.Forge` loop in
`rewriteCustomizedPaths` must also handle that field.

## Checklist for harness field changes

When adding or modifying a field in the `Harness` or `ForgeConfig`
structs:

1. **Determine the field type.** Is it a scalar, list, map, or pointer
   struct? This determines the merge behavior (see ADR-0045 inheritance
   rules).
2. **Update `mergeBaseIntoChild`** if the field participates in `base:`
   composition.
3. **Update `mergeForgeConfig`** if the field can appear under
   `forge.<platform>` blocks.
4. **Update `mergeForgeConfigInto`** if the field appears in
   `ForgeConfig` and participates in `base:` composition of forge
   blocks.
5. **Update `rewriteCustomizedPaths`** if the field is path-bearing
   (contains file paths that need prefix stripping during migration).
   This includes both the top-level harness fields and the per-forge
   loop.
6. **Update tests** in `compose_test.go`, `forge_test.go`, and
   `migrate_test.go` to cover the new field in all affected functions.

## Related

- [ADR-0045](../ADRs/0045-forge-portable-harness-schema.md): Forge-portable
  harness schema — defines the merge/inheritance rules
- [ADR-0064](../ADRs/0064-deprecate-customized-directory-overlay.md):
  Deprecate customized directory overlay — motivation for the
  `migrate-customizations` command
- Issue #5579: Harness field integration pipeline (complementary
  checklist covering the broader field addition workflow)
