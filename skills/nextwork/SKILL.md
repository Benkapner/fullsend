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
explicit refs), follow **open** GitHub `blockedBy` links breadth-first
(cross-repo), classify every item into a status catalog, and recommend the
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
| `--apply` | Perform trivial actions: assign unassigned items to self; post exact `/fs-triage`, `/fs-code`, `/fs-review`, `/fs-fix` comments. Never steals assignment from others and never auto-merges. |
| `--take-over REFS` | Assign the listed refs (comma-separated or repeatable) to `--user`, even if already assigned elsewhere, then classify them as owned by the user. Skill-mediated — ask the user before using this. |
| `--link-blocker DEPENDENT=BLOCKER` | Repeatable. Persist a real GitHub `blockedBy` dependency (DEPENDENT is blocked by BLOCKER, both as `owner/repo#N`). Idempotent if the link already exists. **The dependent must be an open Issue** — GitHub's blocked-by relationship is issue-only, so a PR cannot be the dependent side. |
| `--decisions-only` | Filter output to non-trivial decisions only (statuses in the "Decision?" = No/Decision column below) |
| `--stale-hours N` | Default 6. Past this, a waiting-on-automation item flips to actionable with a re-trigger action |
| `--quiet` | Suppress stderr on API failures |
| `--include-text` | Include truncated body + last comments in JSON output, for the skill's prose-dependency mining pass |

## Slash command

Portable `/nextwork` is defined in [commands/nextwork.md](../../commands/nextwork.md).

## Status catalog

Every item gets exactly one `status`. Eliminated statuses (`eliminated: true`)
are not shown in the default markdown output (add `--show-blocked` to see
them); actionable statuses always appear under "Do now".

**Eliminated — waiting on automation** (flips to actionable if the wait is
older than `--stale-hours`, **except** in-flight agent-status waits, CI, and
merge queue):

| Status | Meaning | Stale → |
|--------|---------|---------|
| `waiting_triage` | `ready-for-triage` label, or no control labels yet (issue is new); **or** non-terminal triage agent-status comment | `needs_triage` (`/fs-triage`) — not when driven by an in-flight status comment |
| `waiting_code` | `ready-to-code`, no open linked PR; **or** non-terminal code agent-status comment | `trigger_code` (`/fs-code`) — not when driven by an in-flight status comment |
| `waiting_review` | Open non-draft PR, no outcome label yet; **or** non-terminal review agent-status comment | `trigger_review` (`/fs-review`) — not when driven by an in-flight status comment |
| `waiting_fix` | PR has `CHANGES_REQUESTED` and is fix-eligible; **or** non-terminal fix agent-status comment | `trigger_fix` (`/fs-fix`) — not when driven by an in-flight status comment |
| `waiting_agent` | Non-terminal agent-status comment whose role could not be mapped | _(no re-trigger)_ |
| `waiting_ci` | Required checks still running (also wins over a stale `ready-for-merge` label) | _(no re-trigger; not stale-overridden)_ |
| `waiting_merge_queue` | PR is already enqueued in the merge queue | _(no re-trigger)_ |

**Eliminated — blocked / deferred / owned elsewhere:**

| Status | Meaning |
|--------|---------|
| `blocked_by` | Open GitHub `blockedBy` link(s), or the `blocked` label. `blockers[]` lists open refs when known from structured data (issues only — GitHub has no PR-side `blockedBy`). |
| `waiting_linked_pr` | Issue has an open linked PR (native closing keywords + `partial-fix #N`) — go look at that PR instead |
| `waiting_info_other` | `needs-info` label and you're not the author (waiting on the reporter) |
| `assigned_elsewhere` | Assignees present and you're not among them. `assignees[]` is included so the skill can offer take-over. Never suggested as something to self-assign — that's `--take-over` only. |
| _(dropped, never shown)_ | Closed/merged, or labeled `duplicate` |

**Actionable:**

| Status | Next action | Trivial? |
|--------|-------------|----------|
| `needs_assign` | Unassigned → assign yourself | Yes |
| `needs_triage` | Stale triage wait → `/fs-triage` | Yes |
| `promote_code` | `triaged` (feature work) → decide whether to promote | Decision |
| `trigger_code` | Stale `ready-to-code` wait → `/fs-code` | Yes |
| `trigger_review` | Stale review wait → `/fs-review` | Yes |
| `trigger_fix` | Stale fix wait → `/fs-fix` | Yes |
| `needs_info_self` | `needs-info` and you're the author → provide info | Decision |
| `needs_review_decision` | `requires-manual-review` or `needs-human` | Decision |
| `ready_to_merge` | `ready-for-merge`, checks settled, review not still required, not yet enqueued → merge or enqueue | Decision (never auto-merged) |
| `fix_conflicts` | `mergeStateStatus` is `DIRTY` | Decision |
| `human_work` | Assigned/authored, no clear automation signal — implement, un-draft, or investigate | Decision |

`--apply` only performs the "Yes" (trivial) rows. `--decisions-only` shows
only the "Decision" rows.

## Skill loop

1. Run `python3 skills/nextwork/scripts/nextwork.py --format json --include-text $ARGUMENTS`.
2. Read `body`/`comments` for prose-only dependencies the script missed —
   especially `blocked_by` items with an empty `blockers[]`, or any item
   whose text clearly depends on another open issue/PR.
3. **Persist confident prose blockers as real data** so future runs don't
   need the LLM: for each `item A blocked by item B` you're confident about,
   run
   `python3 skills/nextwork/scripts/nextwork.py --format json --link-blocker A=B ...`.
   If uncertain, ask the user first. `--link-blocker` requires the dependent
   to be an open Issue; if it's a PR, tell the user GitHub doesn't support
   that relationship and suggest linking the underlying issue instead. Cap
   this persist-and-reclassify loop at ~3 iterations.
4. For any `assigned_elsewhere` item that matters to the user's goal (a
   blocker on their work, or something they explicitly referenced), **offer
   take-over**. On explicit confirmation, run
   `python3 skills/nextwork/scripts/nextwork.py --format json --take-over owner/repo#N ...`
   and continue classifying the refreshed output — the item is now owned and
   goes through the full status catalog like anything else.
5. Present the result:
   - Default: actionable items. Add blocked/waiting/assigned-elsewhere detail
     only if the user asked, or pass `--show-blocked`.
   - "Decisions only": re-run with `--apply --decisions-only` — trivial
     actions get applied and only decision items remain to show. Still ask
     before `--take-over`; still persist confident prose blockers first.
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
  chronologically latest agent-status comment wins. This is checked **before**
  trusting `ready-for-merge`, so a stale merge label during a re-review does
  not surface as `ready_to_merge`.
- GitHub's `blockedBy` dependency feature is **issue-only**. A PR can carry
  the `blocked` label (surfaced as `blocked_by` with an empty `blockers[]`),
  but `--link-blocker` cannot make a PR the dependent side of a structured
  link — only an issue.
- `waiting_ci`, `waiting_merge_queue`, and in-flight agent-status waits are
  not flipped to actionable by `--stale-hours`; there's no `/fs-*` command
  that sensibly resolves a stuck CI run, merge-queue entry, or live agent
  run, so those stay eliminated until they resolve on their own or a human
  intervenes.
- Merge-queue membership is only checked for PRs labeled `ready-for-merge`
  (to avoid an extra API call per PR); other PRs never report
  `in_merge_queue`.
