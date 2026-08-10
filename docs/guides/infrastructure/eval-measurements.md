# Eval Measurements

Eval measurements score agent runs from their **OpenTelemetry traces** for
trends over time. They are **not** functional evals (PR-gate fixtures under
`fullsend-ai/agents` `eval/<agent>/`).

**Decided:** [ADR 0087](../../ADRs/0087-eval-measurements-online-trace-scoring.md).
Telemetry baseline: [Distributed Tracing](./distributed-tracing.md)
([ADR 0050](../../ADRs/0050-distributed-tracing-instrumentation.md)).

## Architecture (read this first)

Every managed agent job that produces telemetry gets the same three layers.
Only the first is mandatory; the others reuse existing OTEL config or are
optional product adapters.

```text
fullsend run
  └─ always writes  output/**/run-telemetry.jsonl
  └─ if OTEL_EXPORTER_OTLP_* set → live OTLP export of agent spans
       (any compatible backend — ADR 0050)

fullsend eval-measure   (same GHA job, fail-open, after run)
  └─ always writes  output/**/eval-measurements.jsonl
       (+ eval-measure-ledger.jsonl for idempotency)
  └─ portable remote: same OTEL_EXPORTER_OTLP_* as the agent run
       (scores follow traces — no second required endpoint)
  └─ optional adapter: MLflow Assessments when MLFLOW_TRACKING_* is set
       (Quality / Assessments UI only — not required for portability)
```

| Artifact | When | Purpose |
|---|---|---|
| `run-telemetry.jsonl` | Every run | OTLP JSON TracesData lines (local source of truth for spans) |
| `eval-measurements.jsonl` | Every measured run | One JSON object per score (`name`, `label`, `value`, `explanation`, `trace_id`, …) |
| Remote agent spans | OTEL configured | Same spans the local file holds |
| Remote scores (portable) | OTEL configured | Scores on the OTLP path — works with any OTLP backend |
| MLflow Assessments | `MLFLOW_TRACKING_PASSWORD` (+ URI) set | Extra UX on MLflow only |

**Do not** treat MLflow Assessments as the primary export. Telemetry already
goes “anywhere OTLP points”; scores must follow that model. Assessments are an
optional adapter for one backend’s Quality panel.

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
| Parser, scorers, CLI, GHA post-step | `fullsend-ai/fullsend` |
| Per-agent **measurement manifests** (which scorers) | `fullsend-ai/agents` |

First scorer: **`trace_fitness`** (catalog id `em-001`) — span tree + expected
attributes so later scorers can trust the trace.

Manifest shape:

```yaml
agent: review
measurements:
  - id: em-001
    scorer: trace_fitness
    version: 1
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
| [#5947](https://github.com/fullsend-ai/fullsend/pull/5947) Level 3 activation + sandbox OTEL denylist | Richer traces fuel later scorers. v1 reads Level 1/2 local JSONL; content-aware scorers need OTLP/backend (or a widened file contract) under the proposed L3 rules. Measure CLI is host-side after the sandbox exits. |
| [#5944](https://github.com/fullsend-ai/fullsend/pull/5944) Span status from run outcome | Unblocks outcome scorers keyed on Status, not raw exit alone. |
| [#2423](https://github.com/fullsend-ai/fullsend/pull/2423) Semantic observability / observer / lessons | Observer + lessons → fixtures; measurements are the online score path. |

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
- Export failures warn and still exit `0`; local JSONL is kept.

### Env for remote export

| Variable | Role |
|---|---|
| `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` / `_HEADERS` / … | **Portable** score export (same as agent traces) |
| `MLFLOW_TRACKING_URI` | Optional; derived from OTEL endpoint if unset |
| `MLFLOW_TRACKING_USERNAME` | Optional Assessments adapter (default `admin`) |
| `MLFLOW_TRACKING_PASSWORD` | Optional Assessments adapter (Basic auth) |

Reusable agent workflows forward OTEL and optional `MLFLOW_TRACKING_*`.
The OTEL ingest Bearer token can write `/v1/traces` but cannot create MLflow
Assessments — that adapter needs tracking Basic auth when used.

## Implementation note

Local `eval-measurements.jsonl` and the optional MLflow Assessments adapter
are wired in the measure CLI today. Portable OTLP score export (same
`OTEL_*` as traces) is the ADR 0087 remote contract — keep adapters
optional; do not require `MLFLOW_TRACKING_*` for scores to be useful
outside one vendor UI. Until portable OTLP score export lands, remote
scores need the optional Assessments adapter (or local JSONL alone).
