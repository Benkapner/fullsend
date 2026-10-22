---
title: "151. Use harness human-role minimums for actor admission"
status: Accepted
relates_to:
  - agent-architecture
  - security-threat-model
topics:
  - authorization
  - cel
  - data-security
---

# 151. Use harness human-role minimums for actor admission

Date: 2026-10-23

## Status

Accepted

This decision partially supersedes the human permission thresholds in
[ADR 0054](0054-require-authorization-on-all-agent-dispatch-paths.md),
[ADR 0098](0098-entity-first-harness-evaluation.md),
[ADR 0133](0133-actor-attributed-data-security-boundary.md), and
[ADR 0141](0141-deterministic-filtered-entity-context-staging.md) for the
target CEL and model-bound paths. It also supersedes [ADR
0061](0061-harness-cel-dispatch.md)'s authorization-before-harness-
enumeration ordering and narrows the human label-transition exception in
[ADR 0107](0107-bot-identity-resolution-for-dispatch-authorization.md) for a
harness requiring more than `triage`.
Their other provenance, bot, freshness, and filtering requirements remain.

## Context

[ADR 0061](0061-harness-cel-dispatch.md) puts platform authorization before
harness CEL. A harness's `role` selects its run identity: GitHub requests a
mint token for it, while GitLab selects registered role credentials
([`run.go` at `fe68925b`](https://github.com/fullsend-ai/fullsend/blob/fe68925b53066c27ba0c0272e2e8eb1ce73ebe40/internal/cli/run.go#L903-L915)).
The existing GitHub workflow uses `triage` for human triage/review slash
commands and `write` for human code/fix/retro/prioritize slash commands;
its other event routes have distinct guards, including an ungated retro-close
route ([`reusable-dispatch.yml` at `fe68925b`](https://github.com/fullsend-ai/fullsend/blob/fe68925b53066c27ba0c0272e2e8eb1ce73ebe40/.github/workflows/reusable-dispatch.yml#L305-L420)).
The current Go path instead applies one `write` gate before loading
harnesses ([`Dispatch` at `fe68925b`](https://github.com/fullsend-ai/fullsend/blob/fe68925b53066c27ba0c0272e2e8eb1ce73ebe40/internal/harnessdispatch/core.go#L48-L85)).

A common `triage`-only admission rule would allow `triage`-authored model
records to guide a write-capable harness even if its trigger CEL required a
`write` prompting actor. A mint role can provide a compatibility default for
human authorization, but the minted token's permission set does not itself
define which human actors may prompt or supply data to the harness.

## Options

Keep one `triage` threshold for every harness and rely on CEL for stricter
routing; derive a threshold from token permissions; or use an explicit default
mapping from mint role with a configurable, platform-enforced harness minimum.

## Decision

For ordinary human actors, Fullsend retains a non-waivable `triage`-level
authorization gate. The narrow verified label-transition grant below can
satisfy that gate only for the exact transition and its proven projection;
it does not establish a normalized human role. For each trusted harness,
Fullsend sets its `human_min_role` from an explicit top-level harness field
or, when omitted, from the harness's `role`: `triage` and `review` default
to `triage`; every other role, including custom roles, defaults to `write`.
An explicit value may be `triage`, `write`, `maintain`, or `admin`. It can
lower a `write` default to `triage` but cannot admit `read`, `none`, or
`external` actors. An invalid explicit value makes that harness ineligible;
it never falls back to the default. Resolve the field after trusted `base`
composition. Event-dependent overlays, CEL, and forge-specific fields cannot
alter it.
The default is an authorization policy, not a deduction from mint token scopes
or the harness's `privilege_levels`. The trusted composed harness revision and
effective minimum stay fixed for one evaluation and run cycle; a changed
policy requires a new cycle or abort, not a silent threshold change.

Fullsend applies the same effective minimum separately to the ordinary human
prompting event before that harness's CEL predicate; to each actor-originated
entity fact needed for an actionable CEL result, including aggregates and
absence claims; and to each Fullsend-controlled model-bound source record at
ADR 0133's fresh exposure checkpoints. The record check uses its own current
revision actor, never the event sender or discovery credential. A fact below
the selected harness's minimum is excluded from its authorized CEL view; an
unresolved decisive fact leaves the result unknown rather than proving false
or empty. An event or model record below the minimum is denied or redacted.
Trusted Fullsend provenance can authorize eventless discovery but does not
authorize actor-originated entity facts or model records. CEL may narrow
selection beyond the platform gate, never relax it.

A verified human label addition supplies a narrow `triage`-equivalent grant
for that exact accepted transition and its proven projection. It neither
sets `actor.role` nor makes `actor.role_verified` true. If the selected
harness requires `write`, `maintain`, or `admin`, the human labeler must
also have a verified effective role meeting that minimum. The
provider-classified bot label handoff and registered-bot rules in ADR 0107
remain separate from human
thresholds; neither label grant admits unrelated history or model content.
The `human_min_role` field does not constrain bots; harness CEL may narrow
their routing.
The ungated GitHub retro-close route remains legacy compatibility, not a
general CEL-path exception.

Effective human roles come from the source provider or a trusted configured
permission source such as OWNERS. A failed forge-role lookup does not negate
an independently verified OWNERS grant for the same actor and target.
Fullsend exposes a verified effective role and its provenance to CEL when
present, and exposes an exact human label grant as separate provenance without
synthesizing a role. [ADR 0148](0148-reuse-verified-actor-roles-for-entity-cel.md)
may cache a bounded provider-verified entity-CEL role, but each harness
reapplies its own minimum; no harness-specific authorization decision or
filtered view is shared as a role verdict. Event and model-bound checks remain
fresh. The [Authorization Contract
v2](../normative/authorization/v2/README.md) specifies these gates and their
audit outcomes.

## Forge scope

The human minimum applies to GitHub, GitLab, and Jira through verified
normalized role mappings. On Jira, Developer and Administrator satisfy
`triage` because its mapping has no separate triage level. Forgejo is a
target, but its adapter must establish a verified effective role before
admitting a human actor through the role-based path; unsupported or
unverifiable mappings fail closed.
Source-specific bot and label grants require their own provider evidence.

## Consequences

- The current Go `write` gate and bash routes remain compatibility behavior
  until dispatch can load trusted harness policy, apply each minimum before
  its CEL predicate, and expose verified effective roles or exact label-grant
  provenance to CEL. The
  `human_min_role` field is target-only until the loader validates and enforces
  it.
- A harness author may deliberately set `human_min_role: triage` for a
  write-capable role; that permits `triage` actors and records to guide that
  harness without changing its minted capabilities.
- Authorized entity views, aggregate evidence, and audits are bound to the
  selected harness's minimum; verified actor roles may be reused, but pass
  decisions and filtered views cannot be reused across different minima.
- [Entity-context-snapshot v1](../normative/entity-context-snapshot/v1/README.md)
  retains its category-derived `triage`/`write` threshold. Snapshot admission
  with a differing override or a higher role requires a versioned contract
  before migration.
