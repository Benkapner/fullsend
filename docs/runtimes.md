# Agent runtimes

Fullsend's `fullsend run` command delegates in-sandbox agent execution to a pluggable **runtime**. Recognized values in org `config.yaml` `defaults.runtime` are **`claude`** (production default) and **`dummy`** (behaviour tests only). Install with `fullsend admin install --runtime dummy` on dedicated test orgs. The runner resolves the backend via `runtime.ResolveFromConfig()` after loading the org config.

When adding a runtime, fill in the security matrix below and register it in `runtime.Resolve()`.

## Registered runtimes

| Runtime | Purpose | Inference |
|---------|---------|-----------|
| `claude` | Production agent runs via Claude Code | Required |
| `opencode` | OpenCode agent runs (stub — not yet functional; resolved by `runtime.Resolve()` but not in `ValidRuntimes()` until implemented) | Required |
| `dummy` | Behaviour tests — scripted ops in real sandbox | None |

## Security feature matrix

| Feature | Where it runs | Claude Code | OpenCode (stub) | Notes for future runtimes |
|---------|---------------|-------------|-----------------|---------------------------|
| **Host-side context injection scan** (DeBERTa / LLM Guard, unicode, SSRF patterns on repo context files) | Host + sandbox `scan context` | ✓ | N/A — stub | Requires sandbox image with ML models; harness `security.host_scanners` |
| **Host-side runtime content scan** (agent def, SKILL.md, plugin JSON before upload) | Host (`scanRuntimeContent`) | ✓ | N/A — stub | Uses `security.InputPipeline()`; not part of `Runtime` interface — runner responsibility |
| **Tirith** (Bash command scanning) | Sandbox PreToolUse hook | ✓ (loaded via `--settings`, #6358) | N/A — stub | `tirith_check.py`; harness `security.sandbox_hooks.tirith`; fails open on missing binary/timeout unless `TIRITH_REQUIRED=1` |
| **SSRF pre-tool** | Sandbox PreToolUse hook | ✓ (e2e-guarded by `hooks-loaded.feature`) | N/A — stub | `ssrf_pretool.py`; default on |
| **Canary token detection** | Sandbox Pre/PostToolUse hooks | pre ✓; post-tool via `posttool_chain.py` (`tool_response` / `updatedToolOutput`, #6357) | N/A — stub | `canary_pretool.py` / `canary_posttool.py`; both inert unless `FULLSEND_CANARY_TOKEN` is set. Post-tool canary is an in-process chain stage so it cannot race sanitizer rewrites. Claude Code `decision:block` does not hide PostToolUse output, so the chain also redacts the token in `updatedToolOutput`. |
| **Secret redaction** | Sandbox PostToolUse hook | ✓ via `posttool_chain.py` (#6357) | N/A — stub | `secret_redact_posttool.py` |
| **Unicode normalization** | Sandbox PostToolUse hook | ✓ via `posttool_chain.py` (#6357) | N/A — stub | `unicode_posttool.py` |
| **Context suppression** | Sandbox PostToolUse hook | ✓ via `posttool_chain.py` (#6357) | N/A — stub | `context_suppress_posttool.py` |
| **Tool allowlist** | Sandbox PreToolUse hook | opt-in; ✓ when enabled | N/A — stub | `tool_allowlist_pretool.py`; requires `FULLSEND_TOOL_ALLOWLIST` (fail-closed when unset) |
| **Prompt injection (DeBERTa)** | Host Path A + sandbox Path B | ✓ | N/A — stub | Same scanner stack as context files when enabled in harness |
| **Sandbox tool hooks wiring** | `SandboxHooksBootstrap` type assert in `Bootstrap` | ✓ scripts at `claude-config/hooks/`, wiring at `claude-config/hooks.json` via `--settings` (#6358) | ✗ — `Bootstrap` is a stub; must wire `security.HookPlan` via OpenCode plugin hooks | Hook scripts and wiring plan are runtime-neutral (see [Sandbox hook contract](#sandbox-hook-contract)); a runtime that ignores `SandboxHooksBootstrap` installs **no** sandbox tool hooks — say so explicitly here |
| **Transcript / debug artifacts** | `TranscriptHandler` (+ optional `DebugLogNamer`) | ✓ (stream-json, `claude-debug.log`) | No-op — see #1935 | Format-specific; not shared across runtimes. Debug-log filename defaults to `agent-debug.log` unless the runtime implements `DebugLogNamer` |

### Fail modes

Harness `security.fail_mode` controls whether critical findings **block** the run (`closed`, default) or **warn** and continue (`open`). This applies to host scans, sandbox `scan context`, and host-side runtime content scan alike.

### Runtime interface contract

| Interface | Responsibility |
|-----------|----------------|
| `runtime.Runtime` | Name, config dir, env exports, bootstrap, run loop, per-iteration artifact cleanup |
| `runtime.BootstrapInput` | Portable agent name/path, skill dirs, and plugin dirs to upload |
| `runtime.SandboxHooksBootstrap` | Optional `BootstrapInput` extension — runtime-neutral sandbox tool hook config (`security.SandboxHookConfig`); every runtime should honour it |
| `runtime.TranscriptHandler` | Extract transcripts/debug logs; parse errors for CI annotations |
| `runtime.DebugLogNamer` | Optional — names the per-iteration debug-log artifact (default `agent-debug.log`) |
| `runtime.ContextBridger` | Optional — runtime auto-loads only `CLAUDE.md`, so the runner injects a `CLAUDE.md`→`AGENTS.md` pointer (Claude Code: yes; runtimes that read `AGENTS.md` natively: omit) |

A runtime whose `Bootstrap` does not type-assert `SandboxHooksBootstrap` will **not** install Tirith, SSRF, canary, or the other hook scripts. The primary security boundary is the OpenShell sandbox, its L7 egress policy, and credential placeholders (ADR 0017, ADR 0025); the hooks are defense-in-depth that every runtime should wire rather than silently drop ([ADR 0090](ADRs/0090-runtime-neutral-sandbox-hooks-contract.md)). Fill in the matrix column above either way.

### Sandbox hook contract

**Contract version: v2** — PostToolUse scripts consume Claude Code's `tool_response` (falling back to `tool_result` for adapters/tests) and replace output via `hookSpecificOutput.updatedToolOutput`. v1 (`tool_result` in/out only) was inert under Claude Code (#6357).

The hook scripts in `internal/security/hooks/*.py` are plain programs with no Claude Code dependency; Claude Code invokes them through `settings.json`. Any runtime can call them from its own tool-call interception point (OpenCode `tool.execute.before/after`, pi `tool_call`/`tool_result`, Cursor hooks, …).

- **Files:** `security.HookFiles(cfg)` returns `filename → script bytes` for the enabled hooks; `runtime.installHookScripts(sandbox, dir, cfg)` creates `dir` in the sandbox and uploads them there (executable) — any directory works. Claude uses `/sandbox/claude-config/hooks/` (`security.SandboxHooksDir`), with the wiring at `/sandbox/claude-config/hooks.json` (`security.SandboxHooksSettings`) loaded via `--settings`.
- **Wiring:** `security.HookPlan(cfg)` returns ordered `HookGroup{Phase, Tools, Scripts}` entries. `Phase` is `PreToolUse` or `PostToolUse`; `Tools` are Claude Code tool names (`Bash`, `Read`, `WebFetch`, `*` = all) — runtimes with other names translate before matching (see #608). PostToolUse is a **single** `posttool_chain.py` script on `*` that applies suppress → unicode → redact → canary in-process (Claude Code runs matching hooks in parallel and does not merge two `updatedToolOutput` rewrites). Individual sanitizer files and `canary_posttool.py` are shipped as libraries the driver imports; adapters should invoke the chain, not the stages. `GenerateHooksConfig` is rendered from `HookPlan`, so the two cannot diverge.
- **Wire protocol (per script):** JSON on stdin — `{"tool_name": ..., "tool_input": {...}}` for PreToolUse. PostToolUse payloads include the tool output as `tool_response` (Claude Code; string or structured object such as Bash `{stdout, stderr, interrupted, isImage}`) with `tool_result` accepted as a fallback. Exit `0` = allow. *Blocking* scripts (all PreToolUse scripts, standalone `canary_posttool.py`, and `posttool_chain.py` when its canary stage fires) exit `1` and print `{"decision":"block","reason":"..."}` on stdout; the adapter must stop the tool call (or, post-tool, drop the result) and surface the reason. *Sanitizing* stages (suppress/unicode/redact) always exit `0` and, when they changed something, print `{"hookSpecificOutput":{"hookEventName":"PostToolUse","updatedToolOutput": <same shape as the input value>}, "tool_result": <scan text>}`. Empty stdout = unchanged. `updatedToolOutput` must match the tool's output shape — a bare string is ignored for built-in Claude Code tools. `scan_text` flattens every string field (including `stderr`); `apply_text` writes a replacement into the first text slot and blanks the rest, or leaves unrecognized structured shapes unchanged.
- **Fail modes:** blocking scripts fail **closed** on malformed JSON or oversized input (> 10 × 1024 × 1024 characters, read from text-mode stdin) — they block. Empty/whitespace-only stdin is treated as "no tool call" and allowed by every script; a payload without `tool_name` blocks only in the allowlist hook. `tirith_check.py` fails **open** when the `tirith` binary is missing, times out or errors, unless `TIRITH_REQUIRED=1` (which `appendHookEnv` writes when Tirith is enabled — adapters must make sure it reaches the script). Sanitizing scripts and each `posttool_chain.py` sanitizer stage fail **open** — malformed input or a stage exception is passed through unchanged (exit 0; the unicode hook logs an `input_truncated` finding). Adapters must not treat a sanitizer's empty stdout as an error.
- **Environment:** `runtime.appendHookEnv` writes `TIRITH_FAIL_ON` / `TIRITH_REQUIRED` into `/sandbox/workspace/.env`; the runtime must launch the scripts with that file sourced (Claude's run command does). Scripts also read `FULLSEND_TRACE_ID`, `FULLSEND_TOOL_ALLOWLIST` (allowlist hook, fail-closed when unset) and `FULLSEND_CANARY_TOKEN` (both canary hooks are no-ops when it is empty; supply it via harness `env.sandbox`/`host_files`), and write findings to `/sandbox/workspace/.security/findings.jsonl`.
- **Claude Code caveats (#6358, #6357):** (1) *Loading* — fixed by #6358: the hook wiring is written to the runner-owned `/sandbox/claude-config/hooks.json` and passed explicitly via `--settings`, so it loads regardless of the CLI's working directory (previously it sat unread in `/sandbox/workspace/.claude/`); the `hooks-loaded.feature` behaviour scenario guards the "silently not loaded" regression class. Note Claude Code still auto-loads a target repo's own `<repo>/.claude/settings.json` hooks from `<cwd>` — a separate exposure to assess. (2) *Payload (fixed in #6357, contract v2)* — scripts read `tool_response` (fallback `tool_result`) and replace output via `hookSpecificOutput.updatedToolOutput` with the original shape preserved. Sanitizer order and canary detection share `posttool_chain.py` so two PostToolUse hooks cannot race. `scan_text` inspects every string field (including `stderr`). (3) *Blocking* — Claude Code keys on the stdout JSON on any exit code (`decision:"block"` is deprecated for PreToolUse but still maps to `deny`) and treats a bare exit `1` as non-blocking (exit `2` is its own blocking code); a local control run confirmed the scripts' "exit 1 + `{"decision":"block"}`" convention does block once the settings are loaded. For PostToolUse, `decision:"block"` **only appends `reason` next to the tool result — Claude still sees the original output**. `canary_posttool.py` therefore also emits `updatedToolOutput` with the token redacted to `[CANARY_REDACTED]`. Net: after #6358 and #6357, both PreToolUse and PostToolUse halves of the contract are effective under Claude Code.

### Runtime-specific config key support

Harness keys are runtime-neutral in the YAML but each runtime owns their translation. Claude Code passes them through unchanged; other runtimes must document their mapping here (this is also an acceptance criterion in #6319).

| Harness key | Claude Code | OpenCode (stub) | Dummy | Notes for new runtimes |
|-------------|-------------|-----------------|-------|------------------------|
| `model` | `--model` (identity; aliases like `opus` resolved by the CLI) | — | ignored | `validModelName` is `^[a-zA-Z0-9_.@-]+$` — no `/`. Runtimes with `provider/model` ids need an alias table or a follow-up regex change |
| `effort` | `--effort` (`low\|medium\|high\|xhigh\|max`, #6218) | — | ignored | Map to the runtime's reasoning knob or reject with a clear error |
| `plugins` | Claude plugin marketplace layout (`bootstrapPlugins`) | — | ignored | Claude-specific format; warn and skip if unsupported |
| Agent frontmatter `tools:` (`Bash(gh,jq)` syntax, ADR 0027) | Native Claude permission syntax | — | ignored | Enforce via `--tools`/allowlist plus a hook adapter; Claude tool names differ in case from most runtimes (#608) |
| `skills` | `CLAUDE_CONFIG_DIR/skills/` | — | ignored | Agent Skills spec (`SKILL.md`) is portable; destination is `rt.ConfigDir() + "/skills"` (also used by the runtime fetch service) |
| `security.sandbox_hooks` | `SandboxHooksBootstrap` → hooks.json via `--settings` | ✗ (stub) | ignored | See [Sandbox hook contract](#sandbox-hook-contract) |
| `--debug` (CLI flag) | `--debug-file`, artifact `claude-debug.log` | — | no-op | Implement `DebugLogNamer` to name the artifact |

## Sandbox workspace layout

The sandbox has two key directories that map to Claude Code's config levels:

```
/sandbox/
├── claude-config/                   ← CLAUDE_CONFIG_DIR (personal level)
│   ├── agents/
│   │   └── <name>.md                   Agent definition (filename derived from the agent name)
│   ├── skills/
│   │   ├── code-review/SKILL.md        Built-in skills (personal level — wins on collision)
│   │   ├── pr-review/SKILL.md
│   │   └── ...
│   ├── plugins/
│   │   └── ...                         Plugin state (simplified; see bootstrapPlugins())
│   ├── hooks/                          Security hook scripts (PreToolUse, PostToolUse)
│   └── hooks.json                      Hook wiring (loaded via --settings in buildRunCommand)
│
└── workspace/                       ← SandboxWorkspace
    ├── .env                            Environment variables (sourced before claude)
    ├── .env.d/                         Additional env files (host_files expand)
    │
    └── <repo-name>/                 ← Claude Code's working directory (cd target)
        ├── CLAUDE.md                   Project instructions (repo's own or injected bridge)
        ├── AGENTS.md                   Project rules (repo's own or org default injected)
        ├── .claude/skills/             Repo skills (project level — shadowed on collision)
        │   └── custom-lint/SKILL.md
        └── src/...                     Target repo source code
```

## Agent rule layering

When `fullsend run` executes an agent, Claude Code loads instructions from
multiple sources. These compose — they occupy different layers, not competing
slots:

```
┌────────────────────────────────────────────────────────┐
│  Layer 1: Agent Definition (system prompt)             │
│  Source: /sandbox/claude-config/agents/<name>.md       │
│  Loaded via: --agent flag                              │
│  Controls: role, task, tools, disallowedTools, model,  │
│            built-in skills list                        │
│  Authority: highest — repo cannot modify               │
├────────────────────────────────────────────────────────┤
│  Layer 2: Project Instructions (advisory)              │
│  Source: /sandbox/workspace/<repo>/CLAUDE.md           │
│         /sandbox/workspace/<repo>/AGENTS.md            │
│  Loaded via: Claude Code auto-loads from working dir   │
│  Controls: conventions, architecture, domain context   │
│  Authority: advisory — cannot override layer 1         │
├────────────────────────────────────────────────────────┤
│  Layer 3: Skills                                       │
│  Personal: /sandbox/claude-config/skills/ (fullsend)   │
│  Project:  <repo>/.claude/skills/ (repo)               │
│  Precedence: personal > project (name collision →      │
│              fullsend wins, repo version shadowed)     │
│  Repo skills extend the agent; use config-driven       │
│  agent registration for org-level skill overrides      │
└────────────────────────────────────────────────────────┘
```

### AGENTS.md injection logic

`run.go` step 8a (`hasAgentsMD()` / `injectClaudeMDPointer()`):

1. If target repo has no AGENTS.md → inject org-level default from config repo,
   add to `.git/info/exclude`
2. If the runtime implements `ContextBridger` (Claude Code does), target
   repo has AGENTS.md but no CLAUDE.md → inject bridge CLAUDE.md pointing to
   AGENTS.md, add to `.git/info/exclude`
3. If target repo has both → use as-is

### Context file security scanning

`run.go` steps 8c and 9b:

Repo context files (CLAUDE.md, AGENTS.md, SKILL.md) are scanned in two
defense-in-depth passes before the agent starts:

1. **Host-side (Path A, step 8c):** `scanRepoContextFiles()` runs the
   `InputPipeline` (unicode normalizer, context injection scanner) on the
   host before files enter the sandbox.
2. **Sandbox-side (Path B, step 9b):** `buildScanContextCommand()` runs
   `fullsend scan context` inside the sandbox after all files are assembled.

Critical findings block the run in `fail_mode: closed`.

## Dummy runtime operations

The `dummy` runtime executes a YAML script of operations inside the real sandbox (behaviour tests only). Besides `write_fixture` and `fail`, dispatch behaviour tests use:

| Op | Args | Purpose |
|----|------|---------|
| `assert_env` | `VAR_NAME` | Assert env var is set and non-empty in the sandbox |
| `assert_file` | `path` | Assert file exists and is readable under the workspace |
| `assert_json` | `path,json_path` | Assert JSON file exists and dot-path field is present and non-null (uses `jq`) |

## Related docs

- [cli-internals.md](guides/dev/cli-internals.md) — sandbox constants, key sandbox operations
- [architecture.md](architecture.md) — Agent Runtime layer
- [problems/security-threat-model.md](problems/security-threat-model.md) — threat model and scanner paths
- [problems/agent-architecture.md](problems/agent-architecture.md) — pluggable runtimes (#1260, #579, #70)
