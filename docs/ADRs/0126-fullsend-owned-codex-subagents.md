---
title: "126. Fullsend-owned mechanism for Codex sub-agents"
status: Accepted
relates_to:
  - agent-architecture
  - security-threat-model
topics:
  - runtime
  - sub-agents
  - security
---

# 126. Fullsend-owned mechanism for Codex sub-agents

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
[ADR 0100](0100-codex-sandbox-hooks.md) do not cover children. The model chooses the
spawn arguments (context inheritance, model, effort) and can reopen a closed child
through a separate resume tool. The spawn and resume tools carry different names under
each collaboration version (`spawn_agent` and `multi_agent_v1resume_agent` under V1,
`collaborationspawn_agent` under V2 on `rust-v0.157.0`), so a hook keyed on one exact
name misses the others.

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

- An agent delegates when its tools include `Agent` (an absent `tools:` includes it, as
  on Claude Code and pi), harness security is enabled, and the parent model's catalog
  entry selects collaboration V1. Bootstrap then registers its skill personas plus a
  generic `default` and an instruction-only `explore` role. Every other agent, a V2
  parent included, runs with Codex's multi-agent tools off (`[agents] enabled = false`).
- Child models resolve in the order decided on #6970: `subagents.<persona>`, then
  `FULLSEND_CODEX_SUBAGENT_MODEL`, then `subagents.default`, then `gpt-5.6-luna`.
  The environment variable sits above the repository default because it is the
  per-run operator override (a validation pass or a cost cap) and below a persona's
  own entry because that entry is a deliberate per-persona choice. `gpt-5.6-luna` is
  the floor for two reasons: #6970 chose the cheap tier of the current family, and it
  is the only listed model whose catalog entry selects V1 on `rust-v0.157.0`, so a
  catalog that can delegate at all serves it. Bootstrap generates each role file. A
  persona with its own entry carries that model in its role file; every other child
  runs the run-level default, which Bootstrap sets as `agents.default_subagent_model`.
  Only OpenAI model IDs are accepted. Bootstrap reads the pinned CLI's bundled catalog
  (`codex debug models --bundled`, offline) and fails when a resolved child model is not
  in it, because Codex checks the run-level default only at each spawn and a role-file
  model not at all. Persona `model:` and `tools:` frontmatter is reported, not applied.
- A mandatory PreToolUse hook, installed whenever harness security is enabled, even with
  every individual sandbox hook disabled, admits only a V1 spawn of a registered role with
  `fork_context: false` and no model or effort override. It rejects resume and any spawn
  from a child. Its matcher is a pattern on the tool-name suffix (`spawn_agent` or
  `resume_agent`), not an exact name, so it runs for every namespace the CLI prefixes
  (`multi_agent_v1…`, `collaboration…`) and admits only the exact V1 name. Native
  configuration limits depth to one and open children to four.
- Before admitting a spawn, the hook checks `hooks.json` and the role files against
  digests the runner recorded at Bootstrap, as the ADR 0100 adapter does for each
  hook script before invoking it.
- The hook fails closed: a policy violation, a digest mismatch, a missing file,
  unreadable input or any error in the hook denies the spawn by exiting 2 with a reason
  on stderr. Codex lets the call proceed after exit 2 without a reason or any other
  non-zero exit. Bootstrap fails if it cannot install the hook.

## Consequences

- Review and retro can delegate on Codex once the paired instructions ship; a parent
  model whose catalog entry selects collaboration V2 runs in one context, as today.
- Departures from ADR 0104 on Codex: persona `model:` is not applied, the chain adds
  `FULLSEND_CODEX_SUBAGENT_MODEL` and ends at `gpt-5.6-luna` instead of the parent's
  live model, persona `tools:` are instructions only (every child has the parent's
  shell and `apply_patch`), and an unregistered role is always rejected, because a
  delegating agent always has `default` and `explore` registered.
- `code` and `fix` declare no `tools:`, so they also get roles, as on pi.
- The residual is a rewritten checker, not only a race: the hook adapter lives under
  the agent-writable `$CODEX_HOME` and is integrity-checked before each iteration, so
  within an iteration the parent can replace it with one that admits everything,
  digest check included. The spawn-time digest check therefore adds coverage only
  against a mid-iteration role or `hooks.json` edit that leaves the adapter untouched;
  tampering between iterations is caught by the launch-time guard either way. Option 4
  closes this. As in ADR 0100, a handler Codex cannot complete, such as one that times
  out or whose interpreter fails to start, is recorded as failed and does not block.
- Token totals include children; Codex still reports no dollar cost.
- Role loading, child model binding, hook reload, the spawn and resume tool names, the
  catalog's V1 entries and the floor model are revalidated on each Codex CLI bump.

Verified against `rust-v0.157.0` (the sandbox image pin): role-file and `hooks.json`
reload at child start, the spawn arguments, the tool names above, the exact-or-regex
matcher rule, `codex debug models --bundled`, and the catalog's V1 entries.
