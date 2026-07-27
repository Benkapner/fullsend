---
name: nextwork
description: >
  Build a readiness-oriented queue of open issues/PRs — assigned work plus
  their open GitHub blockers — and recommend the next action for each. Use
  for /nextwork or when the user asks what to work on next, what's blocking
  them, or wants to clear stale automation waits.
allowed-tools: Bash(python3 skills/nextwork/scripts/nextwork.py:*)
---

# Next Work

Deterministically build a queue of **open** issues/PRs (assigned to you, or
explicit refs), follow **open** GitHub `blockedBy` links and **open**
sub-issues breadth-first (`blockedBy` may be cross-repo; sub-issues are
same-repo), classify every item into a status catalog, and recommend the
next action. Unlike [`/topissues`](../topissues/SKILL.md), this has no
RICE/project dependency — it is readiness-oriented, not priority-scored.

## Prerequisites

- `python3`
- `gh` CLI authenticated with read access to the target repo(s); write access
  is needed for `--apply`, `--take-over`, and `--link-blocker`

## Script

From the repository root:

```bash
python3 skills/nextwork/scripts/nextwork.py [ITEMS...] [OPTIONS]
```

## Flags

| Flag | Description |
|------|-------------|
| positional `ITEMS...` | Seed as `owner/repo#N`, `#N`, `N` (needs `--repo`), or a GitHub issue/PR URL. Omit to seed from open issues/PRs assigned to `--user` in `--repo`. |
| `--repo owner/name` | Repository override (default: current repo via `gh repo view`); also the default repo for bare `#N`/`N` refs |
| `--user LOGIN` | GitHub login (default: authenticated user) |
| `--format markdown\|json` | Output format (default: markdown) |
| `--show-blocked` | Include Waiting/Blocked/Assigned-elsewhere sections in markdown output (JSON always includes every item) |
| `--apply` | Perform trivial actions: `assign:self` first when suggested on actionable unassigned items; post exact `/fs-triage`, `/fs-code`, `/fs-review`, `/fs-fix` comments; remove orphaned `blocked` labels (`remove-label:blocked`). Never steals assignment from others and never auto-merges. |
| `--take-over REFS` | Assign the listed refs (comma-separated or repeatable) to `--user`, even if already assigned elsewhere, then classify them as owned by the user. Skill-mediated — ask the user before using this. |
| `--link-blocker DEPENDENT=BLOCKER` | Repeatable. Persist a real GitHub `blockedBy` dependency (DEPENDENT is blocked by BLOCKER, both as `owner/repo#N`). Idempotent if the link already exists. **The dependent must be an open Issue** — GitHub's blocked-by relationship is issue-only, so a PR cannot be the dependent side. |
| `--decisions-only` | Filter output to non-trivial decisions only (statuses in the "Decision?" = No/Decision column below) |
| `--stale-hours N` | Default 6. Hours after which a **stuck in-flight** agent-status start, or a **never-started** launch label/`/fs-*` command, becomes an actionable re-trigger |
| `--quiet` | Suppress stderr on API failures |
| `--include-text` | Include truncated body + last comments in JSON output, for the skill's prose-dependency mining pass |

## Slash command

Portable `/nextwork` is defined in [commands/nextwork.md](../../commands/nextwork.md).

## Status catalog

Every item gets exactly one `status`. Eliminated statuses (`eliminated: true`)
are not shown in the default markdown output (add `--show-blocked` to see
them); actionable statuses always appear under "Do now".

**Eliminated — waiting on automation** (launch label or `/fs-*`, or non-terminal
agent-status start). `--stale-hours` flips these to the Stale → column when the
**start comment** or **launch signal** is that old. Slash commands are parsed
like production dispatch: first whitespace token of the first comment line.

