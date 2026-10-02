# Post-Flight Verification

Part of the [cutting-releases](SKILL.md) skill.

Run after the version tag is pushed and the CI workflows complete.
The release workflow automatically moves the `v0` floating tag after
GoReleaser succeeds (skipped for pre-release tags). Focus on the areas identified during pre-flight
step F.

## A. Wait for CI workflows

Wait for the Release workflow (triggered by the `v*` tag) and the
Sandbox Images workflow (triggered by the same tag push, not by the
release workflow) to complete. Sandbox Images runs against the tag, not
a branch, so `--limit=1` alone can show an unrelated run — scope it to
this tag with `--branch`:

```
gh run list --workflow=release.yml --limit=1
gh run list --workflow=sandbox-images.yml --branch <tag> --limit=1
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

The release workflow gates **publication itself** on functional test
validation. `resolve-agents` verifies the tag and checks the gate
secrets, `validate-agents` runs agents' functional tests against the
release tag, and only then does `release` run GoReleaser. `tag-agents`
pushes the version tag to agents last. Every failure path sends a Slack
notification.

Verify the four jobs succeeded in the release workflow run:

```
gh run view <run-id> --repo fullsend-ai/fullsend --json jobs \
  --jq '.jobs[] | select(.name | test("resolve-agents|validate-agents|release|tag-agents")) | {name, conclusion}'
```

If `resolve-agents` or `validate-agents` failed, the release was
blocked before publishing: no binaries, no GitHub Release, no moved
`v0` tag, and no agents tag. Step B above will show nothing to verify.
If the cause is a flake or transient infrastructure issue, resolve it
and use "Re-run failed jobs" on the existing run — the tag is already
correct and must not be moved (re-verification would fail the release
if it were). If the fix needs a code or pin change, the tag is now
blocked: delete it both locally and remotely (`git tag -d <tag> && git
push origin :refs/tags/<tag>`) — otherwise the next `git tag -a <tag>`
fails because the old tag still exists — then cut `rc.N+1` from the
fixed commit instead (SKILL.md step 6) rather than re-running.

If `release` itself failed with `422 already_exists`, the final tag
shared a commit with its RC and nothing was published either — but
re-running cannot fix it, since the commit itself is the problem:
delete the tag both locally and remotely (`git tag -d <tag> && git push
origin :refs/tags/<tag>`) — otherwise the next `git tag -a <tag>` fails
because the old tag still exists — and re-tag at a commit Y that is
different from the RC's commit and passes the SKILL.md step 9 check
(see the interim rule there, fullsend#7955). The blocked final's images
were already published and get overwritten when the tag is re-pushed;
that's harmless, since the fleet only pins RC digests.

If only `tag-agents` failed, the fullsend release shipped but agents
was not tagged; fix that job's cause and re-run it.

If all jobs succeeded, verify the tag and release exist on agents:

```
gh release view <tag> --repo fullsend-ai/agents
```

Verify the agents tag points at the repin PR's merge commit (step 8 of
SKILL.md) — not some other commit on `main`:

```
gh api repos/fullsend-ai/agents/git/ref/tags/<tag> --jq '.object.sha'
gh pr view <repin-pr-number> --repo fullsend-ai/agents --json mergeCommit --jq '.mergeCommit.oid'
```

The two SHAs should match. If other PRs landed on agents `main` between
the repin merge and the release, the tag SHA may legitimately differ —
confirm it is still a descendant of the merge commit rather than
predating it:

```
gh api repos/fullsend-ai/agents/compare/<merge-sha>...<tag-sha> --jq .status
```

`identical` or `ahead` is OK; anything else means the tag predates the
repin merge, so the shipped fleet config does not include the pinned
images and the repin did not take effect for this release.

For non-prerelease tags, verify the `v0` floating tag was moved on
**both** repos. For this repo, `git/ref/tags/vX.Y.Z` returns the tag
*object*, not the commit, because the final is an annotated tag — so
resolve the commit the final tag points at via the commits API instead
of the ref before comparing it to `v0`:

```
gh api repos/fullsend-ai/fullsend/commits/vX.Y.Z --jq .sha
gh api repos/fullsend-ai/fullsend/git/ref/tags/v0 --jq '.object.sha'
```

The two SHAs must match. For agents:

```
gh api repos/fullsend-ai/agents/git/ref/tags/v0 --jq '.object.sha'
gh api repos/fullsend-ai/agents/git/ref/tags/<tag> --jq '.object.sha'
```

The two SHAs must match — agents tags are lightweight, so `.object.sha`
is already the commit (unlike the fullsend final tag above, which is
annotated and needs the commits-API resolution).

If the agents release workflow failed, investigate before continuing —
downstream consumers may reference agents by tag.

## B3. Verify the harness image pins

The harness files in `fullsend-ai/agents` should be pinned to the
**RC's** digests (resolved and verified in SKILL.md step 8) — not the
final tag's own `:X.Y.Z` images. The image build is not reproducible
(see SKILL.md Notes), so a final build from the same commit produces
different digests than its RC; re-pinning to the final's digests would
silently undo the revision check done at RC time.

Discover the harness files at the released tag rather than assuming a
fixed list or reading a local checkout (either may have drifted from
what actually shipped):

```
for f in $(gh api "repos/fullsend-ai/agents/contents/harness?ref=vX.Y.Z" --jq '.[].name'); do
  gh api "repos/fullsend-ai/agents/contents/harness/$f?ref=vX.Y.Z" --jq .content \
    | base64 -d | grep -H --label="$f" -E 'image:.*fullsend-(sandbox|code)'
done || true
```

(A non-zero exit from the loop just means the last file's `grep` didn't
match — that's fine.)

Confirm each digest matches what was resolved via `skopeo inspect` for
the `X.Y.Z-rc.N` tag in SKILL.md step 8 — not a fresh `skopeo inspect` of
the final `X.Y.Z` tag, which will likely show a different digest for the
same content.

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

Also report the results of B2 (both `v0` tags moved, agents tag at the
repin merge commit) and B3 (harness pins match the RC digests) —
these are blockers, not optional checks, even though they fall outside
the `@v0`-consumer table above.

Distinguish between:
- **Release-related failures** — workflow resolution errors, missing
  secrets, or permission failures caused by the tag move.
- **Unrelated failures** — agent runtime errors, external API issues,
  or pre-existing test failures.
