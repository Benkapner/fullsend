---
name: authoring-skills
description: >-
  Guide for writing fullsend augmentation skills that work alongside default
  agent skills. Reads existing skills, identifies precedence conflicts, and
  suggests language patterns that complement rather than compete.
---

# Authoring Augmentation Skills

Help users write skills that **augment** default fullsend agent behavior
without fighting it. An augmentation skill adds domain knowledge, tightens
constraints, or reformats output — it does not replace the default skill's
procedure.

## When to use

- The user wants to create a new skill for their repo or `.fullsend` config.
- The user has a skill that is being ignored or overshadowed by a default
  skill.
- The user wants to understand what a default skill already covers before
  writing their own.
- The user is deciding whether to augment or fully override a default skill.

## How skills compose

Fullsend agents can load multiple skills in one session. When two skills
target the same behavior, the model follows whichever uses more specific
language. Default skills shipped with fullsend are generally very specific
(step-by-step procedures, schemas, field-level ownership). A vague
augmentation skill ("be concise", "prefer short prose") loses to a specific
default skill every time.

### Precedence rules

```
Agent definition (system prompt)   — highest priority
  > Built-in skills (shipped with fullsend)
    > Repo skills (.agents/skills/ or .claude/skills/)
      > AGENTS.md / CLAUDE.md project instructions
```

Name collisions: a repo skill with the same directory name as a built-in
skill is shadowed — the agent never sees it. Use a unique name to ensure
discovery.

Full details:
[Configuring with Skills](../../docs/guides/user/customizing-with-skills.md).

## Procedure

### 1. Understand the target agent

Before writing a skill, identify which agent(s) it will affect and read
their default skills.

**Built-in agent skills:**

| Agent | Built-in skills |
|-------|-----------------|
| Triage | `issue-labels` |
| Code | `code-implementation` |
| Review | `code-review`, `pr-review`, `docs-review`, `issue-labels` |
| Fix | `fix-review` |
| Prioritize | `customer-research` (extension point) |
| Retro | `retro-analysis`, `finding-agent-runs` |

