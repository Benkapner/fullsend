---
title: "87. Eval measurements as online trace scoring with portable export"
status: Accepted
relates_to:
  - operational-observability
  - testing-agents
topics:
  - observability
  - evaluation
  - opentelemetry
---

# 87. Eval measurements as online trace scoring with portable export

Date: 2026-08-10

## Status

Accepted

Visual companion: [explainer diagrams](0087-eval-measurements-explainer.html).

## Context

Agent runs already emit OpenTelemetry traces as `run-telemetry.jsonl`, with
optional live OTLP export when `OTEL_EXPORTER_OTLP_*` is set
([ADR 0050](0050-distributed-tracing-instrumentation.md)). Separately,
[ADR 0051](0051-agent-eval-harness-for-test-infrastructure.md) owns the
**functional** eval harness: curated fixtures / scenarios in
`fullsend-ai/agents` `eval/<agent>/` that gate agent PRs. Those fixtures do
not score wild production runs.

Operators also need an **online / trend** layer on wild traces (completeness
first; quality signals later) without a second, product-specific export stack.
Dogfood showed MLflow Assessments improve one backend's Quality UI, but
requiring `MLFLOW_TRACKING_*` breaks ADR 0050's portability promise for scores.

Adjacent telemetry proposals (not competing with this score path):

- **Level 3 content capture** ([#5947](https://github.com/fullsend-ai/fullsend/pull/5947)
  — proposed ADRs 0084/0085): richer span content enables later scorers;
  v1 reads Level 1/2 metadata in `run-telemetry.jsonl`. Content is OTLP-only
  under that proposal, so content-aware scorers will need OTLP/backend access
  or a widened local contract. Measure CLI is host-side after sandbox exit
  (outside the sandbox OTEL denylist surface).
- **Span status from run outcome** ([#5944](https://github.com/fullsend-ai/fullsend/pull/5944)):
  OTLP Status (and `fullsend.transcript_error`) become the reliable
  success/failure signal. EM-001 only checks that `exit_code` is **present**
  (fitness). Outcome scorers must key on Status, not `exit_code == 0`.
- **Observer / lessons → fixtures** ([#2423](https://github.com/fullsend-ai/fullsend/pull/2423)):
  narrative analysis and golden-set promotion. This ADR is same-job
  deterministic scoring on traces.

## Options

1. **Local JSONL only** — simplest; no remote scores on any backend.
2. **Backend-native APIs only** (e.g. MLflow Assessments) — good UX on one
   host; every new backend needs a new adapter and secrets.
3. **Same OTLP path as agent traces, plus optional adapters** — scores travel
   with the same endpoint/headers orgs already configure; adapters are
   opt-in for product UIs.

## Decision

Introduce **eval measurements**: deterministic scorers that read
`run-telemetry.jsonl` after `fullsend run` in the **same** managed job
(`fullsend eval-measure` in `action.yml`), **fail-open**.

**Not fixtures:** functional eval scenarios remain ADR 0051 / `eval/<agent>/`.
Measurements never block delivery. Prefer the glossary terms *eval
measurement* (score) and *eval scenario* (fixture).

**Ownership:** `fullsend` owns parser, scorers, CLI, and job wiring.
`fullsend-ai/agents` owns per-agent **measurement manifests** at
`eval/measurements/<agent>.yaml` (which scorers to enable).

**Persistence (always):** write `eval-measurements.jsonl` (and a small ledger)
next to `run-telemetry.jsonl`. Scores are data, never gates.

**Remote export:** the portable path will use the same `OTEL_EXPORTER_OTLP_*`
as ADR 0050 (not yet implemented). Backend-specific adapters (e.g. MLflow
Assessments) are **optional** and available now. Adapters must not be required
for scores to leave the runner once the OTLP path lands.

**First scorer:** `trace_fitness` (catalog id `em-001`) — span-tree /
attribute fitness so later scorers can trust the trace. Further scorers
(budget, outcome/status, content-aware) land via manifests without changing
this export model.

### Versioning (per measurement, not platform “v1”)

There is no product-wide “eval measurements v1” switch. “First ship” just
means only one scorer is enabled yet. Each manifest entry carries:

| Field | Meaning |
|---|---|
| `id` | Stable catalog id (`em-001`). New measurement concept → new id. |
| `scorer` | Go dispatch name (`trace_fitness`). |
| `version` | Integer **contract** version of that measurement’s checks / pass rule. |

Scores and the idempotency ledger key on `id@version` (e.g. `em-001@1`).
Bump `version` when pass/fail semantics change so trends do not mix eras.
Add a check that does not change the pass definition → same version is fine.
Entirely new signal → new `em-NNN` (and usually a new `scorer` string).

## Consequences

- Wild runs produce a reviewable score file beside telemetry with or without a
  remote backend.
- Orgs that already set OTEL for traces get a portable score export contract
  without a second auth scheme; optional adapters may enrich one product UI.
- Missing manifests skip cleanly; measure failure never fails the agent job.
- Functional scenarios (gate) and eval measurements (trend) stay separate;
  retro/observer lesson extraction may still promote production cases into
  fixtures without replacing online scoring.
- Retro can recommend a **manifest scorer** or a **scenario fixture** — not
  substitutes.
- Richer telemetry (Level 3 / Status fixes) expands what scorers *can* assert;
  it does not replace this same-job path.
