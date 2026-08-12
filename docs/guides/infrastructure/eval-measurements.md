# Eval Measurements

Eval measurements score agent runs from their **OpenTelemetry traces** for
trends over time. They are **not** functional evals (PR-gate fixtures under
`fullsend-ai/agents` `eval/<agent>/`).

**Decided:** [ADR 0087](../../ADRs/0087-eval-measurements-online-trace-scoring.md).
Telemetry baseline: [Distributed Tracing](./distributed-tracing.md)
([ADR 0050](../../ADRs/0050-distributed-tracing-instrumentation.md)).

## Prerequisites

- A repository with fullsend installed and producing `run-telemetry.jsonl`
  (see [Distributed Tracing](./distributed-tracing.md)).
- A measurement manifest for the agent (stock agents get one from
  `fullsend-ai/agents@v0`; custom agents need a local YAML under
  `${FULLSEND_DIR}/eval/measurements/`).

## Architecture (read this first)

Fullsend does not pick an observability product for scores. The portable
contract is a local JSONL artifact next to telemetry; remote export reuses
the same [OTEL](../../glossary.md) (OpenTelemetry) configuration as agent
traces when implemented.

OTLP (OpenTelemetry Protocol) is the wire format that carries spans and
scores to any compatible backend — Phoenix, MLflow, Jaeger, etc.

```text
fullsend run
  └─ always writes  output/**/run-telemetry.jsonl
  └─ if OTEL_EXPORTER_OTLP_* set → live OTLP export of agent spans
       (any compatible backend — ADR 0050)

fullsend eval-measure   (same GHA job, fail-open, after run)
  └─ always writes  output/**/eval-measurements.jsonl
       (+ eval-measure-ledger.txt for idempotency)
```

> **Planned:** portable remote score export via the same `OTEL_EXPORTER_OTLP_*`
> path as agent traces. Not yet implemented.

| Artifact | When | Purpose |
|---|---|---|
| `run-telemetry.jsonl` | Every run | OTLP JSON TracesData lines (local source of truth for spans) |
| `eval-measurements.jsonl` | Every measured run | One JSON object per score (`name`, `label`, `value`, `explanation`, `trace_id`, …) |
| Remote agent spans | OTEL configured | Same spans the local file holds |
| Remote scores *(planned)* | OTEL configured | Scores on the OTLP path — any OTLP backend |

Orgs choose Phoenix, MLflow, Jaeger, or another collector independently.
Fullsend does not forward vendor-specific score credentials in managed
workflows.

## Measurements vs functional evals

| | Functional evals | Eval measurements |
|---|---|---|
| **Repo path** | `agents/eval/<agent>/` | `agents/eval/measurements/<agent>.yaml` |
| **When** | PR / CI fixture gates | After each managed agent run |
| **Input** | Case fixtures + judge harness | `run-telemetry.jsonl` only |
| **Blocks delivery?** | Yes (when wired as a check) | Never (fail-open) |

## Ownership

| Concern | Repo |
|---|---|
| Parser, scorer **implementations**, CLI, GHA post-step | `fullsend-ai/fullsend` |
| Default manifests for stock agents (which scorers / ids) | `fullsend-ai/agents` |
| Overrides, opt-out, BYOA agent manifests | Consumer repo (`FULLSEND_DIR`) |

Defaults for stock agents live next to those agents in `fullsend-ai/agents`.
Managed jobs fetch them from `agents@v0` when no local file exists — users do
**not** duplicate YAML into every install. Put a file under
`${FULLSEND_DIR}/eval/measurements/` only to change policy or score a custom
agent.

Scorer *code* stays in fullsend: the measure CLI is the released engine that
understands `run-telemetry.jsonl`. Agents ships **policy** (manifests), not Go.
EM-001 (`trace_fitness`) evaluates fullsend’s telemetry contract across agents;
stock agents opt in via manifests here / in `agents`.

| Change | Where |
|---|---|
| New Go scorer or new declarative `assert:` primitive | `fullsend` PR |
| New measurement `id` / enable / thresholds for a stock agent (existing scorer) | `agents` PR |
| Org-specific policy for stock or custom agents | Local override in the consumer repo |

