# Configuring Agents with Skills

Fullsend agents use [agent skills](https://agentskills.io/) — self-contained
markdown documents that teach an agent how to perform a specific task. Each
default agent ships with built-in skills, and you can extend or replace them by
committing your own skills to your repository.

For general project-wide instructions (code style, test conventions,
architecture rules), see [Configuring with AGENTS.md](customizing-with-agents-md.md).
This guide covers skills specifically.

## What is a skill?

A skill is a directory containing a `SKILL.md` file with YAML frontmatter and
structured instructions. The agent loads the skill by name and follows its
instructions during execution.

```
.agents/skills/my-skill/
  SKILL.md           # skill definition (required)
  scripts/           # supporting scripts (optional)
    helper-script.sh
  references/        # reference data (optional)
    data.json
```

For portability across agent runtimes, store skills in `.agents/skills/` and
symlink `.claude/skills` to it:

```bash
ln -s ../.agents/skills .claude/skills
```

This way, skills are discoverable by fullsend's agent runtime and by any local
agent tooling developers use when working on the repo directly.

The `SKILL.md` has frontmatter declaring the skill's name and description,
followed by step-by-step instructions:

```markdown
---
name: my-skill
description: >-
  One-line summary of what this skill does.
---

# My Skill

Instructions the agent follows when this skill is invoked.

## Step 1: Gather context

...

## Step 2: Produce output

...
```

Skills can reference companion scripts and data files in the same directory,
giving agents the ability to dynamically gather information at runtime.

This repository also includes workflow-oriented skills exposed as portable
slash commands. For example, [`/nextwork`](../../../skills/nextwork/SKILL.md)
builds a readiness queue, while
[`/adr-corner`](../../../skills/adr-corner/SKILL.md) inventories open pull
requests that change Architecture Decision Records.

## Adding skills to your repository

Place skills in `.agents/skills/` in your target repository and symlink
`.claude/skills` to `.agents/skills`. All agents operating on your repo will
discover them automatically:

```
your-repo/
  .agents/skills/
    customer-research/
      SKILL.md
      scripts/
        query-salesforce.sh
    deployment-checks/
      SKILL.md
  .claude/skills -> ../.agents/skills
```

## Extending agents with repo skills

Skills you add to your repository are available to all fullsend agents
alongside the built-in skills. This is the primary way to give agents
domain-specific capabilities — linting rules, deployment checklists,
architecture constraints — without modifying any fullsend configuration.

Repo skills **extend** the agent's skill set. They do not replace built-in
skills. If a repo skill has the same name as a built-in skill, the built-in
version takes precedence and the repo version is ignored. Fullsend warns about
the collision before the agent starts. Use a unique name to extend the agent,
or intentionally replace it through [`base:` harness composition](#overriding-built-in-skills).

### Skill precedence

Fullsend uploads built-in skills to the agent's personal-level config
directory (`CLAUDE_CONFIG_DIR/skills/`). Repo skills live in the project-level
`.claude/skills/` directory. Claude Code resolves name collisions using
precedence:

```
Personal (CLAUDE_CONFIG_DIR/skills/)  >  Project (.claude/skills/)
         fullsend built-in skills            repo skills
```

A repo skill with a novel name (no collision) is always available. A repo
skill with a name matching a built-in skill is shadowed — the agent never
sees it. Fullsend logs a warning naming the shadowed skill and the supported
extension and override paths.

### Extension points

Some agents recognize skill names that do not ship with fullsend. Providing
these unlocks additional capabilities. See each agent's documentation for the
skills it supports — for example, the
[prioritize agent](../../agents/prioritize.md) uses a `customer-research` skill
when available.

## Overriding built-in skills

To intentionally **replace** a built-in skill with your own version, use
`base:` composition and config-driven agent registration. Register the
agent in `config.yaml` with a harness that uses `base:` to inherit from the
upstream harness, and include your replacement skill in the `skills:` list.
The replacement's skill identity — its declared `SKILL.md` `name:` when the
directory is on disk, otherwise its directory basename — must match the
built-in skill's identity exactly.

> **Declare the built-in's name in your override.** A built-in skill's
> identity is its declared `SKILL.md` `name:`, which is not always its
> directory basename: `skills/issue-labels/github` declares `issue-labels`.
> Your override replaces it when its own `SKILL.md` declares `issue-labels`,
> whatever its directory is called. Relative `skills:` entries are resolved
> against the `.fullsend` directory before the match.

See [Bring Your Own Agent](bring-your-own-agent.md) for the full
composition model and config-driven registration.

> **Compatibility note:** Skill identity (and the sandbox destination a
> skill uploads to) now follows the declared `SKILL.md` `name:` when one is
> present, not just the directory basename. If an existing built-in or
> custom skill's declared `name:` differs from its directory name, its
> sandbox path and override-matching behavior change starting with this
> release. Also, entries in a single harness's `skills:` list no longer
> replace each other when they share an identity (previously the later one
> won); only a child entry replacing a base entry overrides, and any
> remaining duplicate destination now fails at bootstrap. A skill's
> destination name, declared or directory, must be a single path segment of
> letters, digits, `.`, `-` and `_` that does not start with a dot; bootstrap
> rejects any other name.

### Built-in skills

These skills ship with fullsend and can be overridden via config-driven
agent registration:

| Agent | Skill | Purpose |
|-------|-------|---------|
| [Triage](../../agents/triage.md) | `issue-labels` | Label discovery and application during triage |
| [Code](../../agents/code.md) | `code-implementation` | Step-by-step implementation procedure |
| [Review](../../agents/review.md) | `code-review`, `pr-review`, `docs-review`, `issue-labels` | Review evaluation across dimensions |
| [Fix](../../agents/fix.md) | `fix-review` | Review feedback interpretation and fix strategy |
| [Prioritize](../../agents/prioritize.md) | `customer-research` | Customer data gathering for RICE scoring (extension point) |
| [Retro](../../agents/retro.md) | `retro-analysis`, `finding-agent-runs` | Workflow analysis and proposal generation |

## Overriding single files in a skill

You can change a few files of a shipped skill without copying the whole
skill. List the files on the skill's `skills:` entry in your harness. Each
file can be removed, replaced, or added. Every other file still comes from
the upstream skill, so you keep its updates.

The most common use is the review skill, `pr-review`. Its review personas
(the reviewers it can dispatch, such as `security` or `correctness`) are the
files in its `sub-agents/` directory. With overrides you can turn one off,
swap in your own version, or add a new one.

**Before you start:**

- A harness registered in `config.yaml` (see
  [Bring Your Own Agent](bring-your-own-agent.md)). To change a skill that a
  `base:` harness lists, repeat that skill entry in your child harness.
- The path of each file you want to change, relative to the skill's root
  directory (the directory that holds `SKILL.md`).

### Example: remove, replace and add a persona

This harness uses `pr-review` from `fullsend-ai/agents`, pinned to a
commit, and changes three of its files:

```yaml
skills:
  - https://github.com/fullsend-ai/agents/tree/4a6645268c6c8063c15ce5cb1b88a4f3930c74da/skills/pr-review#sha256=d0a639e6870c3e204db83855cdf1e4e10e6eb3ecfaee1695bbf8630843cb5c6c:
      # Remove the user-experience persona.
      sub-agents/user-experience.md: null
      # Replace the security persona with the version from an older commit.
      sub-agents/security.md: https://raw.githubusercontent.com/fullsend-ai/agents/cc64bb29346c43822ad5d9fd14351c7d98d642ca/skills/pr-review/sub-agents/security.md#sha256=93fa30d04fde0af9e96e115e5fcb27d3223ebfb9d456b87ad5638dba67e6e84f
      # Add a licensing persona from a file in your .fullsend directory.
      sub-agents/licensing.md: overrides/licensing.md
```

The added persona is `.fullsend/overrides/licensing.md`:

```markdown
---
name: licensing
description: License compliance dimension.
role: dimension
categories: [licensing]
model: haiku
tools: Read, Grep, Glob
---

# Licensing

Check that new dependencies carry a license compatible with the project.
```

Its `name:` is `licensing` because the file is `licensing.md`. A persona
whose `name:` differs from its file name is skipped.

Allow both URL prefixes in `.fullsend/config.yaml`:

```yaml
allowed_remote_resources:
  - https://github.com/fullsend-ai/agents/
  - https://raw.githubusercontent.com/fullsend-ai/agents/
```

Get the `#sha256=` value of a URL by hashing the bytes it serves:

```console
$ curl -sL https://raw.githubusercontent.com/fullsend-ai/agents/cc64bb29346c43822ad5d9fd14351c7d98d642ca/skills/pr-review/sub-agents/security.md | sha256sum
93fa30d04fde0af9e96e115e5fcb27d3223ebfb9d456b87ad5638dba67e6e84f  -
```

Pin URLs to a commit, not a branch: a branch changes, and the hash then
stops matching.

### Check the harness

`fullsend lint` checks the override paths, that local override files exist,
and that every URL is allowed. It does not download anything, so a wrong
`#sha256=` shows up only when the agent runs. With the example registered as
the agent `persona-probe`:

```console
$ fullsend lint --fullsend-dir .fullsend
  ✓ Agent persona-probe resolved from config (local path)
```

### What you see in a run

The run prints the personas it staged in two places.

**The persona list file.** Every run writes `personas.json`: the list of
review personas staged for this run, after your overrides. Scripts and
skills read it through the `FULLSEND_PERSONAS_FILE` environment variable.
This pre-script prints it:

```bash
#!/usr/bin/env bash
set -euo pipefail
echo "FULLSEND_PERSONAS_FILE=${FULLSEND_PERSONAS_FILE}"
jq -r '.personas[] | "\(.skill)/\(.name) \(.origin) \(.path)"' "$FULLSEND_PERSONAS_FILE"
jq -r '.removed[] | "removed: \(.skill) \(.path)"' "$FULLSEND_PERSONAS_FILE"
```

For the example harness it prints this before the sandbox starts (the run
directory is shortened):

```text
  • Running pre-script: .../scripts/persona-probe.sh
FULLSEND_PERSONAS_FILE=.../out/fs-per-e5e8a1ee86e6/personas.json
pr-review/challenger upstream sub-agents/challenger.md
pr-review/correctness upstream sub-agents/correctness.md
pr-review/cross-repo-contracts upstream sub-agents/cross-repo-contracts.md
pr-review/docs-currency upstream sub-agents/docs-currency.md
pr-review/intent-coherence upstream sub-agents/intent-coherence.md
pr-review/licensing added sub-agents/licensing.md
pr-review/risk-assessment upstream sub-agents/risk-assessment.md
pr-review/security override sub-agents/security.md
pr-review/security-triage upstream sub-agents/security-triage.md
pr-review/style-conventions upstream sub-agents/style-conventions.md
removed: pr-review sub-agents/user-experience.md
```

`user-experience` is gone, `security` is your version, and `licensing` is
new. The two changed entries in the file look like this:

```console
$ jq '{personas: [.personas[] | select(.origin != "upstream")], removed}' "$FULLSEND_PERSONAS_FILE"
{
  "personas": [
    {
      "name": "licensing",
      "skill": "pr-review",
      "path": "sub-agents/licensing.md",
      "origin": "added",
      "sha256": "2628d3d6a40a2cfc489d3dcc3276e3e068a7437bf8eb75fd59b69e72d56df41c",
      "description": "License compliance dimension.",
      "role": "dimension",
      "categories": [
        "licensing"
      ]
    },
    {
      "name": "security",
      "skill": "pr-review",
      "path": "sub-agents/security.md",
      "origin": "override",
      "sha256": "93fa30d04fde0af9e96e115e5fcb27d3223ebfb9d456b87ad5638dba67e6e84f",
      "description": "Evaluates security vulnerabilities, auth/access control, data exposure, injection defense, privilege escalation, and content security."
    }
  ],
  "removed": [
    {
      "skill": "pr-review",
      "path": "sub-agents/user-experience.md"
    }
  ]
}
```

The `security` entry's `sha256` equals the `#sha256=` in the harness, which
shows your file was staged.

**The run log (`pi` runtime).** pi logs one line per persona it registers.
The same run logs `licensing` and `security`, and no `user-experience`:

```text
subagents: intent-coherence → anthropic-vertex/claude-sonnet-5-5 (from frontmatter)
subagents: licensing → anthropic-vertex/claude-haiku-5-5 (from frontmatter)
subagents: risk-assessment → anthropic-vertex/claude-sonnet-5-5 (from frontmatter)
subagents: security → anthropic-vertex/claude-sonnet-5-5 (from frontmatter)
```

### Override rules

**Keys** name one file inside the skill:

- Relative to the skill root, with `/` separators, because the key is a path
  inside the skill.
- No leading `/` and no `..`, so a key cannot reach outside the skill.
- In clean form (no `./`, `//` or trailing `/`), so two keys cannot name the
  same file.
- A file, not a directory. Override each file of a directory separately.

**Values** say what happens to that file:

| Value | File exists in the skill | File does not exist |
|-------|--------------------------|---------------------|
| `null` | Removed | Warning; nothing changes |
| Local path or URL | Replaced; keeps its file mode | Added with mode `0644` |

- A local path is relative to the `.fullsend` directory and must exist.
- A URL must use `https://`, end in `#sha256=` with the file's hash, and be
  allowed by `allowed_remote_resources`. The runner checks the hash before
  the run starts.
- An override file must be a regular file. A symlink into another
  directory, or a symlinked directory inside `.fullsend` on the way to it,
  fails the run, so an override cannot pull in a file from outside your
  configuration.

**How a run applies them:**

- The runner copies the skill into the run's output directory and changes
  the copy. The skill in your `.fullsend` directory or in the download cache
  is never changed.
- This happens right after the harness loads, so the pre-script, the
  sandbox and the agent all see the same files.
- A skill that has overrides must not contain symlinks.
- Overrides work on every runtime (`claude`, `pi`, `codex`, `opencode` and
  `dummy`) and in runs that use a lock file.
- When a child harness and its `base:` list the same skill, their overrides
  merge. For the same file, the child wins.

### The persona list file

| Reader | Path | Environment variable |
|--------|------|----------------------|
| Pre-script, post-script, validation script | `<output-dir>/<sandbox-name>/personas.json` | `FULLSEND_PERSONAS_FILE` |
| Agent and skills in the sandbox | `/sandbox/workspace/.fullsend/personas.json` | `FULLSEND_PERSONAS_FILE` |

- The file is written on every run, even with no overrides. An empty list
  means no skill has personas.
- It lists the personas found with the same rules `pi` and `codex` use to
  discover them. A skill on any runtime can read it to pick from the staged
  personas instead of a fixed list.
- `pi` and `codex` can still drop a listed persona later, for example when
  its model is not available in the run or its tools are not supported. They
  log a warning when they do.
- A file in `sub-agents/` becomes a persona only when its `name:` equals its
  file name and is not a reserved name (`default`, `explore`, `agent`, the
  agent's own name or a built-in agent name). Other files are skipped with a
  warning.
- When two skills have a persona with the same name, the first skill in
  `skills:` wins and the other file is skipped with a warning.
- `origin` is `upstream` (unchanged), `override` (replaced) or `added`
  (new). `sha256` is the hash of the staged file.
- `description`, `role`, `when`, `categories`, `always` and
  `failure_severity` are copied from the persona's frontmatter. A field of
  the wrong type is left out with a warning.
- `removed` lists the personas a `null` override removed.
- The sandbox copy sits outside the repository checkout, so a pull request
  cannot plant its own. `env.sandbox` and `env.runner` cannot change the
  variable.

### If it goes wrong

Messages name the entry's position in `skills:` (`skills[0]` is the first)
and the file key.

| Message | Cause | Fix |
|---------|-------|-----|
| `skills[0].overrides[sub-agents/security.md]: integrity check failed for https://...: expected 93fa...840, got 93fa...84f` | The URL serves different bytes than the `#sha256=` says | Use the `got` value, and pin the URL to a commit |
| `... skills[0].source: URL "https://github.com/fullsend-ai/agents/tree/..." is not in allowed_remote_resources` | No `allowed_remote_resources` entry covers the URL | Add the URL prefix to `allowed_remote_resources` in `config.yaml` |
| `skills[0].overrides[KEY] URL must include #sha256=... integrity hash` | A URL value has no `#sha256=` | Append `#sha256=` and the file's hash |
| `skills[0]: override file path "../licensing.md" must not contain path traversal segments` | The key has `..` | Name a file inside the skill |
| `skills[0]: override file path "KEY" must be relative (no leading /)` | The key starts with `/` | Write the path relative to the skill root |
| `skills[0]: override file path "KEY" must be in clean form ("CLEAN")` | The key has `./`, `//` or a trailing `/` | Use the form shown in the message |
| `persona .../sub-agents/license-check.md skipped: frontmatter name "licensing" must equal file basename "license-check"` (warning) | The persona's `name:` differs from its file name | Make `name:` match the file name, or rename the key |
| `persona .../extra-review/sub-agents/security.md skipped: name already declared by skill "pr-review"` (warning) | Two skills have a persona with the same name; the first one wins | Rename one persona, or remove one with `null` |
| `persona PATH skipped: frontmatter name: is required` (warning) | The persona has no `name:` | Add `name:` equal to the file name |
| `skills[0]: override "KEY" is null but the skill has no such file; nothing removed` (warning) | The removed file is not in the skill | Fix the path, or drop the key |
| `skills[0]: override "KEY": reading override source: PATH resolves through a symlink to REAL` | The override file, or a directory on its path inside `.fullsend`, is a symlink | Commit the file itself at the path the harness names |
| `skills[0]: skill tree contains a symlink at "PATH"; overridden skills must contain only regular files and directories` | A skill with overrides contains a symlink | Replace the symlink with a regular file in the skill |
| `personas.json: FILE: frontmatter field "FIELD" is not a string; omitted` (warning) | A copied frontmatter field has the wrong type (also `is not a list of strings` or `is not a bool`) | Fix the field in the persona's frontmatter |

## When to use skills vs. AGENTS.md

Use **skills** when you need to change how a specific agent performs a specific
task — especially when the configuration involves domain knowledge, helper
scripts, or external data sources that only one agent needs.

Use **[AGENTS.md](customizing-with-agents-md.md)** for broad instructions that
apply to all agents and human contributors alike.

## Authoring skills that augment defaults

Adding a skill is easy; writing one that **works beside** shipped defaults is
harder. Defaults often use specific language and own concrete output fields.
A soft "prefer concise" skill will lose that contest.

When you are tuning a built-in agent (triage, retro, review, …):

1. Discover what the agent already loads (harness `skills:`, agent markdown,
   result schema, post-script human surfaces).
2. Choose the right artifact: unique-named **augmentation skill**,
   **sub-agent** under an orchestrator, or whole-skill override — not a
   same-named drop-in that gets shadowed.
3. Prefer hard limits and field ownership over soft preferences.
4. Re-check current docs for the lightest shipping path (repo skill, harness
   pin, [file-level override](#overriding-single-files-in-a-skill), or
   upstream contribution).

For the full procedure, use the contributor skill
[`author-fullsend-augmentations`](../../../skills/author-fullsend-augmentations/SKILL.md)
(local checkout of this repo, or any environment that loads
`skills/author-fullsend-augmentations/`).

Bring Your Own Agent covers the same decision frame in
[Tuning agents with augmentation skills](customizing-agents.md#tuning-agents-with-augmentation-skills).

To change one sub-agent of a shipped skill without forking the whole tree,
use [per-file overrides](#overriding-single-files-in-a-skill).

## What not to do

- **Don't duplicate AGENTS.md content in skills.** If an instruction applies
  to all agents, put it in `AGENTS.md`. Skills are for agent-specific behavior.
- **Don't reuse a built-in skill's identity** (its declared `SKILL.md`
  `name:`, or directory name when there is no `SKILL.md`) unless you intend a
  supported whole-skill override path — same-identity project skills are
  shadowed by built-ins (see [Skill precedence](#skill-precedence)).

## See also

- [`fullsend agent new`](../../cli/agent.md#agent-new) — generate a custom agent that can then mount these skills

- [Customizing Agents](customizing-overview.md) — overview of all customization approaches
- [Bring Your Own Agent](bring-your-own-agent.md) — building, registering, and
  [tuning existing agents](customizing-agents.md#tuning-agents-with-augmentation-skills)
- [`author-fullsend-augmentations`](../../../skills/author-fullsend-augmentations/SKILL.md)
  — discovery-driven authoring procedure for augmentations and sub-agents
- [Default, derived, and custom agents](default-vs-custom.md)
  — adding skills keeps you in "configured default agent" territory
