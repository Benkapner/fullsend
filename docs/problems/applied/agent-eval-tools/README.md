# Agent-Driven Evaluation Tools

How fullsend's own problem areas appear in agent-driven tools that evaluate agent configurations.

## Context

Agent setup evaluation tools (linters, security scanners for skills/commands/agents/hooks) are themselves agent-driven systems. They face a subset of the same challenges fullsend faces, viewed from the tooling side rather than the platform side.

An example is [harness-eval](https://github.com/redhat-community-ai-tools/harness-eval), an open-source linter and security scanner for AI agent configurations (not to be confused with agent-eval-harness, the dynamic test-execution framework adopted in ADR-0051; harness-eval performs static analysis and does not execute agents). It runs deterministic rules against skills, commands, agents, hooks, and MCP configs, with optional LLM-based review.

## Technology landscape

These tools typically operate as CLI utilities or CI integrations (GitHub Actions, pre-commit hooks). They parse agent configuration files (markdown with YAML frontmatter, JSON schemas, shell scripts) and run pattern-based or AST-based analysis without executing the agent. The evaluation happens at the configuration layer, not the runtime layer, which means they complement behavioral testing rather than replacing it.

## Relevant problem areas

### [Testing agents](../../testing-agents.md)

An evaluation tool's own agent setup (skills, commands, hooks) needs the same static analysis it provides to others. The tool must dogfood its own checks, and its CI must gate on its own lint and security rules. Failure to do this means the tool's own configuration can drift into the patterns it flags for others.

### [MCP configuration drift](../../mcp-config-drift.md)

Evaluation tools that consume `.mcp.json` or similar MCP configs as analysis targets face config drift when servers are added or removed. Static analysis can catch structural mismatches (e.g., a skill references an MCP server not declared in the config), but runtime drift (servers configured but never used in practice) requires observability beyond what a linter provides.

### [Tool call risk assessment](../../tool-call-risk-assessment.md)

Evaluation tools that run security scans must not themselves become attack vectors. A malicious skill under evaluation could contain patterns designed to influence the evaluator's behavior (jailbreak patterns, evaluator-targeted prompt injection). The tool needs its own defense against adversarial inputs, which is distinct from the defenses it provides to users.

### [Trustworthiness evidence](../../trustworthiness-evidence.md)

For an evaluation tool, trustworthiness evidence takes a specific form: false positive rate, false negative rate, and rule accuracy over time. If the tool flags too many false positives, teams disable it. If it misses real issues, teams lose trust. Tracking these metrics is how the tool earns continued adoption.

## Unique considerations

- **Recursive evaluation:** the tool must be able to evaluate its own setup without circular dependency issues
- **Rule accuracy feedback loop:** users who suppress findings or override verdicts generate signal about rule quality
- **Multi-assistant support:** an evaluation tool aiming for broad adoption may need to support multiple AI coding-agent runtimes (Claude Code, Cursor, Copilot, Gemini, OpenCode) with different configuration formats, unlike fullsend, which defaults production orgs to a single runtime (Claude Code) today despite having a pluggable `runtime.Runtime` interface (docs/runtimes.md), with OpenCode noted as another candidate backend
