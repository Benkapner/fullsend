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

This harness keeps the `pr-review` skill and changes four files in it:

```yaml
role: review
agent: agents/review.md
policy: policies/base.yaml
skills:
  - skills/pr-review:
      # Replace: the upstream skill has this file.
      sub-agents/security.md: overrides/security.md
      # Add: the upstream skill has no such file.
      sub-agents/licensing.md: overrides/licensing.md
      # Remove: null deletes the file from this run's copy.
      sub-agents/user-experience.md: null
      # Add from a URL, pinned by the SHA-256 of the file's bytes.
      sub-agents/supply-chain.md: https://raw.githubusercontent.com/example-org/agents/<commit-sha>/sub-agents/supply-chain.md#sha256=<sha256-of-file>
```

The URL override must be covered by `allowed_remote_resources`. Add the prefix
to `.fullsend/config.yaml`:

```yaml
allowed_remote_resources:
  - https://raw.githubusercontent.com/example-org/agents/
```

A harness can also declare its own `allowed_remote_resources`, but each entry
must be covered by the `config.yaml` list.

To get the `#sha256=` value, hash the exact bytes you publish. For a file you
have locally, run `sha256sum`:

```console
$ sha256sum overrides/security.md
6104df20f8080c6c4b1b4af08be98a87cba4163e9dcc68c371835ad581a3c631  overrides/security.md
```

Pin the URL to a commit SHA, not a branch, so the hash keeps matching.

### Override rules

An entry with overrides is a single-key map. The key is the skill source (a
local path or a pinned URL, as in the plain string form). The value maps
paths inside the skill to override values.

**Keys** name a file inside the skill:

- Relative to the skill root, with `/` separators.
- No leading `/`, no `..` segment, no null bytes.
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
  `<output-dir>/<sandbox-name>/skills/<index>/<skill-name>`, after harness
  resolution and before the pre-script, then applies the overrides there.
  `<index>` is the entry's position in the resolved `skills:` list.
- Only regular files and directories are copied, with their modes. A symlink
  anywhere in the skill tree fails the run.
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
above, with a concrete commit SHA and hash in place of the placeholders:

```console
$ fullsend lint --fullsend-dir .fullsend
  ✓ Agent review resolved from config (local path)
```

The same harness without the `allowed_remote_resources` entry in
`config.yaml` fails:

```console
$ fullsend lint --fullsend-dir .fullsend
  ✓ Agent review resolved from config (local path)
Error: validating allowed remote resources for agent "review": skills[0].overrides.sub-agents/supply-chain.md: URL "https://raw.githubusercontent.com/example-org/agents/0123456789abcdef0123456789abcdef01234567/sub-agents/supply-chain.md" is not in allowed_remote_resources
```

Checks that need the staged tree, such as symlinks in the skill, directory
keys and persona frontmatter, run only in `fullsend run`.

### The persona manifest

Every run writes a persona manifest, `personas.json`, even when the harness
has no overrides. It lists the sub-agent personas the agent can dispatch
after overrides are applied, so scripts and skills can read the final roster
instead of guessing it.

Given these override files:

```markdown
---
name: security
description: Security dimension with our threat model.
role: dimension
categories: [injection, authz]
always: true
failure_severity: high
---
Our security persona.
```

```markdown
---
name: licensing
description: License compliance dimension.
role: dimension
---
Licensing persona.
```

and the harness above without the URL entry, the manifest for an upstream
`pr-review` skill whose only sub-agents are `security.md` and
`user-experience.md` has this content. It is derived from the rules below,
not captured from a run; a full `fullsend run` needs an OpenShell sandbox.
The `sha256` values are the real hashes of the two files shown.

```json
{
  "version": 1,
  "personas": [
    {
      "name": "licensing",
      "skill": "pr-review",
      "path": "sub-agents/licensing.md",
      "origin": "added",
      "sha256": "d4eeeb52ac9ceabedced9b6f346d522b819e4c92f0f3c3aeed4ef4a0c051d057",
      "description": "License compliance dimension.",
      "role": "dimension"
    },
    {
      "name": "security",
      "skill": "pr-review",
      "path": "sub-agents/security.md",
      "origin": "override",
      "sha256": "6104df20f8080c6c4b1b4af08be98a87cba4163e9dcc68c371835ad581a3c631",
      "description": "Security dimension with our threat model.",
      "role": "dimension",
      "categories": ["injection", "authz"],
      "always": true,
      "failure_severity": "high"
    }
  ],
  "removed": [
    {"skill": "pr-review", "path": "sub-agents/user-experience.md"}
  ]
}
```

**Manifest rules:**

- One entry per staged `sub-agents/*.md` file in each skill. Subdirectories
  of `sub-agents/` are not read.
- Entries are sorted by `skill`, then by `name`.
- `skill` is the skill's sandbox name. `path` is relative to the skill root,
  with `/` separators.