**Planned (not in first ship):** declarative checks in the manifest (attribute
exists, ratio/threshold bands) so most agent-specific policy is YAML-only.
Until then, agent-specific math still lands as a named Go scorer in fullsend,
enabled only for the agents that list it.

First scorer: **`trace_fitness`** (catalog id `em-001`) — span tree + expected
attributes so later scorers can trust the trace.

Manifest shape (first ship — enablement only):

```yaml
agent: review
measurements:
  - id: em-001
    scorer: trace_fitness
    version: 1
```

Illustrative **logic-as-config** (future declarative engine — not wired yet):

```yaml
agent: code
measurements:
  - id: em-001
    scorer: trace_fitness
    version: 1
  - id: em-010
    scorer: declarative
    version: 1
    where:
      span: agent
    checks:
      - name: turn_token_ratio
        assert: ratio_lte
        numerator: gen_ai.usage.total_tokens
        denominator: fullsend.turns
        max: 8000
```

### Versioning

Not a platform “v1.” Each entry versions its own contract:

- **`id`** — stable catalog id (`em-001`). New concept → new id.
- **`scorer`** — which Go scorer to run (`trace_fitness`).
- **`version`** — bump when pass/fail semantics change; scores store
  `em-001@1`. Ledger is idempotent per `(trace_id, name, id@version)`.

The EM-001 `exit` check only requires that `exit_code` is **present** on the
run span (instrumentation fitness). It does **not** treat `exit_code == 0` as
success. After [#5944](https://github.com/fullsend-ai/fullsend/pull/5944),
run/agent **OTLP Status** (and `fullsend.transcript_error`) are the
success/failure signal for outcome scorers.

## Adjacent telemetry work

| Proposal | Relationship to measurements |
|---|---|
| [#5947](https://github.com/fullsend-ai/fullsend/pull/5947) Level 3 activation + sandbox OTEL denylist | Richer traces fuel later scorers. First ship reads Level 1/2 local JSONL; content-aware scorers need OTLP/backend (or a widened file contract) under the proposed L3 rules. Measure CLI is host-side after the sandbox exits. |
| [#5944](https://github.com/fullsend-ai/fullsend/pull/5944) Span status from run outcome | Unblocks outcome scorers keyed on Status, not raw exit alone. |
| [#2423](https://github.com/fullsend-ai/fullsend/pull/2423) Semantic observability / observer / lessons | Observer + lessons → fixtures; measurements are the online score path. |
| [#5524](https://github.com/fullsend-ai/fullsend/pull/5524) Harness snapshot / forge join keys (ADR 0075) | Complementary join/identity proposal beside telemetry; measurements are derived scores, not primary run facts. |

## Same-job timing

```text
GitHub Actions job
├── fullsend run
├── fullsend eval-measure   # reads run-telemetry.jsonl; never fails the job
└── upload-artifact         # includes both JSONL files under output/
```

## Manifest resolution in CI

`action.yml` resolves the measurement manifest for `inputs.agent`:

1. `${FULLSEND_DIR}/eval/measurements/${AGENT}.yaml` if present, else
2. `https://raw.githubusercontent.com/fullsend-ai/agents/v0/eval/measurements/${AGENT}.yaml`

Step 2 is how stock-agent defaults reach every install (same pin style as
other agents-repo fallbacks). Step 1 is override / BYOA only.

Missing manifest → log and exit `0` (skip). The CLI flag remains
`--registry` for now (path to the YAML); rename is cosmetic follow-up.

## CLI

```bash
fullsend eval-measure \
  --telemetry path/to/run-telemetry.jsonl \
  --registry path/to/agents/eval/measurements/review.yaml \
  --out-dir path/to/output
```

- `--registry` is required (manifests live in `fullsend-ai/agents`, not in
  the fullsend binary).
- Exit `0` when a score is `fail` — scores are data.

## Implementation note

Today the measure CLI always writes local `eval-measurements.jsonl`.

> **Planned:** portable OTLP score export (same `OTEL_*` as traces) is the
> ADR 0087 remote contract and is not wired yet. Until it lands, consume the
> JSONL artifact (or your own pipeline) for remote dashboards.
