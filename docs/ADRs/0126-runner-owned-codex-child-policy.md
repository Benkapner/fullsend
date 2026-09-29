---
title: "126. Runner-owned policy for native Codex children"
status: Accepted
relates_to:
  - agent-architecture
  - security-threat-model
topics:
  - runtime
  - sub-agents
  - security
---

# 126. Runner-owned policy for native Codex children

Date: 2026-09-29

## Status

Accepted

## Context

Review and retro dispatch sub-agents with fresh contexts and selected personas
([agent architecture](../problems/agent-architecture.md)). On Codex they run in one
context today: fullsend registers no roles, although Codex already offers its native
`spawn_agent` tool to parent models whose catalog entry selects collaboration V1
([#6970](https://github.com/fullsend-ai/fullsend/issues/6970)).

[ADR 0104](0104-per-persona-model-resolution.md) makes child models a runner decision
and planned Codex support for `agents[].subagents`. Its persona frontmatter does not
carry over: `model:` values are Claude aliases, and Codex has no per-child tool
allowlist, so ADR 0104's rule that an unservable `tools:` list fails Bootstrap would
reject every review persona. Codex also rereads a role's file and `$CODEX_HOME/hooks.json`
whenever a child starts, so the launch-time checks of
[ADR 0100](0100-codex-sandbox-hooks.md) do not cover children, and the model chooses
the spawn arguments: context inheritance, model, effort and resume.

## Options

1. **Single context (status quo).** No new mechanism; review and retro keep no
   independent child contexts on Codex.
2. **Native children governed by instructions only.** Nothing enforces fresh context,
   depth or model choice; a parent can fork its conversation into the challenger or
   pick any OpenAI model.
3. **Native children under a runner-owned role and dispatch policy** (chosen).
4. **Option 3 plus write protection of the runtime's files.** Closes the
   time-of-check window noted below. Each way to do it adds a platform requirement:
   Landlock needs kernel 6.2+ and stops new top-level `$HOME` entries; a read-only
   OpenShell path is fixed at sandbox
   creation, before the per-run files exist; roles baked into the image tie them to
   image releases. A separate decision if the residual below is not acceptable.

## Decision

Codex children run under a policy the runner provisions and enforces.

- An agent delegates only when its tools include `Agent` (an absent `tools:` includes
  it, as on Claude Code and pi), its merged `agents[]` entry names a model under at
  least one `subagents` key, and harness security is enabled. Bootstrap then registers
  its skill personas plus a generic `default` and an instruction-only `explore` role.
  Other agents run with Codex's multi-agent tools off.
- Child models resolve as in ADR 0104 without the frontmatter step:
  `subagents.<persona>`, then `subagents.default`, then the parent's live model, with
  no Codex-specific default model or environment override. Bootstrap generates each
  role file and writes a configured model into it; a role with nothing configured
  carries no model, so its child inherits the parent's. Only OpenAI model IDs are
  accepted, and an explicit model that cannot be served fails rather than falling
  back. Persona `model:` and `tools:` frontmatter is reported, not applied.
- A mandatory PreToolUse hook, installed whenever harness security is enabled, even with
  every individual sandbox hook disabled, admits only a V1 spawn of a registered role with
  `fork_context: false` and no model or effort override. It rejects resume and any spawn
  from a child. Native configuration limits depth to one and open children to four.
- Before admitting a spawn, the hook checks `hooks.json` and the role files against
  digests the runner recorded at Bootstrap, as the ADR 0100 adapter does for each
  hook script before invoking it.
- The hook fails closed: a policy violation, a digest mismatch, a missing file,
  unreadable input or any error in the hook denies the spawn with exit 2 and a reason,
  the only exit Codex treats as a block. Bootstrap fails if it cannot install the hook.

## Consequences

- Review and retro can delegate on Codex once the paired instructions ship and their
  entries name a `subagents` model; a parent model whose catalog entry selects
  collaboration V2 cannot delegate under this policy.
- Departures from ADR 0104 on Codex: persona `model:` is not applied, persona `tools:`
  are instructions only (every child has the parent's shell and `apply_patch`), and an
  unregistered role is always rejected, because a delegating agent always has `default`
  and `explore` registered.
- An agent whose entry names no `subagents` model stays single-context on Codex, unlike
  on Claude Code and pi.
- The spawn-time check has ADR 0100's residuals: a change that lands between the check
  and the child's load goes undetected within the iteration, and a handler Codex cannot
  complete, such as one that times out or whose interpreter fails to start, is recorded
  as failed and does not block.
- Token totals include children, attributed per native thread; Codex still reports no
  dollar cost.
- Role loading, child model inheritance, hook reload and collaboration tool names are
  revalidated on each Codex CLI bump.
