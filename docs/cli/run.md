---
sidebar_label: fullsend run
---

# fullsend run

Execute an agent locally in a sandbox. `fullsend run` resolves the agent harness, provisions a sandbox container, and runs the agent to completion.

## Usage

```bash
fullsend run <agent-name> [flags]
```

## Flags

| Flag | Description |
|------|-------------|
| `--fullsend-dir` | Path to the `.fullsend` configuration directory |
| `--output-dir` | Base directory for run output (default: `/tmp/fullsend`) |
| `--target-repo` | Path to the target repository |
| `--fullsend-binary` | Path to a Linux fullsend binary to copy into the sandbox |
| `--env-file` | Load environment variables from a dotenv file (repeatable) |
| `--no-post-script` | Skip post-script execution |
| `--keep-sandbox` | Skip sandbox deletion after the run |
| `--debug [filter]` | Enable agent runtime debug logging with optional category filter (e.g. `"api,hooks"`) |
| `--forge` | Forge platform to use (e.g. `"github"`, `"gitlab"`); auto-detected from CI env vars when omitted |
| `--offline` | Reject network fetches; only use cached remote resources |
| `--max-depth` | Maximum dependency depth for transitive resolution (0 disables) |

## Plan block

At startup, `fullsend run` prints a plan block summarizing the resolved configuration:

```
Agent:     code
Role:      code
Model:     sonnet
Effort:    high
Runtime:   claude (from /path/to/.fullsend/config.yaml)
Image:     fullsend-sandbox:latest
```

The **Runtime** line shows which runtime was selected and the config source it was read from. When no `config.yaml` exists, the source reads `default (config not found)`.

## Runtime selection

The runtime is resolved from the org or per-repo `config.yaml`:

```yaml
# .fullsend/config.yaml
runtime: pi
```

The runner prints the selection to stderr for script consumers:

```
runtime: selected "pi" from /path/to/.fullsend/config.yaml
```

See [Runtimes](/runtimes) for the full list of runtimes, selection precedence, and capability differences.

## Output artifacts

Each run produces artifacts in the output directory:

| File | Description |
|------|-------------|
| `metrics.json` | Behavioral metrics: tokens, cost, model, runtime, iterations |
| `transcripts/` | Agent conversation transcripts |
| `claude-debug.log` or `pi-debug.log` | Debug log (when `--debug` is set) |

### metrics.json fields

| Field | Description |
|-------|-------------|
| `runtime` | Runtime that executed the run (e.g. `claude`, `pi`) |
| `model` | Model the provider reported using |
| `requested_runtime` | Runtime selected from config |
| `requested_model` | Model the harness/agent requested |
| `override_source` | Where the model value came from (`harness`, `FULLSEND_PI_MODEL`, `default`) |
| `total_cost_usd` | Total inference cost |
| `num_turns` | Number of conversation turns |
| `iterations` | Number of retry iterations |

## Related

- [Running Agents Locally](/guides/user/running-agents-locally) for a step-by-step walkthrough
- [Runtimes](/runtimes) for runtime selection and capabilities
- [CLI internals](/guides/dev/cli-internals) for the full command tree