- `name` is the frontmatter `name:`, or the file name without `.md` when the
  frontmatter has none. Two personas with the same name fail the run, even
  when they come from different skills.
- `origin` is `upstream` (unchanged file), `override` (replaced file) or
  `added` (new file).
- `sha256` is the hex SHA-256 of the staged file's bytes.
- `description`, `role`, `when`, `categories`, `always` and
  `failure_severity` are copied verbatim from the frontmatter when present
  and omitted when absent. The runner checks only their types:
  `categories` must be a list of strings, `always` a boolean, and the rest
  strings. A wrong type or frontmatter that does not parse fails the run.
  Other frontmatter keys are ignored.
- `removed` lists every `null` override that removed an upstream file under
  `sub-agents/`.
- Empty lists are written as `[]`.

### Where to read the manifest

| Reader | Path | Environment variable |
|--------|------|----------------------|
| Pre-script, post-script, validation script (host) | `<output-dir>/<sandbox-name>/personas.json` | `FULLSEND_PERSONAS_FILE` (absolute host path) |
| Agent and skills (sandbox) | `/sandbox/workspace/.fullsend/personas.json` | `FULLSEND_PERSONAS_FILE` |

The runner sets `FULLSEND_PERSONAS_FILE` itself. A harness cannot override it:
`env.runner` and `runner_env` do not reach it in host scripts, and
`env.sandbox` drops it as a reserved name. The sandbox copy sits outside the
target repository checkout, so a pull request cannot plant its own manifest.

A script can read the roster with `jq`. Run against the manifest above:

```console
$ jq -r '.personas[] | "\(.skill)/\(.name) \(.origin)"' "$FULLSEND_PERSONAS_FILE"
pr-review/licensing added
pr-review/security override
$ jq -r '.removed[] | "\(.skill) \(.path)"' "$FULLSEND_PERSONAS_FILE"
pr-review sub-agents/user-experience.md
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
| `skills[N]: override file path "KEY" resolves outside the skill directory` | The key does not name a file inside the skill once cleaned (for example `.`) | Name a file inside the skill |
| `skills[N]: override "KEY" names a directory; only files can be overridden` | The key is a directory in the skill | Override each file in the directory separately |
| `skills[N]: skill tree contains a symlink at "PATH"; overridden skills must contain only regular files and directories` | A skill that has overrides contains a symlink | Replace the symlink with a regular file in the skill source; the copy fails before overrides apply, so a `null` override cannot remove it |
| `skills[N]: override "KEY": reading override source: ...` | The local override file or the fetched copy could not be read | Check the path, which resolves from the `.fullsend` directory |
| `skills[N].overrides[KEY]: stat ...: no such file or directory` (from `fullsend lint`) | The local override file does not exist | Fix the path or commit the file |
| `skills[N]: override "KEY" is null but the skill has no such file; nothing removed` (warning) | A `null` key names a file the upstream skill does not have | Fix the path, or drop the key if upstream already removed the file |
| `skills[N].overrides[KEY] URL must include #sha256=... integrity hash` | A URL override has no `#sha256=` hash | Append `#sha256=<hex digest of the file>` |
| `skills[N]: override source for "KEY" must not contain query or fragment markers` | A value with `?` or `#` was read as a local path, often an `http://` URL (only `https://` counts as a URL) | Use an `https://` URL, or drop the `?`/`#` from the local path |
| `... URL "..." is not in allowed_remote_resources` | No allowlist entry covers the override URL | Add a prefix to `allowed_remote_resources` in `config.yaml` |
| `allowed_remote_resources[N]: "..." is not covered by the org allowlist` | The harness allowlist names a prefix that `config.yaml` does not cover | Add the prefix to `config.yaml` too, or remove it from the harness |
| `skills[N].overrides[KEY]: integrity check failed for ...: expected ..., got ...` | The fetched bytes do not match the `#sha256=` value | Recompute the hash, and pin the URL to a commit SHA |
| `personas.json: duplicate persona name "NAME" in FILE1 and FILE2` | Sub-agent files in two different staged skills resolve to one persona name (inside one skill a repeated name is allowed; entries are told apart by `path`) | Rename one persona, or remove one file with `null` |
| `personas.json: FILE: frontmatter does not parse: ...` | A staged sub-agent file has invalid YAML frontmatter | Fix the frontmatter in the override file, or override the broken upstream file |
| `personas.json: FILE: frontmatter field "FIELD" must be a string` | `name`, `description`, `role`, `when` or `failure_severity` is not a string | Quote the value |
| `personas.json: FILE: frontmatter field "FIELD" must be a list of strings` | `categories` is not a list of strings | Write `categories: [a, b]` |
| `personas.json: FILE: frontmatter field "FIELD" must be a bool` | `always` is not `true` or `false` | Use an unquoted `true` or `false` |

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