| Status | Meaning | Stale → |
|--------|---------|---------|
| `waiting_triage` | `ready-for-triage` / `/fs-triage` with no matching completed Triage yet; **or** non-terminal triage agent-status; **or** no control labels / launch signal yet (never auto-flips from `created_at` alone). A terminal Triage **or** sticky `<!-- fullsend:triage-agent -->` (when status is absent) at/after the launch signal clears the wait. | `needs_triage` (`/fs-triage`) — only when a launch signal or stuck start is stale |
| `waiting_code` | `ready-to-code` / `/fs-code`; **or** non-terminal code agent-status | `trigger_code` (`/fs-code`) |
| `waiting_review` | `ready-for-review` / `/fs-review` / review-required path; **or** non-terminal review agent-status | `trigger_review` (`/fs-review`) — also when head commits are newer than the last terminal Review |
| `waiting_fix` | Unresolved review threads all from `fullsend-ai-review[bot]`; **or** non-terminal fix agent-status | `trigger_fix` (`/fs-fix`) |
| `waiting_agent` | Non-terminal agent-status comment whose role could not be mapped | _(no re-trigger)_ |
| `waiting_ci` | Required checks still running | _(no re-trigger)_ |
| `waiting_merge_queue` | PR is already enqueued in the merge queue | _(no re-trigger)_ |

**Eliminated — blocked / deferred / owned elsewhere:**

| Status | Meaning |
|--------|---------|
| `blocked_by` | Open GitHub `blockedBy` link(s) only. `blockers[]` lists those open refs (issues only — GitHub has no PR-side `blockedBy`). The `blocked` label alone does **not** yield this status. |
| `waiting_sub_issues` | Issue has one or more open GitHub sub-issues. `open_sub_issues[]` lists them; BFS enqueues each open child (same repo) for classification. Prefer this over promoting an epic while children are unfinished. |
| `waiting_linked_pr` | Issue has an open linked PR (native closing keywords + `partial-fix #N`) — go look at that PR instead |
| `waiting_info_other` | `needs-info` label and you're not the author (waiting on the reporter) |
| `assigned_elsewhere` | Assignees present and you're not among them. `assignees[]` is included so the skill can offer take-over. Never suggested as something to self-assign — that's `--take-over` only. |
| _(dropped, never shown)_ | Closed/merged, or labeled `duplicate` |

**Actionable:**

| Status | Next action | Trivial? |
|--------|-------------|----------|
| `needs_assign` | Unassigned with no other automation/decision signal → assign yourself | Yes |
| `needs_triage` | Stale triage launch/start, **or** completed triage (terminal agent-status **or** sticky `<!-- fullsend:triage-agent -->` when status is absent) older than 3 days / followed by non-exempt comments (does **not** override a non-stale `waiting_code`) → `/fs-triage` | Yes |
| `promote_code` | `triaged` (feature work) → decide whether to promote | Decision |
| `close_or_plan` | Has sub-issues and all are closed → close the parent, or plan further work / open new sub-issues | Decision |
| `trigger_code` | Stale `ready-to-code` / `/fs-code` / stuck Code start → `/fs-code` | Yes |
| `trigger_review` | Stale review launch/start, or newer commits since last Review → `/fs-review` | Yes |
| `trigger_fix` | Unresolved threads all from the review bot and launch/start is stale (or ready to run) → `/fs-fix` | Yes |
| `needs_info_self` | `needs-info` and you're the author → provide info | Decision |
| `needs_review_decision` | Manual-review labels, human unresolved threads, failed CI (`FAILURE`/`ERROR`), or `mergeStateStatus=BLOCKED` under `ready-for-merge` | Decision |
| `ready_to_merge` | `ready-for-merge` **and** `mergeStateStatus` is `CLEAN`/`UNSTABLE`, no unresolved threads, checks settled, review not still required, not yet enqueued | Decision (never auto-merged) |
| `fix_conflicts` | `mergeStateStatus` is `DIRTY` **or** `mergeable` is `CONFLICTING` | Decision |
| `human_work` | Assigned/authored, no clear automation signal — implement, un-draft, or investigate | Decision |

**Side-action (orthogonal to primary status):**

| Suggestion | When | Trivial? |
|------------|------|----------|
| `assign:self` | Actionable (`eliminated: false`) and unassigned — prepended ahead of other suggestions | Yes (`--apply` assigns **first**, before `/fs-*` comments or label removal) |
| `remove-label:blocked` | Item has the `blocked` label but no open structured blockers | Yes (`--apply` removes it) |

