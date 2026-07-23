---
description: Show your next actionable work, following open blockers and stale automation waits
argument-hint: "[ITEMS...] [--repo owner/name] [--user LOGIN] [--show-blocked] [--apply] [--decisions-only]"
allowed-tools: Bash(python3 skills/nextwork/scripts/nextwork.py:*)
---

Follow skill **nextwork**.

From the repository root, run:

    python3 skills/nextwork/scripts/nextwork.py --format json --include-text $ARGUMENTS

Then follow the skill loop in [skills/nextwork/SKILL.md](../skills/nextwork/SKILL.md):
mine prose-only dependencies from `body`/`comments`, persist confident ones
with `--link-blocker`, offer take-over for `assigned_elsewhere` items that
matter to the user's goal, then present the result. Default to actionable
items only; include blocked/waiting/assigned-elsewhere detail if the user
asked for it or passed `--show-blocked`. Don't invent statuses the script
didn't emit.
