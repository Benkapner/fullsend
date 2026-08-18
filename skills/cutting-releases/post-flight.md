# Post-Flight Verification

Part of the [cutting-releases](SKILL.md) skill.

Run after the version tag is pushed and the CI workflows complete.
The release workflow automatically moves the `v0` floating tag after
GoReleaser succeeds (skipped for pre-release tags). Focus on the areas identified during pre-flight
step F.

## A. Wait for CI workflows

Wait for the Release workflow (triggered by the `v*` tag) and the
Sandbox Images workflow (triggered by release workflow) to complete:

```
gh run list --workflow=release.yml --limit=1
gh run list --workflow=sandbox-images.yml --limit=1
```

Both must pass before proceeding. If either fails, investigate and
resolve before continuing — a broken release or sandbox image affects
all downstream consumers.

## B. Verify the release artifacts

```
gh release view <tag>
```

Check that the title, changelog, and binary assets look correct.
Verify the release is not marked as a draft.

## B2. Verify agents validation and tag

The release workflow gates the agents tag on functional test
validation. After GoReleaser completes, `validate-agents` runs
agents' functional tests against the release tag. Only if those
tests pass does `tag-agents` push the tag to agents. If either
job fails, a Slack notification is sent automatically.

First, verify the `validate-agents` and `tag-agents` jobs succeeded
in the release workflow run:

```
gh run view <run-id> --repo fullsend-ai/fullsend --json jobs \
  --jq '.jobs[] | select(.name | test("validate-agents|tag-agents")) | {name, conclusion}'
```

If `validate-agents` failed, the agents functional tests did not
pass against the new binary — investigate before proceeding. The
fullsend release shipped, but agents was not tagged. A Slack
notification should have been sent.

If both jobs succeeded, verify the tag and release exist on agents:

```
gh release view <tag> --repo fullsend-ai/agents
```

For non-prerelease tags, verify the `v0` floating tag was moved:

```
gh api repos/fullsend-ai/agents/git/ref/tags/v0 --jq '.object.sha'
```

If the agents release workflow failed, investigate before continuing —
downstream consumers may reference agents by tag.

## C. Skip fullsend-ai repos

The `fullsend-ai/.fullsend` repo references reusable workflows via
`@main`, not `@v0`. Its runs do **not** exercise the `v0` tag and
cannot confirm that the tag move worked. (Those runs are checked
during pre-flight instead, as a signal that `main` is healthy.)

Skip fullsend-ai for post-flight `v0` verification. Focus on other
downstream consumers in step D.

## D. Check additional downstream repos (optional)

Use `AskUserQuestion` to ask if the user has access to additional
downstream orgs:

> Do you have access to any other downstream orgs/repos to verify?
> (e.g. "konflux-ci, redhat-developer/rhdh-agentic")
> Leave blank to skip.

For each repo provided, check recent workflow runs that started
**after** the `v0` tag move:

```
gh run list --repo <org/repo> --limit=5
```

Confirm they completed without workflow-resolution errors (e.g.
"could not find reusable workflow"). If no runs occurred naturally,
check for recent failed runs that can be retriggered:

```
gh run list --repo <org/repo> --status=failure --limit=3
```

Present any candidate to the user for confirmation before retriggering:

> I found run `<run-id>` (failed) in `<org/repo>`.
> Retrigger it to verify `@v0` resolves?

Once confirmed:

```
gh run rerun <run-id> --failed --repo <org/repo>
```

If blank, skip this step — not all admins have access to every
enrolled org.

## E. Present post-flight summary

Summarize results to the user:

| Org/Repo | `@v0` Refs | Status |
|----------|-----------|--------|
| ... | ... | ... |

Note: `fullsend-ai` repos are excluded from this table — they use
`@main` and were checked during pre-flight.

Distinguish between:
- **Release-related failures** — workflow resolution errors, missing
  secrets, or permission failures caused by the tag move.
- **Unrelated failures** — agent runtime errors, external API issues,
  or pre-existing test failures.