`--apply` performs the "Yes" (trivial) status rows **and** side-actions: `assign:self` on actionable unassigned items (including decision statuses), then primary `/fs-*` comments, then any `remove-label:blocked` (including on eliminated / decision items). It never steals assignees from others. `--decisions-only` shows only the "Decision" status rows.

## Skill loop

1. Run `python3 skills/nextwork/scripts/nextwork.py --format json --include-text $ARGUMENTS`.
2. Read `body`/`comments` for prose-only dependencies the script missed —
   especially items whose text clearly depends on another open issue/PR
   (including those still carrying an orphaned `blocked` label).
3. **Persist confident prose blockers as real data** so future runs don't
   need the LLM: for each `item A blocked by item B` you're confident about,
   run
   `python3 skills/nextwork/scripts/nextwork.py --format json --link-blocker A=B ...`.
   If uncertain, ask the user first. `--link-blocker` requires the dependent
   to be an open Issue; if it's a PR, tell the user GitHub doesn't support
   that relationship and suggest linking the underlying issue instead. Cap
   this persist-and-reclassify loop at ~3 iterations. Do this **before**
   `--apply` so a prose-only blocker is linked instead of stripping the
   orphaned `blocked` label first.
4. For any `assigned_elsewhere` item that matters to the user's goal (a
   blocker on their work, or something they explicitly referenced), **offer
   take-over**. On explicit confirmation, run
   `python3 skills/nextwork/scripts/nextwork.py --format json --take-over owner/repo#N ...`
   and continue classifying the refreshed output — the item is now owned and
   goes through the full status catalog like anything else.
5. Present the result:
   - Default: actionable items. Add blocked/waiting/assigned-elsewhere detail
     only if the user asked, or pass `--show-blocked`.
   - Remaining `assign:self` and `remove-label:blocked` suggestions (after
     step 3) are trivial side-actions — include them when offering apply.
   - "Decisions only": re-run with `--apply --decisions-only` — trivial
     actions (including `assign:self` and orphaned `blocked` label removal)
     get applied and only decision items remain to show. Still ask before
     `--take-over`; still persist confident prose blockers first.
6. Offer to apply remaining trivial actions (re-run with `--apply`) unless
   already applied in step 5.
7. Don't invent statuses the script didn't emit. The skill's job is finding
   prose dependencies, persisting them, offering take-over, and clarifying
   the human-facing summary — not re-deriving readiness itself.

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | Missing `gh` or not in a resolvable repository |
| 2 | Invalid arguments (bad `--repo`, unparseable ref, malformed `--link-blocker` spec) |
| 3 | GraphQL/API failure |

## Limitations

- In-flight agent detection uses HTML markers from status comments
  (`<!-- fullsend:agent-status:<runID> -->` without
  `<!-- fullsend:status:terminal -->`), not `gh run list` / GHA polling. The
  chronologically latest agent-status comment wins. A non-terminal start
  younger than `--stale-hours` stays `waiting_*` (no `/fs-*` suggestion); once
  that start is older than `--stale-hours`, nextwork suggests the matching
  re-trigger. This is checked **before** trusting `ready-for-merge`.
- Merge readiness does **not** trust the `ready-for-merge` label alone. The
  script also requires `mergeable` / `mergeStateStatus` (requesting `mergeable`
  so GitHub computes conflict state) and zero unresolved `reviewThreads`.
  Conflicts (`DIRTY` / `CONFLICTING`) win over review triggers; failed CI
  (`FAILURE`/`ERROR`), human unresolved conversations, or `BLOCKED` yield
  `needs_review_decision` instead of `ready_to_merge`.
- GitHub's `blockedBy` dependency feature is **issue-only**. The `blocked`
  label alone does not classify as `blocked_by`; when present without open
  structured blockers it yields `remove-label:blocked` (trivial / `--apply`).
  `--link-blocker` cannot make a PR the dependent side of a structured
  link — only an issue.
- `waiting_ci` and `waiting_merge_queue` are not flipped by `--stale-hours`.
- Merge-queue membership is only checked for PRs labeled `ready-for-merge`
  (to avoid an extra API call per PR); other PRs never report
  `in_merge_queue`.
