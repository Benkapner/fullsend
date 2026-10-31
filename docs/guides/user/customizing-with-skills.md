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

A whole-skill override makes you maintain a full copy of the skill. To change
only a few files, such as one sub-agent of a review skill, keep the upstream
skill and list per-file overrides on its harness `skills:` entry. Each run
copies the skill into a fresh directory, applies your overrides to the copy,
and uploads the copy to the sandbox. The upstream skill, whether it sits in
your `.fullsend` directory or in the fetch cache, is never modified.

**Before you start:**

- A harness registered in `config.yaml` (see
  [Bring Your Own Agent](bring-your-own-agent.md)). To change a skill that a
  `base:` harness lists, repeat that skill entry in your child harness.
- The paths of the files you want to change, relative to the skill's root
  directory (the directory that holds `SKILL.md`).

### Example: remove, replace and add files

This harness takes the `pr-review` skill from `fullsend-ai/agents`, pinned to
a commit, and changes three files in it:

```yaml
skills:
  - https://github.com/fullsend-ai/agents/tree/4a6645268c6c8063c15ce5cb1b88a4f3930c74da/skills/pr-review#sha256=d0a639e6870c3e204db83855cdf1e4e10e6eb3ecfaee1695bbf8630843cb5c6c:
      # Remove: null deletes the file from this run's copy.
      sub-agents/user-experience.md: null
      # Replace: the upstream skill has this file. Here the replacement is
      # the same file at an older commit, pinned by the SHA-256 of its bytes.
      sub-agents/security.md: https://raw.githubusercontent.com/fullsend-ai/agents/cc64bb29346c43822ad5d9fd14351c7d98d642ca/skills/pr-review/sub-agents/security.md#sha256=93fa30d04fde0af9e96e115e5fcb27d3223ebfb9d456b87ad5638dba67e6e84f
      # Add: the upstream skill has no such file.
      sub-agents/licensing.md: overrides/licensing.md
```

The added file lives in your `.fullsend` directory as
`overrides/licensing.md`. Its frontmatter `name:` matches the file name, so
the runtimes register it as a persona:

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

Every URL, the skill's and the override's, must be covered by
`allowed_remote_resources` in `.fullsend/config.yaml`:

```yaml
allowed_remote_resources:
  - https://github.com/fullsend-ai/agents/
  - https://raw.githubusercontent.com/fullsend-ai/agents/
```

A harness can also declare its own `allowed_remote_resources`, but each entry
must be covered by the `config.yaml` list.

To get the `#sha256=` value, hash the exact bytes the URL serves:

```console
$ curl -sL https://raw.githubusercontent.com/fullsend-ai/agents/cc64bb29346c43822ad5d9fd14351c7d98d642ca/skills/pr-review/sub-agents/security.md | sha256sum
93fa30d04fde0af9e96e115e5fcb27d3223ebfb9d456b87ad5638dba67e6e84f  -
```

Pin the URL to a commit SHA, not a branch, so the hash keeps matching.

### Override rules

An entry with overrides is a single-key map. The key is the skill source (a
local path or a pinned URL, as in the plain string form). The value maps
paths inside the skill to override values.

**Keys** name a file inside the skill:

- Relative to the skill root, with `/` separators.
- No leading `/`, no `..` segment, no null bytes.
- In clean form: no `./` prefix, no `//`, no trailing `/`. Two keys can
  then never name the same file.
- A file, never a directory.

**Values** decide what happens to that file:

| Value | Key exists upstream | Key does not exist upstream |
|-------|---------------------|-----------------------------|
| `null` | The file is removed from this run's copy | Warning; nothing changes |
| Local path or URL | The file is replaced; it keeps the upstream file mode | The file is added with mode `0644`; missing parent directories are created |

- A local path resolves from the `.fullsend` directory, like every other
  relative harness path. It must exist.
- A URL must use `https://`, carry a `#sha256=` hash of the file, and be
  covered by `allowed_remote_resources`. The runner fetches it during harness
  resolution and checks the hash before the run continues.

**Staging:**

- The runner copies each skill that has overrides to
  `<output-dir>/<sandbox-name>/skills/<index>/<skill-name>` right after
  harness resolution, then applies the overrides there. This happens before
  the runner decides which providers pi sub-agents need, before the sandbox
  is created and before the pre-script, so all of them see the merged tree.
  `<index>` is the entry's position in the resolved `skills:` list.
- Only regular files and directories are copied, with their modes. The
  skill root may be a link (a URL skill's root is a link into the fetch
  cache), but a symlink inside the skill tree fails the run.
- An override file must be a regular file inside the `.fullsend` directory
  (URL overrides are cached there). A symlinked file, or a symlinked
  directory inside `.fullsend` on the way to it, fails the run. Symlinks
  above the `.fullsend` directory, such as macOS `/tmp`, are fine.
- Entries without overrides are uploaded from their resolved location, as
  before.
