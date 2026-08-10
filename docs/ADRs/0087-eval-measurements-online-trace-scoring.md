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
(`fullsend eval-measure` in `action.yml`), **fail-open**.

**Not fixtures:** functional eval scenarios remain ADR 0051 / `eval/<agent>/`.
Measurements never block delivery. Prefer the glossary terms *eval
measurement* (score) and *eval scenario* (fixture).

**Ownership (engine vs default policy):**

| Concern | Repo |
|---|---|
| Parser, scorer **implementations**, CLI, GHA post-step | `fullsend-ai/fullsend` |
| Default measurement manifests for stock agents | `fullsend-ai/agents` (`eval/measurements/<agent>.yaml`) |
| Org/repo overrides and BYOA agent manifests | Consumer repo (`FULLSEND_DIR`) |

Stock agents ship defaults the same way as other agent content: managed jobs
resolve local `${FULLSEND_DIR}/eval/measurements/${AGENT}.yaml` if present,
else fetch `fullsend-ai/agents@v0/...`. Users who only use stock agents do
**not** copy manifests into their repos. Local files are for override, opt-out,
or custom agents — not for receiving defaults.

Executable scorer logic stays in fullsend because `fullsend eval-measure` is
the released binary that reads `run-telemetry.jsonl` (which fullsend writes).
Agents is content (YAML/prompts), not a library linked into that binary.
EM-001 (`trace_fitness`) is a platform fitness check on that telemetry
contract, so its Go implementation belongs in fullsend even though every
stock agent enables it via agents manifests.

**Future — logic-as-config:** agent-specific *policy* (thresholds, attribute
checks) should move into declarative manifest fields evaluated by a generic
engine in fullsend. Until that lands, new imperative scorers are Go in
fullsend; wiring a default for a stock agent is an agents manifest change.
New assert primitive → fullsend PR; new id/thresholds on existing primitives →
agents-only (or consumer-repo override).

**Persistence (always):** write `eval-measurements.jsonl` (and a small ledger)
next to `run-telemetry.jsonl`. This JSONL is the portable, tool-agnostic
contract — backends and dashboards are chosen by the org, not by fullsend.

**Remote export:** use the same `OTEL_EXPORTER_OTLP_*` configuration as
ADR 0050 when score export is implemented. Do **not** ship vendor-specific
score adapters or `MLFLOW_*` (or similar) wiring in core action/workflows.
Orgs that want a product UI can consume the local JSONL or an OTLP backend
outside fullsend.

**First scorer:** `trace_fitness` (catalog id `em-001`) — span-tree /
attribute fitness so later scorers can trust the trace. Further scorers
land via manifests without changing this export model.

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
  telemetry.
- Core stays tool-agnostic: no product-specific score env vars in managed
  workflows; remote scores follow OTEL when that path lands.
- Missing manifests skip cleanly; measure failure never fails the agent job.
- Functional scenarios (gate) and eval measurements (trend) stay separate.
- Retro can recommend a **manifest scorer** or a **scenario fixture** — not
  substitutes.
- Richer telemetry (Level 3 / Status fixes) expands what scorers *can* assert;
  it does not replace this same-job path.
