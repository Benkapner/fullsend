---
sidebar_label: Choose a Runtime
---

# Choose an agent runtime

Fullsend supports multiple agent runtimes. A runtime is the program that runs inside the sandbox and drives the model — it owns the tool-use loop, hook wiring, and transcript format. The runner (fullsend) owns everything outside: sandbox lifecycle, credentials, metrics, and the verdict.

## Available runtimes

| Runtime | Description | When to use |
|---------|-------------|-------------|
| `claude` | Claude Code (production default) | Most deployments — mature, full sub-agent support |
| `pi` | [Pi](https://github.com/earendil-works/pi) on Claude-on-Vertex | Opt-in alternative; see [Runtimes](/runtimes) for known constraints |

## How to select a runtime

### At setup time

Pass `--runtime` when configuring a repo:

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

Or set `defaults.runtime:` in the org config to apply to all repos:

```yaml
defaults:
  runtime: pi
```

No workflow file change is needed — the runner reads the config at the start of every run.

## Where to see what ran

After a run completes, the selected runtime and model appear in several places:

- **Run plan block** — `Runtime: <name> (from <source>)` printed at the start of every `fullsend run`
- **Status comment** — the terminal status comment on the issue/PR includes a footer with runtime, model, effort, and cost
- **metrics.json** — `runtime`, `requested_runtime`, `requested_model`, and `override_source` fields record what was selected and why
- **stderr** — `runtime: selected "<name>" from <source>` for script consumers

## Next steps

- [Configuring GitHub](configuring-github.md) to set up your repo
- [Runtimes](/runtimes) for the full runtime reference, including model override precedence and the capability table
