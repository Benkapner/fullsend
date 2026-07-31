---
title: "80. config.yaml vs. agent env vars: where a config option belongs"
status: Accepted
relates_to:
  - agent-infrastructure
  - governance
topics:
  - configuration
  - harness
  - conventions
---

# 80. config.yaml vs. agent env vars: where a config option belongs

Date: 2026-07-31

Amends: [ADR 0049](0049-agent-configuration-env-var-convention.md)

## Status

Accepted

## Context

[fullsend-ai/agents#567](https://github.com/fullsend-ai/agents/pull/567)
added `TRIAGE_AUTO_CODE` as a harness `env.runner` default in
`harness/triage.yaml`. A reviewer flagged that fullsend-ai/fullsend#1754,
the issue that requested the knob, asked for it to "live in the per-repo/
per-org config surface" — i.e. `.fullsend/config.yaml` — not a harness
default (see the [review
discussion](https://github.com/fullsend-ai/agents/pull/567#discussion_r3686020058)).
`config.yaml` already carries fields like `create_issues.allow_targets`
that are unrelated to any single agent. ADR 0049 defines how agent config
env vars are *named* (`{AGENT}_{SETTING_NAME}`) but says nothing about
*when* a knob should be a `config.yaml` field instead of an env var, so
there was no rule to check the PR against.

## Decision

A config option belongs in exactly one of the two surfaces, based on
whether its behavior is meaningful to more than one agent:

- **Applies uniformly across agents (or to dispatch/policy behavior, not a
  specific agent's inference-time logic):** it is a `config.yaml` field.
  It gets a plain name with no `{AGENT}_` prefix, and it is not also
  settable via environment variable — `config.yaml` (`internal/config`
  accessors) is the single source of truth. `roles`, `auto_merge`, and
  `create_issues.allow_targets` are existing examples.
- **Tunes the behavior of one specific agent:** it is an `{AGENT}_`-prefixed
  env var per ADR 0049, delivered via that agent's `env.runner`/
  `env.sandbox`. It is not also settable as a `config.yaml` field —
  overriding it per repo or org means overriding the harness (e.g. via
  `base:` composition, per ADR 0045), not adding a parallel field to
  `config.yaml`.

A knob only moves from one surface to the other by a deliberate migration,
not by adding a second way to set the same value. Applying this rule to
`TRIAGE_AUTO_CODE`: it governs one agent's (triage's) behavior, so it
correctly belongs in `harness/triage.yaml` `env.runner`, not
`config.yaml`. fullsend-ai/fullsend#1754's "per-repo/per-org config
surface" request is satisfied by documenting the existing harness override
path (`base:` composition or an org/repo harness copy), not by adding a
`config.yaml` field — the gap the reviewer found is a documentation gap,
not a placement gap.

## Consequences

- Resolves the fullsend-ai/agents#567 ambiguity: `TRIAGE_AUTO_CODE` stays
  an env var; `docs/agents/<agent>.md`'s Variables table must state how to
  override it (which harness layer to edit), matching the guidance ADR
  0049 already expects.
- `config.yaml` cannot accumulate `{agent}_foo`-style fields — any such
  field is a signal the knob was misplaced.
- An env var cannot quietly gain a `config.yaml` mirror with its own
  precedence rules; there is one settable location per config option.
- A knob whose scope grows from one agent to several requires a new
  decision (an ADR update or explicit review), not a silent field
  addition to `config.yaml`.
