---
title: "151. Use a common human actor floor for CEL and model admission"
status: Accepted
relates_to:
  - agent-architecture
  - security-threat-model
topics:
  - authorization
  - cel
  - data-security
---

# 151. Use a common human actor floor for CEL and model admission

Date: 2026-10-23

## Status

Accepted

This decision partially supersedes the human permission thresholds in
[ADR 0054](0054-require-authorization-on-all-agent-dispatch-paths.md),
[ADR 0098](0098-entity-first-harness-evaluation.md),
[ADR 0133](0133-actor-attributed-data-security-boundary.md), and
[ADR 0141](0141-deterministic-filtered-entity-context-staging.md) for the
target CEL and model-bound paths. Their other authorization, freshness, and
filtering requirements remain.

## Context

[ADR 0061](0061-harness-cel-dispatch.md) puts a platform authorization gate
before harness CEL. The existing GitHub workflow assigns `triage` or `write`
to its stage-guarded routes, with an ungated lifecycle retro-close route
([`reusable-dispatch.yml` at `fe68925b`](https://github.com/fullsend-ai/fullsend/blob/fe68925b53066c27ba0c0272e2e8eb1ce73ebe40/.github/workflows/reusable-dispatch.yml#L305-L420)).
An arbitrary CEL harness has no required stage name. The current Go gate uses
`write` for ordinary events
([`IsAuthorized` at `fe68925b`](https://github.com/fullsend-ai/fullsend/blob/fe68925b53066c27ba0c0272e2e8eb1ce73ebe40/internal/harnessdispatch/auth.go#L38-L68))
before harness matching. An OWNERS upgrade used by that gate is not passed to
CEL
([`Dispatch` at `fe68925b`](https://github.com/fullsend-ai/fullsend/blob/fe68925b53066c27ba0c0272e2e8eb1ce73ebe40/internal/harnessdispatch/core.go#L48-L85)).

[ADR 0098](0098-entity-first-harness-evaluation.md) and
[ADR 0133](0133-actor-attributed-data-security-boundary.md) carried a selected
harness's observation or mutation threshold into entity-fact and model-input
admission. That makes a harness category part of the platform security gate
even though CEL now expresses harness-specific selection.

## Options

Keep stage-derived thresholds, requiring a mapping for every CEL harness; use
`write` for every harness, excluding existing `triage` observation use; or set
one verified platform floor and let each harness express stricter selection in
CEL.

## Decision

For an ordinary human actor, Fullsend requires a verified effective normalized
role of **`triage` or higher** to admit a prompting event, an actor-originated
entity fact that could make CEL actionable, or an actor-attributed source
record into Fullsend-controlled model input. The floor is independent of the
selected harness, its configured agent identity, and its token capabilities.
The effective role comes from the source provider or a trusted configured
permission source such as OWNERS; Fullsend makes that verified effective role
available to CEL through a versioned input contract. A harness CEL predicate
may require `write` or narrower actor, target, or action conditions. It cannot
admit an actor or fact that fails the platform floor.
Raw state used only to conservatively defer eligibility under
[ADR 0147](0147-evaluate-entity-cel-over-selectively-acquired-data.md) is not
admitted as actor authority.

A proven below-floor event is denied; a proven below-floor entity fact is
excluded from the authorized CEL view; and a proven below-floor model record is
redacted. Missing or unverifiable attribution or authority fails closed. A
potentially decision-changing entity fact with unresolved authority cannot
prove a false or empty CEL result. Event admission and each model-bound
checkpoint use fresh authority. Only
[ADR 0148](0148-reuse-verified-actor-roles-for-entity-cel.md)'s bounded
entity-CEL cache may reuse a provider-verified role; it must never cache a
harness-specific authorization decision. Trusted Fullsend provenance permits
an eventless check but does not authorize the actor-originated facts it reads.

[ADR 0107](0107-bot-identity-resolution-for-dispatch-authorization.md)'s
provider-verified registered-bot roles and exact verified label-transition
grant remain separate from the human floor. The label grant covers only its
accepted transition and proven projection, not unrelated history or model
content. The existing ungated GitHub retro-close workflow remains a legacy
compatibility path; it supplies no general exception for CEL-routed harnesses.

The common floor also applies when Fullsend controls snapshot assembly or a
later retrieval result for the model, under ADR 0133's filtering, delimitation,
and fresh exposure checks. A trigger's CEL result is not admission authority
for a separate model record. This decision does not extend the guarantee to
model-initiated direct source-system calls outside Fullsend's control.
[Entity-context-snapshot v1](../normative/entity-context-snapshot/v1/README.md)
retains its historical harness-derived threshold; the changed record-admission
meaning requires a v2 snapshot contract before migration. The
[Authorization Contract v2](../normative/authorization/v2/README.md) specifies
the common floor and its gate outcomes.

## Forge scope

The floor applies to GitHub, GitLab, and Jira through their verified normalized
role mappings. On Jira, Developer and Administrator satisfy `triage` because
its mapping has no separate triage level. Forgejo is a target, but its adapter
must establish a verified effective role before admitting a human actor; an
unsupported or unverifiable mapping fails closed. Source-specific bot and
label grants require their own provider evidence.

## Consequences

- Legacy stage guards and the current Go `write` gate remain in effect until
  migration; lowering the Go gate requires CEL-visible effective roles and
  verified stronger CEL predicates for first-party mutation harnesses.
- A `triage` actor can select a write-capable harness if its CEL predicate has
  no stronger role condition; the harness keeps its separately configured
  capabilities.
- A `triage`-authored record can become model guidance for a write-capable
  harness even when the prompting actor's CEL predicate requires `write`;
  per-record filtering and fixed agent capabilities remain required.
- The versioned event, entity, and snapshot contracts must carry verified
  effective roles and the common floor before implementations claim this
  target behavior.
