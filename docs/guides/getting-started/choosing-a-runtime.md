---
sidebar_label: Choose a Runtime
---

# Choose an agent runtime

Fullsend supports multiple agent runtimes. A runtime is the program that runs inside the sandbox and drives the model — it owns the tool-use loop, hook wiring, and transcript format. The runner (fullsend) owns everything outside: sandbox lifecycle, credentials, metrics, and the verdict.

## Available runtimes

| Runtime | Description | When to use |
|---------|-------------|-------------|
| `claude` | Claude Code (production default) | Most deployments — mature, full sub-agent support |
| `pi` | [Pi](https://github.com/earendil-works/pi) — Claude on Vertex by default; any provider pi supports by model name (e.g. Gemini on Vertex with the same credentials) | Opt-in alternative; no sub-agent tool yet, so `review`/`retro` run single-context; see [Runtimes](../../runtimes.md) for known constraints |

## How to select a runtime

### At setup time

`fullsend github setup <owner/repo>` asks which runtime to use when run from a terminal (press Enter for `claude`), and the setup PR it opens describes the choice. To set it explicitly, pass `--runtime`:

```bash
fullsend github setup <owner/repo> \
  --inference-wif-provider "<wif-url>" \
  --runtime pi
```

### In config

Set `runtime:` in the repo's `.fullsend/config.yaml`:

```yaml
runtime: pi
```

For fleets managed through `repos.yaml`, set `defaults.runtime` (or a per-entry `runtime`) and run `fullsend repos install`, or pass `fullsend repos install --runtime pi` for repos the command adds:

```bash
fullsend repos set-default defaults.runtime pi
```

To try a runtime or model on one run without changing the repo, use the per-run overrides — `fullsend run --runtime pi --model google-vertex/gemini-2.5-flash`, or the `FULLSEND_RUNTIME` / `FULLSEND_MODEL` / `FULLSEND_EFFORT` environment variables (flag beats environment beats config).

No workflow file change is needed — the runner reads the config at the start of every run.

## Where to see what ran

After a run completes, the selected runtime and model appear in several places:

- **Run plan block** — `Runtime: <name> (from <source>)` printed at the start of every `fullsend run`
- **Status comment** — the terminal status comment on the issue/PR includes a footer with runtime, model, effort, and cost
- **metrics.json** — `runtime`, `requested_runtime`, `runtime_source`, `requested_model`, and `override_source` fields record what was selected and why
- **stderr** — `runtime: selected "<name>" from <source>` for script consumers

## Next steps

- [Configuring GitHub](configuring-github.md) to set up your repo
- [Runtimes](../../runtimes.md) for the full runtime reference, including model override precedence and the capability table
