---
title: "86. Conversation surface for agent participation"
status: Accepted
relates_to:
  - agent-architecture
  - agent-infrastructure
  - security-threat-model
topics:
  - discussions
  - dispatch
  - conversation
  - tracker
  - portability
  - slash-commands
---

# 86. Conversation surface for agent participation

Date: 2026-08-11

## Status

Accepted

Builds on [ADR 0005](0005-forge-abstraction-layer.md),
[ADR 0017](0017-credential-isolation-for-sandboxed-agents.md) /
[ADR 0046](0046-host-side-api-server-design.md),
[ADR 0054](0054-require-authorization-on-all-agent-dispatch-paths.md),
[ADR 0058](0058-agent-registration.md),
[ADR 0061](0061-harness-cel-dispatch.md),
[ADR 0063](0063-polling-based-work-discovery.md), and
[ADR 0076](0076-slash-command-entity-context-separation.md).
Aligns with the `tracker.Client` split
([#5988](https://github.com/fullsend-ai/fullsend/issues/5988)): domain
interfaces stay narrow; `forge.Client` remains git-hosting only.

## Context

Agents already act on forge **work items** and **change proposals** via slash
commands and host/post-script comments. GitHub Discussions (and later Slack,
Discord, Matrix, GitLab discussions) are a different surface: threaded community
conversation that must not be shoehorned into `work_item` without breaking
entity-context rules ([ADR 0076](0076-slash-command-entity-context-separation.md)).

`forge.Client` is scoped to git-hosting. Issue content for non-forge backends
(Jira) already moves to `tracker.Client` rather than expanding the forge
interface. Slack/Discord/Matrix are likewise not forges — conversation
read/write must not grow `forge.Client`.

Discussions also introduce **categories** (partition + format/permission
policy) distinct from **labels** (multi-tag triage). The event model must
expose both without collapsing them.

## Options

### A. Special-case GitHub Discussions in workflows

Fastest for GitHub; GitHub-centric and duplicates auth/writeback.

### B. Always-on chat bot outside the execution stack

Low latency; bypasses unidirectional control, mint/OIDC identity, and harness
registration ([ADR 0016](0016-unidirectional-control-flow.md)).

### C. Extend `forge.Client` with Conversation/Message methods

Pulls non-forge chat into the forge package and fights the `tracker.Client`
split.

### D. Portable Conversation surface via `conversation.Client` (chosen)

Parallel to `tracker.Client`: narrow domain interface + dispatch/event reuse,
with per-backend adapters.

## Decision

Adopt **Option D**.

### Domain model

| Relation | Cardinality | Notes |
|----------|-------------|--------|
| Category → Conversation | **1:M** | Every conversation has exactly one category (GitHub requires it; Slack/Discord map channel → category). |
| Conversation ↔ Label | **M:M** | Same triage tags as issues when the backend supports them (GitHub Discussions do). |
| Conversation → Message | **1:M** | Messages (comments/replies) belong to one conversation. |
| Message → Category / Label | **none** | Messages inherit routing context from their parent conversation; they are not categorized or labeled independently. |

**Category ≠ label.** Categories are a single exclusive partition with
backend-defined format/permission semantics (e.g. GitHub announcement =
maintain/admin create; Q&A = answerable). Labels are additive tags on the
conversation. Adapters must not encode the category name as a synthetic label.

**Permissions.** Platform authorization remains
[ADR 0054](0054-require-authorization-on-all-agent-dispatch-paths.md) (effective
repo/collaborator role on `actor`). Category format is **routing and policy
metadata** for harness CEL (e.g. only vouch agents match
`category.slug == "vouch-request"`), not a second auth system. Backends may
still reject creates/replies the actor cannot perform; that is adapter error
handling.

### Architecture seams

1. **`conversation.Client`:** `internal/conversation` with Conversation/Message
   APIs (and category metadata on Conversation). GitHub Discussions first;
   Slack/peers implement directly. **Not** on `forge.Client` or
   `tracker.Client`.
2. **`NormalizedEvent`:** `entity.kind: conversation`. Require
   `state.conversation.category` (`name` required; `id`/`slug`/`format`
   optional). Reuse `state.labels` for conversation labels only. Message
   events use `transition.kind: comment_added` with `transition.comment`;
   category and labels still describe the **parent conversation**. See
   [normative v1](../normative/normalized-event/v1/).
3. **Ingress / egress / identity / security:** shim + dispatch drivers;
   host-mediated writeback via `conversation.Client` (tier 1 default, host API
   for mid-run); least-privilege Discussions/chat scopes; conversation bodies
   untrusted; entity-context rules keep code-mutating slash commands off
   conversations ([ADR 0076](0076-slash-command-entity-context-separation.md)).

## Consequences

- Harnesses route with CEL on both `state.conversation.category` and
  `state.labels` (e.g. category selects the agent; labels refine).
- Slack becomes input-driver + `conversation.Client` adapter work; channel maps
  to category, thread maps to conversation.
- Domain split: `forge.Client` / `tracker.Client` / `conversation.Client`.
- Linking a conversation to a work item for `/fs-code` remains a follow-on.
