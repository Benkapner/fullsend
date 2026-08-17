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

## Context

Agent runs already emit OpenTelemetry traces as `run-telemetry.jsonl`, with
optional live OTLP export when `OTEL_EXPORTER_OTLP_*` is set
([ADR 0050](0050-distributed-tracing-instrumentation.md)). Separately,
[ADR 0051](0051-agent-eval-harness-for-test-infrastructure.md) owns the
**functional** eval harness: curated fixtures / scenarios in
`fullsend-ai/agents` `eval/<agent>/` that gate agent PRs. Those fixtures do
not score wild production runs.

Operators also need an **online / trend** layer on wild traces (completeness
first; quality signals later). Fullsend must stay **backend-agnostic**: orgs
already choose Phoenix, MLflow, Jaeger, or another OTLP collector for traces.
Baking a single product’s Assessments/Quality API into the core CLI or managed
workflows would force a tool decision on every install.

Adjacent telemetry proposals (not competing with this score path):

- **Level 3 content capture** ([#5947](https://github.com/fullsend-ai/fullsend/pull/5947)
  — proposed ADRs 0084/0085): richer span content enables later scorers;
  first ship reads Level 1/2 metadata in `run-telemetry.jsonl`. Measure CLI is
  host-side after sandbox exit.
- **Span status from run outcome** ([#5944](https://github.com/fullsend-ai/fullsend/pull/5944)):
  OTLP Status (and `fullsend.transcript_error`) become the reliable
  success/failure signal. EM-001 only checks that `exit_code` is **present**
  (fitness). Outcome scorers must key on Status, not `exit_code == 0`.
- **Observer / lessons → fixtures** ([#2423](https://github.com/fullsend-ai/fullsend/pull/2423)):
  narrative analysis and golden-set promotion. This ADR is same-job
  deterministic scoring on traces.
- **Harness snapshot / forge join keys** ([#5524](https://github.com/fullsend-ai/fullsend/pull/5524)
  — proposed ADR 0075): sibling artifact for harness fingerprint and
  forge/CI pointers beside telemetry. Complementary join/identity layer;
  primary run facts belong on the OTEL trace (Level 1), while measurements
  stay a derived sibling file.

## Options

1. **Local JSONL only** — portable offline artifact; no remote scores from
   fullsend itself.
2. **Backend-native APIs in core** (e.g. one vendor’s Assessments API) —
   couples every managed workflow to that product’s auth and schema.
3. **Local JSONL + same OTLP path as agent traces for remote** — scores travel
   with the endpoint/headers orgs already configure for ADR 0050; no second
   vendor stack in core.

## Decision

Introduce **eval measurements**: deterministic scorers that read
`run-telemetry.jsonl` after `fullsend run` in the **same** managed job
(`fullsend eval-measure` in `action.yml`), **fail-open**. Functional eval
scenarios remain ADR 0051 / `eval/<agent>/`; measurements never block
delivery.

In plain terms: eval measurements are the concept of scoring traces.
[OTEL primary facts](../glossary.md#otel-primary-facts) are what happened
on the run (the OTEL trace / `run-telemetry.jsonl`).
[OTEL derived products](../glossary.md#otel-derived-products) are scores
computed from that trace (`eval-measurements.jsonl`). Measurements never
rewrite primary facts, and they are [fail-open](../glossary.md#fail-open).

Scores always land in a tool-agnostic `eval-measurements.jsonl` (plus a
small idempotency ledger) next to `run-telemetry.jsonl`. Remote score export
will use the same `OTEL_EXPORTER_OTLP_*` configuration as ADR 0050 — no
vendor-specific score adapters in core. `fullsend` owns the parser, scorers,
CLI, and GHA step; `fullsend-ai/agents` owns per-agent measurement manifests
(`eval/measurements/<agent>.yaml`) that declare which scorers to enable.
Stock-agent defaults resolve from `agents@v0` at runtime; local files are for
override, opt-out, or custom agents only.

The first scorer is `trace_fitness` (catalog id `em-001`) — span-tree and
attribute fitness so later scorers can trust the trace.

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

- Every measured run produces a reviewable, backend-agnostic score file beside
  telemetry; missing manifests skip cleanly and measure failure never fails
  the agent job. GitHub Actions is the first-ship managed path (uploads
  `output/`). GitLab CI calls the same fail-open `eval-measure` CLI, writing
  under `/tmp/fullsend-output` with no `artifacts:` block by default.
- Core stays tool-agnostic: no product-specific score env vars in managed
  workflows; remote scores follow OTEL when that path lands.
- Functional scenarios (gate) and eval measurements (trend) stay separate;
  retro can recommend either a manifest scorer or a scenario fixture.
- Richer telemetry (Level 3 / Status fixes) expands what scorers *can* assert;
  it does not replace this same-job path.
- Per-measurement versioning (`id@version`) lets pass/fail semantics evolve
  without mixing trend eras.
- Pre-script skipped runs (`fullsend.prescript.skipped=true` on the root span)
  are excluded from EM-001: the scorer writes `label: skip` instead of failing
  a run that never created a sandbox.