- Overrides work on every runtime (`claude`, `pi`, `codex`, `opencode` and
  `dummy`), and in runs that resolve from a lock file.

**Composition:** when a child harness lists a skill whose identity matches a
`base:` entry, the child entry replaces the base entry and the two override
maps merge. For the same key, the child's value wins.

### Check the harness before a run

`fullsend lint` validates override keys, checks that local override files
exist, and checks URL overrides against the allowlists. It does not fetch
URLs, so a wrong `#sha256=` value only shows up at run time. For the example
above, registered as the agent `persona-probe`:

```console
$ fullsend lint --fullsend-dir .fullsend
  ✓ Agent persona-probe resolved from config (local path)
```

Without the `https://github.com/fullsend-ai/agents/` entry it fails:

```console
$ fullsend lint --fullsend-dir .fullsend
  ✓ Agent persona-probe resolved from config (local path)
Error: validating allowed remote resources for agent "persona-probe": skills[0].source: URL "https://github.com/fullsend-ai/agents/tree/4a6645268c6c8063c15ce5cb1b88a4f3930c74da/skills/pr-review" is not in allowed_remote_resources
```

Checks that need the staged tree, such as symlinks in the skill and
directory keys, run only in `fullsend run`, as do the persona warnings
below.

### The persona manifest

Every run writes a persona manifest, `personas.json`, even when the harness
has no overrides. It lists exactly the sub-agent personas the `pi` and
`codex` runtimes register from the staged skills, after overrides are
applied, so scripts and skills can read the final roster instead of
guessing it.

A run of the example harness writes a manifest with ten `pr-review`
personas: eight unchanged, plus `security` and `licensing`. The changed
entries and the removal, from that run's `personas.json`:

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

The `security` entry's `sha256` is the `#sha256=` of the override URL, so a
reader can tell which bytes were staged. On the `pi` runtime the same run
logs one registration line per manifest entry, and none for
`user-experience`:

```text
subagents: intent-coherence → anthropic-vertex/claude-sonnet-5-5 (from frontmatter)
subagents: licensing → anthropic-vertex/claude-haiku-5-5 (from frontmatter)
subagents: risk-assessment → anthropic-vertex/claude-sonnet-5-5 (from frontmatter)
subagents: security → anthropic-vertex/claude-sonnet-5-5 (from frontmatter)
```

**Manifest rules:**

- One entry per registered persona. The runner reads the staged
  `sub-agents/*.md` files of each skill (subdirectories of `sub-agents/`
  are not read) with the same rules the runtimes use.
- A file is registered only when its frontmatter parses and its `name:`
  equals the file name without `.md`. The name must be lowercase
  alphanumeric segments joined by hyphens, at most 64 characters, and not
  reserved (`default`, `explore`, `agent`, the agent's own name, a
  built-in agent name or a Claude Code built-in agent type).
- Any other file is skipped with a warning in the run log, and the run goes
  on. The persona is then missing from the manifest and from the runtime.
- When two skills declare the same name, the first skill in the resolved
  `skills:` list wins and the other file is skipped with a warning.
- Entries are sorted by `skill`, then by `name`.
- `skill` is the skill's sandbox name. `path` is relative to the skill root,
  with `/` separators.
- `origin` is `upstream` (unchanged file), `override` (replaced file) or
  `added` (new file).
- `sha256` is the hex SHA-256 of the staged file's bytes.
- `description`, `role`, `when`, `categories`, `always` and
  `failure_severity` are copied verbatim from the frontmatter when present
  and omitted when absent or `null`. The runner checks only their types:
  `categories` must be a list of strings, `always` a boolean, and the rest
  strings. A field of the wrong type is left out of that entry with a
  warning naming the file and field. Other frontmatter keys are ignored.
- `removed` lists every `null` override that removed an upstream
  `sub-agents/*.md` file.
- Empty lists are written as `[]`.

### Where to read the manifest

| Reader | Path | Environment variable |
|--------|------|----------------------|
| Pre-script, post-script, validation script (host) | `<output-dir>/<sandbox-name>/personas.json` | `FULLSEND_PERSONAS_FILE` (absolute host path) |
| Agent and skills (sandbox) | `/sandbox/workspace/.fullsend/personas.json` | `FULLSEND_PERSONAS_FILE` |

The runner sets `FULLSEND_PERSONAS_FILE` itself. `env.runner` and
`runner_env` do not override it in host scripts, and `env.sandbox` drops it
as a reserved name. Other ways to set sandbox variables, such as a
`host_files` entry that writes into the sandbox `.env.d` directory, are not
blocked, so treat the variable as set by the runner only when the harness
does not do that. The sandbox copy sits outside the
target repository checkout, so a pull request cannot plant its own manifest.

A pre-script or post-script can read the roster with `jq`. This script prints
it:

