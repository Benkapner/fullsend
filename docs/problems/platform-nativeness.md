# Platform Nativeness

When the platform you're automating is also the platform you're building on, what problems disappear — and what new constraints appear?

## Why this matters

Fullsend is an external system that integrates with GitHub. [GitHub Agentic Workflows (gh-aw)](https://github.github.com/gh-aw/) is a native GitHub feature that runs coding agents in GitHub Actions with strong guardrails. This architectural difference has a concrete consequence: a significant portion of fullsend's implemented complexity solves problems that are artifacts of its external position, not inherent to the goal of autonomous agentic development.

This document is an honest self-assessment. It sorts fullsend's problems into three categories: problems created by building externally, problems that exist regardless of platform position, and problems both approaches face where nativeness provides an advantage. The goal is to sharpen our understanding of where fullsend's engineering effort delivers unique value versus where it compensates for a self-imposed handicap.

## Problems that arise from external integration

These problems exist because fullsend is not part of GitHub. A native system does not have them.

### Cross-repo dispatch

Fullsend uses a centralized `.fullsend` config repo as the hub for agent pipelines. When something happens in an enrolled repo (a PR is opened, an issue is filed), a shim workflow in that repo must dispatch an event to `.fullsend` so the agent pipeline can run. This requires:

- `workflow_dispatch` as the cross-repo trigger mechanism, because `workflow_call` would require the caller to have the App PEM ([ADR 0008](../ADRs/0008-workflow-dispatch-for-cross-repo-dispatch.md))
- A fine-grained PAT (`FULLSEND_DISPATCH_TOKEN`) stored as an org secret with selected-repo visibility, so enrolled repos can trigger `.fullsend` workflows
- `pull_request_target` in the shim workflow, so PR authors cannot rewrite the dispatch code to steal the token ([ADR 0009](../ADRs/0009-pull-request-target-in-shim-workflows.md))
- Verification logic — the CLI dispatches `agent.yaml` with `event_type=verify` to confirm the token works

gh-aw workflows are defined **in the repo itself** as markdown files. Events trigger Actions workflows directly. There is no cross-repo dispatch to set up, no dispatch token to manage, no shim to secure.

**Compensating value:** The centralized model means org-wide configuration lives in one place. But GitHub already has org-level Actions policies and required workflows that could serve the same purpose natively.

### Per-role GitHub App creation

Fullsend creates one GitHub App per agent role (triage, coder, review, fullsend) through the [manifest flow](https://docs.github.com/en/apps/sharing-github-apps/registering-a-github-app-from-a-manifest). This involves a local HTTP server, a browser redirect to GitHub, a callback to capture the PEM (available only at creation time), polling or prompting to install the app on the org, and logic to detect reuse vs. lost-key scenarios ([ADR 0007](../ADRs/0007-per-role-github-apps.md)). The credential lifecycle is particularly fragile: the PEM private key is returned only once during the manifest conversion, there is no API to rotate it, and if the key is lost the app must be deleted and recreated from scratch.

gh-aw uses GitHub's own `GITHUB_TOKEN`, scoped per-job. The agent gets a read-only token automatically; the write job gets a scoped write token. No App creation, no PEM lifecycle, no manifest flow, no rotation concern.

**Compensating value:** Per-role Apps give fine-grained identity separation — triage can't push code, reviewer can't merge. But GitHub's native per-job token scoping achieves a similar (if less granular) boundary with zero setup cost.

**A sharper reason than least privilege: workflow retriggering.** GitHub's own [`GITHUB_TOKEN` documentation](https://docs.github.com/en/actions/concepts/security/github_token) states the general rule plainly: events triggered by `GITHUB_TOKEN` do not create a new workflow run, except `workflow_dispatch` and `repository_dispatch`. `pull_request` events (`opened`/`synchronize`/`reopened`) get a narrow carve-out — they still fire, but land in an **approval-required** state that a human with write access must manually release; every other `pull_request` activity type (`labeled`, `edited`, `closed`) gets no carve-out at all. Issues and label events aren't in the exception list either, so they fall under the general suppression: an issue opened, or a label applied, via `GITHUB_TOKEN` silently does not trigger the next workflow.

That is fatal for a pipeline that hands off through GitHub-native events at every stage — retro filing a follow-up issue, triage applying `ready-to-implement`, code opening a PR that review is supposed to pick up automatically. Each of those handoffs must come from a real bot identity (a GitHub App installation token or PAT), not the job's ambient `GITHUB_TOKEN`, or the pipeline stalls waiting on a human click (PRs) or never proceeds at all (issues, labels). This is a stronger and more concrete justification for per-role Apps than least-privilege identity separation — it is not about limiting what an agent *can* do, it is about whether the next stage runs unattended at all. gh-aw's docs do not state whether its safe-outputs write jobs mint a comparable non-ambient identity for these operations; that should be verified before assuming its stage-to-stage handoffs run unattended end-to-end.

### Repository enrollment

Fullsend must inject a shim workflow into each target repo. The CLI creates a branch (`fullsend/onboard`), writes `.github/workflows/fullsend.yaml` with the `pull_request_target` trigger, and opens a PR. A human must merge this PR to complete enrollment ([ADR 0013, proposed](../ADRs/0013-admin-install-repo-enrollment-v1.md)).

gh-aw enrollment is adding a markdown file to your repo. `gh aw run` generates the corresponding `.lock.yml`.

**Compensating value:** The enrollment PR provides an explicit opt-in gate for repo owners. But a native system achieves the same opt-in by requiring someone to add the workflow file.

### The install/uninstall layer stack

The bulk of the Go CLI's complexity is an ordered, idempotent layer stack: config repo creation, workflow writing, secret provisioning, dispatch token setup, and enrollment ([ADR 0006](../ADRs/0006-ordered-layer-model.md)). Each layer has install, uninstall, analyze, and preflight operations. Uninstall runs in reverse order and collects errors. App deletion cannot be automated via API and requires manual browser interaction.

gh-aw's CLI installs with `gh extension install github/gh-aw`, but per-repo setup is not trivial: each repo needs a markdown workflow file (~50 lines of frontmatter + ~200 lines of agent instructions), compilation via `gh aw compile` to produce a hardened `.lock.yml` (a ~1,300-line, 5-job Actions pipeline: pre_activation → activation → agent → detection → safe_outputs), and both files committed to the repo. This must be repeated for every repo and every workflow. There is no org-level mechanism for cross-repo consistency — shared configuration requires manual imports (`owner/repo/path@ref`).

**Compensating value:** The layer model is well-engineered and idempotent. The per-repo setup cost of gh-aw is real but linear and local — each repo is self-contained. Fullsend's install stack manages cross-repo coordination infrastructure (dispatch tokens, centralized config, enrollment shims) that a native system doesn't need, but its centralized model does provide org-wide consistency that gh-aw lacks.

### Credential isolation architecture

Fullsend designs host-side REST proxies with L7 per-method/per-path policy to keep credentials out of the agent sandbox ([ADR 0017](../ADRs/0017-credential-isolation-for-sandboxed-agents.md)). The default approach is prefetch/post-process (no credentials in sandbox at all); the fallback is a capability-reducing REST proxy.

gh-aw's [security architecture](https://github.github.com/gh-aw/introduction/architecture/) solves this through a formal three-layer trust model. At the substrate level, the Agent Workflow Firewall (AWF) containerizes agents, uses iptables to redirect traffic through a Squid proxy enforcing domain allowlists, and drops capabilities before launching the agent. An API proxy routes model traffic while keeping credentials isolated. An MCP Gateway spawns each MCP server in its own isolated container with per-server domain allowlists and tool allowlisting. At the plan level, the SafeOutputs subsystem buffers all external writes as artifacts, with a separate threat detection job gating the write jobs. The agent's token literally cannot write — credential isolation is enforced by the platform, not by a proxy.

**Compensating value:** The L7 proxy design is more flexible (works with any runtime, not just Actions). But that flexibility is only needed if agents run outside Actions.

## Problems that arise from fullsend's ambition

These problems exist regardless of whether the system is native or external. They are inherent to the goal of fully autonomous agentic development and are not addressed by gh-aw.

### Autonomous merge judgment

gh-aw defaults to keeping humans in the loop. Its safe-outputs model produces artifacts that a gated write job applies, but merge is not currently one of the permitted operations. This is however a default design choice, not a platform limitation — credentials injected into an agentic workflow can allow merging a PR. A [GitHub blog post](https://github.blog/ai-and-ml/generative-ai/code-review-in-the-age-of-ai-why-developers-will-always-own-the-merge-button/) argues developers will always own the merge button, but that's one team's editorial position, not a constraint GitHub's platform enforces. Fullsend's thesis is that for routine changes with sufficient verification, the merge decision can be automated, but such a system could be implemented directly with GitHub Agentic Workflows as well.

### Intent verification

Checking whether a change is authorized against a structured intent system — not just "is this change correct?" but "is this change one we actually want?" This is absent from every tool in the [landscape](../landscape.md), including gh-aw. A native system could implement it, but gh-aw's architecture does not. See [intent-representation.md](intent-representation.md).

### Intent-authorization-tier-based autonomy

Different agent authority for different types of changes: auto-merge a dependency bump, require human review for an API change, block agent-authored modifications to CODEOWNERS. gh-aw's [integrity filtering](https://github.github.com/gh-aw/reference/integrity/) implements a form of input trust tiering (`merged > approved > unapproved > none`) and its [supply chain protection](https://github.github.com/gh-aw/reference/threat-detection/#supply-chain-protection-protected-files) blocks modifications to sensitive files (dependency manifests, CI config, CODEOWNERS) by default — but these are applied to *what the agent can see and touch*, not to *whether the agent's output should be merged*. The output model remains flat: agent proposes, human decides, regardless of change type. Fullsend's autonomy spectrum applies to the merge decision itself. See [autonomy-spectrum.md](autonomy-spectrum.md).

### Governance

Who controls agent policies org-wide? How do those policies evolve? Who can change what agents are allowed to do? gh-aw leaves this to whoever writes the markdown workflow files — there is no governance framework, no policy hierarchy, no audit trail of policy changes. See [governance.md](governance.md).

### Zero-trust inter-agent review

Agents treating each other's output as untrusted, with blocking power derived from forge permissions rather than narrative trust. gh-aw's single-agent-per-workflow model has no concept of inter-agent interaction. See [agent-architecture.md](agent-architecture.md) and [code-review.md](code-review.md).

## Problems both face, where nativeness helps

These are shared concerns where gh-aw's native position provides a structural advantage.

### Run-trigger authorization (the "pwn request" gap)

GitHub Actions has no built-in approval gate for `issue_comment` or `issues.opened` triggers — unlike `pull_request` from public forks, which can require a maintainer's approval before running. Any user who can comment on an issue or open one fires the workflow immediately, full stop. [GitHub Security Lab named this class of gap "pwn request"](https://securitylab.github.com/resources/github-actions-preventing-pwn-requests/) in 2021; their original writeup focuses on `pull_request_target` code execution, but the same absence of a platform-level trigger gate applies to any social-interaction event. For a workflow that runs paid LLM inference, this means external cost exposure and an abuse surface with no rate limit — an attacker doesn't need a security bug, just a repo they can comment on.

Fullsend closes this itself: nearly every dispatch path — slash commands and automatic event triggers alike — is gated on collaborator repository permission before dispatching an agent, via a parameterized `has_repo_permission` check against the collaborator-permission API (observation stages like triage/review require `triage`+, mutation stages like code/fix require `write`+) or, for label-triggered handoffs, the implicit requirement that applying a label itself needs write access ([ADR 0054](../ADRs/0054-require-authorization-on-all-agent-dispatch-paths.md)). The `issue_comment` needs-info re-triage path intentionally uses a weaker gate (any non-`NONE` author association, or the issue's own author), and `retro` on PR-close is intentionally left ungated for read-only lifecycle accounting. This was not the default behavior; it had to be built and applied consistently after discovering some dispatch paths were gated and others weren't.

It is not stated in gh-aw's public documentation whether it applies an equivalent check on *who* may trigger a workflow run, as opposed to controlling what a triggered agent's output is allowed to do via safe-outputs and integrity filtering. Until verified, treat gh-aw workflows triggered by `issue_comment`/`issues.opened` as inheriting the same ungated-trigger behavior as any other GitHub Actions workflow.

### Cost/token observability

Both systems need visibility into inference spend. Fullsend already emits cost and token metrics via OpenTelemetry to whatever backend the operator configures (MLflow, or any OTLP-compatible provider). gh-aw ships [`max-ai-credits`](https://github.github.com/gh-aw/reference/frontmatter/#ai-credits-guardrail-max-ai-credits) as a per-run hard budget plus [`gh aw logs` / `gh aw audit`](https://github.github.com/gh-aw/reference/cost-management/) for inline cost and token inspection from the CLI. The underlying capability is equivalent; gh-aw's edge is convenience — the numbers are visible directly against the workflow run without a separate dashboard. That is a UX nicety fullsend could add via a post-script or a separate, non-blocking workflow, not a capability gap to close.

### Injection defense

Both systems must defend against prompt injection via untrusted input (issue text, PR descriptions, code comments). gh-aw's defense is layered and substantially deeper than a single output scan:

- **Content sanitization** at the input boundary: @mention neutralization, bot trigger protection, XML/HTML tag conversion, URI filtering (only HTTPS from trusted domains), unicode normalization, content size limits (0.5MB/65k lines), and control character removal. This runs before the agent sees any untrusted content.
- **[Integrity filtering (DIFC)](https://github.github.com/gh-aw/reference/integrity/)**: a trust-based system that filters GitHub content by author association level (`merged > approved > unapproved > none > blocked`). On public repos, `min-integrity: approved` is applied by default — content from untrusted external contributors is removed before the agent sees it. Supports `trusted-users`, `blocked-users`, and `approval-labels` overrides, with centralized management via GitHub organization variables. Filtered events are logged as `DIFC_FILTERED` for audit.
- **Substrate containment**: read-only token, AWF network firewall with domain allowlisting, MCP server isolation per container.
- **Threat detection**: AI-powered output scan plus optional custom scanners (Semgrep, TruffleHog, LlamaGuard) before any write is externalized.
- **[Supply chain protection](https://github.github.com/gh-aw/reference/threat-detection/#supply-chain-protection-protected-files)**: static rule-based blocking of agent modifications to dependency manifests, CI/CD config, agent instruction files, and CODEOWNERS, with `blocked/allowed/fallback-to-issue` policies.

Fullsend's experiments show that pre-LLM scanners alone are insufficient — [Model Armor caught ~8–25% of payloads](https://github.com/fullsend-ai/experiments/tree/main/guardrails-eval); [LLM Guard in sentence mode caught ~83%](https://github.com/fullsend-ai/experiments/tree/main/guardrails-eval). gh-aw's approach of combining input sanitization, trust-based content filtering, containment, and output scanning is a more comprehensive defense-in-depth than any single layer. And gh-aw's containment means the *consequence* of a missed injection is bounded (the agent can't do anything destructive), while fullsend must build equivalent containment from scratch.

Neither approach solves the *semantic* injection problem — an agent tricked into producing subtly wrong but plausible output that passes all scans. That requires the judgment-layer defenses (intent verification, specialized injection-defense sub-agents) that gh-aw does not attempt.

### Multi-agent coordination

Fullsend builds custom dispatch infrastructure for multi-agent pipelines (triage → code → review). gh-aw already provides native [orchestration primitives](https://github.github.com/gh-aw/patterns/orchestration/): `dispatch-workflow` (async fan-out to worker workflows via `workflow_dispatch`) and `call-workflow` (same-run fan-out via `workflow_call`, preserving actor attribution). An orchestrator workflow can decide what to do, split work into units, and dispatch typed worker workflows with scoped permissions and tools. [Cross-repository operations](https://github.github.com/gh-aw/reference/cross-repository/) allow workflows to read from and write to external repos via `target-repo`, `allowed-repos`, and cross-repo checkout — partially addressing the org-wide coordination that fullsend's centralized `.fullsend` repo provides. Both `dispatch-workflow` and `call-workflow` are same-repo only per gh-aw's own [safe-outputs reference](https://github.github.com/gh-aw/reference/safe-outputs/) — plausibly because the target needs `actions: write` and cross-repo dispatch would complicate billing/actor attribution, though gh-aw's docs don't state the rationale explicitly. Genuine cross-repo *workflow* fan-out instead relies on a separate, experimental `dispatch-repository` safe-output (`repository_dispatch` to external repos), while `target-repo`/`allowed-repos` on individual safe-outputs cover writing resources — not triggering runs — in other repos. Both still require the caller to supply their own PAT or GitHub App token — gh-aw does not provide one for you.

Fullsend's SDLC pipeline is itself orchestrated natively via GitHub — labels and issue/PR events as the state machine, the repo as coordinator, no privileged orchestrator process ([ADR 0002](../ADRs/0002-initial-fullsend-design.md)). gh-aw's `dispatch-workflow`/`call-workflow` primitives are a more explicit expression of the same underlying event-driven pattern, not a different architecture. This comparison is orthogonal to the [per-role App question above](#per-role-github-app-creation): orchestration primitives determine how work fans out to other workflow runs, not which identity authors the writes those runs produce, so they do not change the `GITHUB_TOKEN` retriggering behavior described there.

### Org-wide configuration

Fullsend creates a `.fullsend` config repo with `config.yaml`, normative specs, and centralized secrets. GitHub already provides org-level Actions policies, required workflows, organization rulesets, and org secrets with selected-repo visibility. A native system could leverage these directly rather than creating a parallel configuration layer.

## The trade-offs of nativeness

Building natively is not strictly superior. Platform lock-in introduces constraints that an external system avoids.

### Forge lock-in

Fullsend's forge abstraction ([ADR 0005](../ADRs/0005-forge-abstraction-layer.md)) means the same design could work on GitLab, Forgejo, or other platforms. gh-aw is GitHub-only by definition. For organizations that use multiple forges or may migrate, this matters. For GitHub-only organizations, it's cost without benefit.

Multi-forge portability is not the only reason GitHub-only is a narrower target than it looks, though. GitHub is fullsend's highest-value starting point, not its ceiling, because it's the platform where the harder trust problem shows up first: internal/enterprise repos have natural contributor segregation (only employees or vetted collaborators can comment, open issues, or open PRs), which bounds the abuse surface without extra engineering. Wide-open public upstream repos have no such segregation — any anonymous account can trigger automation (see [Run-trigger authorization](#run-trigger-authorization-the-pwn-request-gap) above) — so the same threat model has to hold at internet scale, not org scale. That distinction holds regardless of which forge is in play, and is arguably a bigger driver of fullsend's design than portability alone.

### Runtime constraints

GitHub Actions runners have a 6-hour job timeout, limited compute options (unless self-hosted), and restrictions on what can run in containers. Agents that need long-running sessions, GPU access, specialized build tooling, or persistent state across invocations may outgrow Actions. An external system can run agents anywhere — on Kubernetes, dedicated VMs, or specialized agent platforms.

### Product dependency

gh-aw is in early development and may change significantly. Building on it means depending on GitHub's product decisions, deprecation timeline, and willingness to support autonomous use cases. gh-aw's maintainers have publicly favored keeping humans on the merge button — an editorial stance rather than a platform-enforced restriction, but one that shapes what gh-aw is likely to build next. An external system can build merge authority independently of that stance.

### Isolation model

gh-aw's container isolation is strong for its use case, but the isolation boundary is defined by GitHub, not by the operator. Organizations with specific compliance, data residency, or security requirements may need control over the sandbox that a native system cannot provide.

## Open questions

- **Should fullsend adopt gh-aw as its containment and execution layer?** Rather than building parallel infrastructure for cross-repo dispatch, credential isolation, and agent sandboxing, fullsend could use gh-aw directly for the containment layer and focus its engineering effort on the judgment layer (intent verification, review composition, merge authority). gh-aw workflows are markdown files — agents can author them, aided by gh-aw's own [documentation MCP server](https://github.github.com/gh-aw/reference/gh-aw-as-mcp-server/). The `gh aw` CLI already handles compilation, security scanning, and lock file generation. Fullsend would not need to replicate any of this.

- **Is the forge abstraction worth the cost at this stage?** Fullsend's only concrete implementation is GitHub. If forge-neutrality is deferred to the [Infrastructure](../roadmap.md#infrastructure) and [Cross-forge orchestration](../roadmap.md#cross-forge-orchestration) work in the [roadmap](../roadmap.md), the current implementation could use GitHub-native primitives directly, simplifying the stack substantially.

- **Can gh-aw's safe-outputs model be extended for autonomous merge?** Nothing technical blocks this today — a plain workflow job triggered on `pull_request_review` (approved) or a passing check suite can call the merge REST endpoint or the merge-queue GraphQL mutation with a token that has write access, entirely outside gh-aw's safe-outputs system. The open question is whether gh-aw's maintainers formalize this as an official safe-output type (gated by required checks and a confidence threshold), which would make it a first-class, auditable primitive instead of something every adopter bolts on themselves. Given their stated editorial position, that seems unlikely to come from upstream — but it doesn't stop fullsend or anyone else from building it as an unofficial follow-up job today.

- **Where is the boundary between containment and judgment?** gh-aw solves containment well. Fullsend's unique value is judgment. Should fullsend's implementation focus exclusively on the judgment layer and delegate containment to native platform features wherever possible?

## Possible next step: gh-aw containment POC

One way to test the native-vs-external trade-off empirically would be to implement one fullsend pipeline stage (e.g. triage or review) as a gh-aw workflow, checking whether gh-aw's containment model, orchestration primitives, and safe-outputs are sufficient as a substrate for fullsend's judgment layer.

gh-aw provides an [MCP server for CLI access](https://github.github.com/gh-aw/reference/gh-aw-as-mcp-server/) (compiling, running, and managing workflows), and we have configured a [gh-aw docs MCP server](https://github.github.com/gh-aw/llms.txt) in the fullsend development environment that agents can use to fetch gh-aw reference material (security architecture, safe-outputs spec, frontmatter reference, design patterns, etc.) while authoring workflow markdown. This means a POC could be largely agent-driven.

However, this approach carries real tensions that should be weighed:

- **ADR 0005 conflict:** The accepted [forge abstraction layer](../ADRs/0005-forge-abstraction-layer.md) decision assumes fullsend remains portable across GitHub, GitLab, and Forgejo. Adopting gh-aw as the containment layer would lock fullsend at the runtime and security architecture level — deeper than what the forge abstraction was designed to abstract over. A POC should be evaluated against whether it invalidates, defers, or supersedes ADR 0005.
- **Pre-GA platform risk:** gh-aw is in technical preview (~68 releases in 8 months), Linux-only, and may change significantly before GA. Building on it means depending on GitHub's product decisions, deprecation timeline, and feature stability.
- **Philosophical opposition:** gh-aw's maintainers have [publicly favored](https://github.blog/ai-and-ml/generative-ai/code-review-in-the-age-of-ai-why-developers-will-always-own-the-merge-button/) keeping humans on the merge button — fullsend's core thesis is that this decision should be automatable for routine changes. That stance doesn't block fullsend from adding a merge job on top of gh-aw's containment (nothing in GitHub's API prevents it), but it does mean gh-aw itself is unlikely to ever formalize merge as an official safe-output, so fullsend would own that layer entirely rather than building on upstream support for it.

If pursued, the POC would inform whether fullsend's implementation effort should shift from building containment infrastructure to building judgment-layer capabilities on top of gh-aw — but it should be treated as an experiment, not a commitment.