Find the skill files in fullsend's `skills/` directory or the
[fullsend-ai/agents](https://github.com/fullsend-ai/agents) repo. Read
the SKILL.md for each built-in skill that overlaps with the behavior the
user wants to change. Note:

- What fields or outputs does the default skill own?
- What procedures or steps does it define?
- What language does it use — hard rules, templates, or soft guidance?

### 2. Classify the change

Determine what the user's skill needs to do:

| Intent | Approach | Example |
|--------|----------|---------|
| Add domain knowledge no default covers | **New capability** — clean addition, no conflict risk | A `customer-research` skill for the prioritize agent |
| Change how a default-owned output looks | **Augmentation** — must declare field ownership | Shortening triage comments while keeping JSON intact |
| Replace a default skill's procedure entirely | **Override** — use config-driven registration | Replacing `code-implementation` with an org-specific workflow |
| Add constraints on top of default behavior | **Augmentation** — use hard rules, not soft preferences | Banning certain patterns in review findings |

If the intent is a full override, direct the user to
[config-driven agent registration](../../docs/guides/user/bring-your-own-agent.md)
and the
[escalation ladder](../../docs/agents/topics/escalation-ladder.md).
Override means the user ships their own version and loses future upstream
improvements — they should be sure.

### 3. Analyze for conflicts

For augmentation skills, compare the user's draft (or intent) against the
default skills. Look for these conflict patterns:

**Field ownership collision.** Two skills both say what a field should
contain. Example: `retro-analysis` says `summary` should describe findings
in detail; a brevity skill says `summary` should be short. The model
follows whichever is more specific.

**Procedure overlap.** Two skills define steps for the same task. Example:
`code-implementation` has a 10-step implementation procedure; a custom
skill says "follow these 5 steps to implement." The model tries to satisfy
both and produces inconsistent results.

**Tone conflict.** One skill uses soft language ("prefer", "try to",
"consider"); the other uses hard language ("must", "never", "always").
Hard language wins. If the default skill is hard and the augmentation is
soft, the augmentation is effectively ignored.

**Scope ambiguity.** The augmentation skill says "shorten all output" but
the default skill owns specific fields that must stay complete. The model
cannot satisfy both, so it follows the more specific instruction.

### 4. Write the skill

Apply these patterns to make augmentation skills effective:

#### Declare field ownership explicitly

Name the exact fields or outputs your skill controls. Name the fields it
does **not** control. This eliminates ambiguity about scope.

```markdown
## What this skill controls

- `comment` field: word count, tone, structure
- `summary` field: length, bullet format

## What this skill does NOT control

- `reasoning`, `label_actions`, `scores` — owned by default skills
- `proposals[]` contents — owned by retro-analysis
```

#### Use hard limits, not soft preferences

Models treat "prefer concise" as optional. They treat "40 words maximum"
as a constraint. Always use hard language when the behavior must change.

| Weak (will be ignored) | Strong (will be followed) |
|------------------------|---------------------------|
| Prefer shorter comments | Limit `comment` to 40 words maximum |
| Try to avoid filler | Never start with "Thanks", "I've reviewed", or "This retro" |
| Consider using bullets | Use bullets, not paragraphs |
| Be more concise | Each bullet: one clause, one fact |

#### Provide templates

Give the model a concrete shape to fill in. Templates are more specific
than rules and harder for competing instructions to override.

```markdown
### Comment template (use this shape)

**Sufficient:**
Sufficient: <one-line why>. Next: <ready-to-code / waiting on X>.

**Insufficient:**
Insufficient: need <one fact>.
<one specific question>
```

#### Include before/after examples

Show the model what "wrong" and "right" look like for your specific case.
Concrete examples anchor behavior more effectively than abstract rules.

#### Add self-check rules

Give the model criteria to verify its own output before finishing. These
act as a last-pass filter.

```markdown
## Fail checks (rewrite if you hit these)

- `comment` starts with "Thanks" or "I've reviewed"
- `comment` exceeds 40 words
- `summary` retells the full workflow instead of pointing at proposals
- Any field owned by another skill was shortened
```

#### Acknowledge the default skill explicitly

When your augmentation skill targets behavior that a default skill also
influences, name the default skill and state the relationship. This
eliminates ambiguity for the model.

```markdown
The `retro-analysis` skill owns proposal quality and evidence.
This skill owns `summary` length and tone only. A long proposal body
with a short summary is success. A short proposal body is failure.
```

### 5. Review the draft

Check the skill against these criteria:

- [ ] **Unique name.** Does not collide with any built-in skill directory
  name (see table in step 1).
- [ ] **Field ownership declared.** Every field the skill touches is
  explicitly claimed. Every field it must not touch is explicitly excluded.
- [ ] **Hard language only.** No "prefer", "try to", "consider",
  "should try", or "it would be nice". Use "must", "never", "always",
  "do not", "limit to N".
- [ ] **Default skills acknowledged.** If the augmentation overlaps with a
  default skill, the skill names the default and states the boundary.
- [ ] **Templates provided.** For output formatting changes, concrete
  templates show the expected shape.
- [ ] **Self-checks included.** The skill lists conditions that indicate
  failure so the model can catch its own mistakes.
- [ ] **No procedure overlap.** The skill does not redefine steps that a
  default skill already owns. It adds constraints or knowledge, not
  alternative procedures.

### 6. Recommend placement

**Repo skill (no harness change needed):**

Place the skill in `.agents/skills/<skill-name>/SKILL.md` in the target
repo and symlink `.claude/skills` to `.agents/skills`. All agents discover
it automatically. Use this path when:

- The skill adds domain knowledge (architecture context, team conventions)
- The skill tightens constraints on output format or content
- The skill is specific to one repository

**Harness skill (needs config-driven registration):**

Add the skill to a `skills/` directory in the `.fullsend` config repo and
reference it in the harness `skills:` list via `base:` composition. Use
this path when:

- The skill should apply to all repos in the org
- The skill needs to be pinned to a specific version
- The skill is paired with a derived or custom agent

## Augment vs. override decision

Use this decision tree:

1. Does the default skill cover the behavior you want to change?
   - **No** → Write a new augmentation skill. No conflict risk.
   - **Yes** → Continue.
2. Can you achieve the change by constraining or reformatting the
   default skill's output without changing its procedure?
   - **Yes** → Write an augmentation skill with field ownership and
     hard limits.
   - **No** → Continue.
3. Is the change org-specific, or would it benefit all fullsend users?
   - **Org-specific** → Override via config-driven registration.
   - **Benefits everyone** → Contribute the improvement upstream
     (see [escalation ladder](../../docs/agents/topics/escalation-ladder.md)).

## Common mistakes

| Mistake | Fix |
|---------|-----|
| Soft language ("prefer concise") | Use hard limits ("40 words maximum") |
| No field ownership declaration | List exactly which fields the skill owns and which it does not |
| Redefining default skill procedures | Add constraints to the output, not alternative steps |
| Generic scope ("shorten all output") | Name specific fields and agents |
| Same directory name as a built-in | Rename — the built-in shadows it silently |
| Ignoring the default skill's existence | Acknowledge it by name and state the boundary |
| Overriding when augmenting would work | Augmentation preserves upstream improvements; override loses them |