```bash
#!/usr/bin/env bash
set -euo pipefail
echo "FULLSEND_PERSONAS_FILE=${FULLSEND_PERSONAS_FILE}"
jq -r '.personas[] | "\(.skill)/\(.name) \(.origin) \(.path)"' "$FULLSEND_PERSONAS_FILE"
jq -r '.removed[] | "removed: \(.skill) \(.path)"' "$FULLSEND_PERSONAS_FILE"
```

As the pre-script of the example harness, it prints this in the run log
before the sandbox starts (the run directory is shortened here):

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

A skill can tell the agent to read `$FULLSEND_PERSONAS_FILE` (or the sandbox
path above) to pick which personas to dispatch, instead of hard-coding a
roster that an override may have changed.

### If it goes wrong

Errors name the entry's position in the resolved `skills:` list (`N`) and
the override key (`KEY`). Errors from `fullsend lint` and harness loading are
prefixed with the agent name and the stage, such as
`loading harness for agent "review": invalid harness:`.

| Message | Cause | Fix |
|---------|-------|-----|
| `skills[N]: override file path "KEY" must be relative (no leading /)` | The key starts with `/` | Write the path relative to the skill root |
| `skills[N]: override file path "KEY" must not contain path traversal segments` | The key has a `..` segment | Name a file inside the skill |
| `skills[N]: override file path "KEY" must be in clean form ("CLEAN")` | The key has a `./` prefix, `//`, a `.` segment or a trailing `/` | Write the key as shown in the message |
| `skills[N]: override file path "KEY" must name a file in the skill` | The key is `.` | Name a file inside the skill |
| `skills[N]: override "KEY" names a directory; only files can be overridden` | The key is a directory in the skill | Override each file in the directory separately |
| `skills[N]: skill tree contains a symlink at "PATH"; overridden skills must contain only regular files and directories` | A skill that has overrides contains a symlink | Replace the symlink with a regular file in the skill source; the copy fails before overrides apply, so a `null` override cannot remove it |
| `skills[N]: override "KEY": reading override source: ...` | The local override file or the fetched copy could not be read | Check the path, which resolves from the `.fullsend` directory |
| `skills[N]: override "KEY": reading override source: PATH resolves through a symlink to REAL` | The override file, or a directory on its path, is a symlink | Commit the file itself at the path the harness names |
| `skills[N]: skill has no resolved source; its file overrides are not applied` (warning) | The skill was already pulled in as a dependency of another resource, so this entry was dropped with its overrides | List the skill once, with its overrides |
| `skills[N].overrides[KEY]: stat ...: no such file or directory` (from `fullsend lint`) | The local override file does not exist | Fix the path or commit the file |
| `skills[N]: override "KEY" is null but the skill has no such file; nothing removed` (warning) | A `null` key names a file the upstream skill does not have | Fix the path, or drop the key if upstream already removed the file |
| `skills[N].overrides[KEY] URL must include #sha256=... integrity hash` | A URL override has no `#sha256=` hash | Append `#sha256=<hex digest of the file>` |
| `skills[N]: override source for "KEY" must not contain query or fragment markers` | A value with `?` or `#` was read as a local path, often an `http://` URL (only `https://` counts as a URL) | Use an `https://` URL, or drop the `?`/`#` from the local path |
| `... URL "..." is not in allowed_remote_resources` | No allowlist entry covers the override URL | Add a prefix to `allowed_remote_resources` in `config.yaml` |
| `allowed_remote_resources[N]: "..." is not covered by the org allowlist` | The harness allowlist names a prefix that `config.yaml` does not cover | Add the prefix to `config.yaml` too, or remove it from the harness |
| `skills[N].overrides[KEY]: integrity check failed for ...: expected ..., got ...` | The fetched bytes do not match the `#sha256=` value | Recompute the hash, and pin the URL to a commit SHA |
| `persona PATH skipped: name already declared by skill "SKILL"` (warning) | Two skills declare the same persona name; the first one wins | Rename one persona, or remove one file with `null` |
| `persona PATH skipped: frontmatter name "NAME" must equal file basename "STEM"` (warning) | The file's `name:` differs from its file name, for example an override that reuses another persona's file | Make `name:` match the file name |
| `persona PATH skipped: frontmatter name: is required` (warning) | The file has no frontmatter `name:` | Add `name:` equal to the file name |
| `persona PATH skipped: frontmatter does not parse: ...` (warning) | The file's YAML frontmatter is invalid | Fix the frontmatter in the override file, or override the broken upstream file |
| `persona PATH skipped: name is reserved` and similar (warning) | The name breaks a naming rule above | Rename the persona |
| `personas.json: FILE: frontmatter field "FIELD" is not a string; omitted` (warning) | `description`, `role`, `when` or `failure_severity` is not a string | Quote the value |
| `personas.json: FILE: frontmatter field "FIELD" is not a list of strings; omitted` (warning) | `categories` is not a list of strings | Write `categories: [a, b]` |
| `personas.json: FILE: frontmatter field "FIELD" is not a bool; omitted` (warning) | `always` is not `true` or `false` | Use an unquoted `true` or `false` |

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
