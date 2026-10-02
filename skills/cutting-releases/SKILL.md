---
name: cutting-releases
description: >
  Use when the user wants to tag a release, cut a release candidate, or ship a
  new version. Also use when asking about release process, versioning, or how
  GoReleaser is configured.
allowed-tools: Read, Grep, Glob, AskUserQuestion, Agent, Bash(git tag:*), Bash(git log:*), Bash(git diff:*), Bash(git pull:*), Bash(git push:*), Bash(git rev-parse:*), Bash(gh release:*), Bash(gh run:*), Bash(gh api:*), Bash(gh pr:*), Bash(git checkout:*), Bash(git fetch:*), Bash(skopeo inspect:*), Bash(grep:*), Bash(bash skills/cutting-releases/scripts/install-binary.sh:*)
---

# Cutting Releases

Releases are driven by annotated git tags. When a tag matching `v*` is pushed,
`.github/workflows/release.yml` runs GoReleaser to build binaries, generate a
changelog, and create the GitHub release.

## Process

Before starting step 1, read
[pre-flight.md](pre-flight.md) in this skill's directory and complete
the pre-flight audit. Do not proceed until the user confirms GO.

Follow these steps in order.

### 1. Confirm the branch

Releases should be cut from `main`. Verify you are on `main` and up to date:

```
git checkout main && git pull --tags --force
```

### 2. Determine the version

Check the latest **final** tag — RC/alpha/beta tags sort above their
final, so exclude anything with a `-` suffix:

```
git tag --sort=-v:refname | grep -v -- - | head -5
```

Decide the next **target final version** following semver. Every release is
cut as a release candidate first (step 6) and promoted to this final version
only after the RC gate passes and the fleet images are repinned (step 8) —
so what you pick here is the version the release will end up as, not the
first tag you push:

| Change type | Example target version |
|---|---|
| Breaking / major milestone | `v1.0.0` |
| New functionality (MVP, feature set) | `v0.X.0` |
| Bug fixes only | `v0.0.X` |

### 3. Confirm the version with the user

Use `AskUserQuestion` to present your proposed version tag and the rationale
for your choice. For example:

> I'd suggest `v0.2.0` (cut first as `v0.2.0-rc.1`) — there are 5 new `feat:`
> commits since `v0.1.0` and no breaking changes. Does that look right, or
> would you prefer a different version?

Do not proceed until the user confirms.

### 4. Ask for a tag subject

Use `AskUserQuestion` to ask:

> Any special title for this release? (e.g. "MVP Release Candidate 1")
> Leave blank to use just the version tag.

The answer becomes the tag subject line. If blank, use the tag name itself as
the subject so that GoReleaser's `name_template` guard (`ne .TagSubject .Tag`)
suppresses it, producing a clean release title without duplication.

### 5. Gather changes since the last final

```
git log --oneline <previous-final>..HEAD
```

Summarize changes into categories (features, fixes, refactors). Exclude
`docs:`, `test:`, `chore:`, `ci:`, `build:` commits — GoReleaser filters these anyway.

### 6. Create the RC tag

Every release starts as a release candidate — even one you expect to ship
with no changes after the gate run. Record the current commit — call it
commit X:

```
git rev-parse HEAD
```

Build the tag message:

- **Line 1 (subject):** The custom title from step 4, if one was given.
  If no custom title, **use the tag name itself** (e.g. `v0.9.0-rc.1`) —
  git's `%(contents:subject)` skips leading blank lines, so a blank first
  line still picks up the first category header as `.TagSubject`. Using the
  tag name as subject ensures `.TagSubject == .Tag`, which the goreleaser
  guard suppresses, producing a clean release title with no suffix.
- **Line 2:** Blank.
- **Lines 3+:** Summary of highlights organized by category.

Name commit X explicitly and verify the tag landed on it before pushing:

```
git tag -a v0.X.0-rc.1 <X> -m "<message>"
git rev-parse v0.X.0-rc.1^{commit}
```

The second command's output must equal commit X.

**Until fullsend#7955 merges**, the final tag in step 9 must point at a
different, later commit Y — not commit X — see the interim rule there.

The first line of the annotation becomes the release title suffix via
GoReleaser's `name_template` (see `.goreleaser.yml`).

### 7. Push the RC tag

```
git push origin v0.X.0-rc.N
```

The Release workflow takes over from here. Verify it starts:

```
gh run list --workflow=release.yml --limit=1
```

Expect the run to take a while before any artifact appears: agents'
functional tests gate the publish step, so GoReleaser does not start
until they pass (see the notes at the end of this file). Once the gate
passes, GoReleaser publishes the `:X.Y.Z-rc.N` **binaries** (it has no
`dockers:` section in `.goreleaser.yml`) and marks the GitHub release as
a prerelease; `v0` does not move for a prerelease tag, but `tag-agents`
still runs and tags `fullsend-ai/agents` at this same prerelease version
(see Notes). The `:X.Y.Z-rc.N` **images** are built separately, by the
Sandbox Images workflow triggered by the same tag push — see step 8.1.

**If the gate fails:** fix forward on `main`, increment N, and cut
`rc.N+1` from the new commit (back to step 6). The failed RC's images stay
published in the registry but unused — never delete a published tag (see
Notes).

### 8. Repin the fleet harness images

Once the RC gate is green, repin the fleet harness images in
`fullsend-ai/agents` to this RC's digests before tagging the final. The
final tag in step 9 must wait until this repin PR is merged.

1. **Resolve the RC digests.** First confirm the Sandbox Images workflow
   run for this tag succeeded — GoReleaser does not build these images
   (see step 7):

   ```
   gh run list --workflow=sandbox-images.yml --branch vX.Y.Z-rc.N --limit 1
   ```

   The `sandbox-images.yml` workflow publishes images tagged with the
   bare semver (no `v` prefix). On macOS, `skopeo inspect` fails with
   "no image found in image index for ... darwin" unless you pass
   `--override-os linux`:

   ```
   skopeo inspect --override-os linux --no-tags docker://ghcr.io/fullsend-ai/fullsend-sandbox:X.Y.Z-rc.N | jq -r '.Digest, .Labels["org.opencontainers.image.revision"]'
   skopeo inspect --override-os linux --no-tags docker://ghcr.io/fullsend-ai/fullsend-code:X.Y.Z-rc.N | jq -r '.Digest, .Labels["org.opencontainers.image.revision"]'
   ```

   `.Digest` is the multi-arch index digest to pin. Check the revision
   label equals commit X. If it doesn't, a revision ≠ X means the tag
   was rebuilt from another commit — do not proceed; investigate which
   commit actually built it.
2. **Open a repin PR against `fullsend-ai/agents`.** Don't assume a fixed
   file list — discover the current harness files and repin whichever
   ones reference `fullsend-sandbox` or `fullsend-code`. Read agents
   `main`, which is what the repin PR edits and what the final will tag
   (not a local fullsend checkout, which may have drifted from what the
   harness actually runs):

   ```
   HARNESS_FILES=$(gh api "repos/fullsend-ai/agents/contents/harness?ref=main" --jq '.[].name') || exit 1
   [ -n "$HARNESS_FILES" ] || exit 1

   MATCH_COUNT=0
   for f in $HARNESS_FILES; do
     CONTENT=$(gh api "repos/fullsend-ai/agents/contents/harness/$f?ref=main" --jq .content) || exit 1
     [ -n "$CONTENT" ] || exit 1
     DECODED=$(printf '%s' "$CONTENT" | base64 -d) || exit 1
     MATCHES=$(printf '%s\n' "$DECODED" | grep -E 'image:.*fullsend-(sandbox|code)')
     GREP_STATUS=$?
     [ "$GREP_STATUS" -le 1 ] || exit 1
     if [ -n "$MATCHES" ]; then
       printf '%s:\n%s\n' "$f" "$MATCHES"
       MATCH_COUNT=$((MATCH_COUNT + 1))
     fi
   done
   [ "$MATCH_COUNT" -gt 0 ] || exit 1
   ```

   Check the directory listing, each content fetch, and the base64
   decode independently — an API error, an empty response, or a bad
   decode is a discovery failure, not "no harness files," and must stop
   here rather than silently repinning nothing. Tolerate only `grep`
   exit status 1 (no match in that file); any other status is also an
   error. Require at least one matching harness file overall before
   continuing — zero matches means discovery is broken, not that no
   harness references these images.

   Replace the `image:` line in each matching file with the resolved
   digest. Pin the multi-arch index digest only (`@sha256:...`) — never
   `:latest` or a per-platform digest. Use
   [fullsend-ai/agents#1570](https://github.com/fullsend-ai/agents/pull/1570)
   as the template for the PR diff and description.
3. **Hold window.** Between pushing the RC tag and pushing the final tag,
   do not merge PRs in this repo touching `images/sandbox`, `images/code`,
   or `.github/workflows/sandbox-images.yml`. Any such merge changes what
   the *next* tag's build produces — the step 9 `X..Y` check would then
   print commits for those paths, forcing `rc.N+1` instead of shipping
   the final from the already-repinned RC.
4. **Wait for the repin PR to merge** before proceeding to step 9.

### 9. Tag and push the final release

> **Interim rule — remove once fullsend#7955 merges.** GoReleaser
> resolves the tag to build via `git tag --points-at HEAD`; if the final
> tag shares a commit with its RC, it finds both tags there, picks the
> RC's, and fails publishing with `422 already_exists` (this is what
> happened on `v0.44.0` — see Notes). Until #7955 lands, the final tag
> MUST point at a commit Y that is different from the RC's commit X. If
> no newer commit exists on `main` by the time the repin PR merges (the
> repin merges in `fullsend-ai/agents`, which does not move fullsend
> `main`), merge a docs-only commit first to create a Y, then continue
> below.

**Changelog caveat — also remove once fullsend#7955 merges.** Because the
final tag (commit Y) and its RC (commit X) are now different commits,
GoReleaser's auto-generated changelog for the final compares against the
RC tag immediately before it, so it covers only `rc.N..vX.Y.Z` — not the
full set of changes since the last final. When writing highlights in
step 11, gather commits from `<previous-final>..vX.Y.Z` instead of
relying on the release body for the full picture.

Let Y be the commit the final tag will point at (distinct from commit X)
and run:

```
git log --oneline X..Y -- images/sandbox images/code .github/workflows/sandbox-images.yml
```

- **Prints nothing:** the fleet images haven't changed since the RC was
  built, so the final tag can safely point at Y.
- **Prints commits:** a change since the RC could produce different
  images than the ones just repinned. Do not tag the final yet — cut
  `rc.N+1` at Y instead (back to step 6) and repin again.

Once clear, name commit Y explicitly and verify the tag landed on it
before pushing, reusing the subject and highlights from step 6 with the
`-rc.N` suffix dropped:

```
git tag -a vX.Y.Z <Y> -m "<message>"
git rev-parse vX.Y.Z^{commit}
git push origin vX.Y.Z
```

The second command's output must equal commit Y.

Verify it starts the same way as step 7. This run moves `v0` and tags
`fullsend-ai/agents` at the non-prerelease version once GoReleaser
succeeds (see Notes). Because the RC gate ran against agents `main`
*before* the repin PR merged, this is the **first** gate run against the
repinned images. For a flaked gate, re-run instead of re-tagging (see
Notes). For a real gate failure, the final tag is now blocked: delete it
both locally and remotely before cutting `rc.N+1` at a new commit (back
to step 6) — otherwise the next `git tag -a vX.Y.Z` here fails because
the old tag still exists. For the `422 already_exists` failure, first
confirm it is the RC-tag-selection case — not a rerun hitting an
already-published final — before deleting anything (see Notes).

### 10. Run post-flight verification

Read [post-flight.md](post-flight.md) in this skill's directory and
follow the post-flight verification procedure.

### 11. Write release highlights

After post-flight confirms the release is published, write a short user-facing
summary highlighting the changes that matter most to end users.

1. **Gather the raw changelog.** **Until fullsend#7955 merges**, the
   release body only covers `rc.N..vX.Y.Z` (see the changelog caveat in
   step 9), not the full release — don't rely on it. Gather commits with
   `git log --oneline <previous-final>..vX.Y.Z` instead. Once #7955
   lands, `gh release view <tag> --json body -q .body` is sufficient on
   its own.
2. **Research the actual changes.** Do not rely on PR titles or one-line
   summaries — they often undersell or misrepresent user impact. Launch an
   `Agent` sub-agent to read the full body, diff, and comments of every merged
   PR in the release (`gh pr view <number>`, `gh pr diff <number>`). The agent
   should identify which changes affect user-visible behavior, CLI flags,
   configuration, error messages, performance, or compatibility — and flag
   anything that looks like a breaking change or notable upgrade, even if the
   PR title doesn't say so.
3. **Draft highlights.** Write one or more paragraphs of prose (not
   bullet lists — use only one paragraph if only one is necessary)
   focusing on *what changed for the user*, not internal refactors.
   Bold the names of features or areas being discussed (e.g. **token mint**,
   **`fullsend init`**). Use code fences where showing a command or config
   snippet helps illustrate a change. Skip items that have no user-visible
   effect. Full coverage of every change is a non-goal — clarity and impact
   are the goals.
4. **Present the draft to the user.** Use `AskUserQuestion` to show the
   proposed highlights text and ask:

   > Here are the draft release highlights I'd prepend to the release body.
   > Edit freely or say "looks good" to proceed.

5. **Prepend to the release.** Once confirmed (with any edits applied), fetch
   the current release body, prepend the highlights separated by a horizontal
   rule (`---`), and update the release:

   ```
   gh release edit <tag> --notes "$(cat <<'EOF'
   <highlights>

   ---

   <existing body>
   EOF
   )"
   ```

### 12. Install the binary locally

Use `AskUserQuestion` to ask where to install (default: `~/.local/bin/`),
then run the install script using its repo-root-relative path:

```bash
bash skills/cutting-releases/scripts/install-binary.sh <tag> [install-dir]
```

The script downloads the archive, verifies its SHA-256 checksum, and
installs the binary as `fullsend-<tag>` so multiple versions can coexist.

## Notes

- **Pre-releases:** Tags with `-rc.N`, `-alpha.N`, or `-beta.N` suffixes are
  automatically marked as pre-releases by GoReleaser.
- **Never delete a tag that published artifacts.** If a shipped release
  turns out bad, cut a new patch or RC instead of deleting it. The
  exception is a final tag confirmed blocked before publishing — either
  the agents validation gate failed, or the `release` job itself failed
  with `422 already_exists` and the confirmation steps below ruled out a
  rerun of an already-published final (see below) — only then is it
  safe to delete and re-cut.
- **The changelog** is auto-generated from PR titles (which must follow conventional commit format). GoReleaser uses `changelog.use: github` in `.goreleaser.yml`, so merged PR titles — not individual commit subjects — are the source of release-note entries.
- **The `v0` tag** is a moving tag consumed by downstream orgs for reusable
  workflows. It is automatically moved by the release workflow after
  GoReleaser completes (skipped for pre-release tags).
- **Agents validation runs before anything is published.** On a `v*` tag
  push the workflow first runs `resolve-agents` (verifies the tag still
  points at the commit that triggered the run, checks the gate secrets
  are configured, and records agents' `main` SHA), then `validate-agents`,
  which runs agents' functional tests (via a cross-repo reusable workflow
  call) against the release tag. Only if those pass does the `release` job
  re-verify the tag and run GoReleaser. A failure at either step means
  **no binaries or GitHub Release are published** (images are still
  built — see below), and `v0` does not move; a Slack notification
  reports the release as blocked. If the cause is a flake or transient
  infrastructure issue, resolve it and re-run the failed jobs — the tag
  stays as it is. If the fix needs a code or pin change (e.g. the
  functional-tests gate pin drift described in pre-flight step A3), the
  tag is now blocked: for a final tag, delete it both locally and
  remotely (`git tag -d vX.Y.Z && git push origin :refs/tags/vX.Y.Z`) —
  otherwise the next `git tag -a vX.Y.Z` fails because the old tag still
  exists — then cut `rc.N+1` from the fixed commit instead (back to step
  6). An RC tag that fails this way has no name to free up; just cut
  `rc.N+1`.
- **A final tag can also fail in the `release` job itself**, after the
  gate passes, with `422 already_exists`. This happens when the final
  shares a commit with its RC: GoReleaser resolves the tag to build via
  `git tag --points-at HEAD`, finds both tags on that commit, and builds
  as the rc tag — then fails re-uploading the RC's assets to the RC's
  own release, which already has them (this is what happened on
  `v0.44.0`). **`422 already_exists` alone doesn't prove this**, though:
  `release` can also hit its own already-uploaded assets on a rerun of a
  prior successful publish (e.g. after a later step such as `tag-agents`
  or the `v0` move failed and the whole job was re-run). Before touching
  the tag, check the `release` job's GoReleaser logs for which tag it
  resolved and built — the RC-selection failure shows the RC tag, not
  the final — and confirm the final has no GitHub Release or binary
  assets yet (`gh release view vX.Y.Z`). Only when both are confirmed is
  nothing published in this case, and re-running cannot fix it because
  the commit itself is the problem: delete the blocked final tag both
  locally and remotely (`git tag -d vX.Y.Z && git push origin
  :refs/tags/vX.Y.Z`) — otherwise the next `git tag -a vX.Y.Z` fails
  because the old tag still exists — and re-tag at a commit Y ≠ X that
  passes the step 9 check (see the interim rule there, fullsend#7955).
  The blocked final's `:X.Y.Z` images (built independently by Sandbox
  Images — see below) were already published and get overwritten when
  the tag is re-pushed; that's harmless, since the fleet only pins RC
  digests (step 8), never a final's. If the final already has a GitHub
  Release or binary assets, it already shipped — preserve the tag and
  investigate the later step that failed instead.
- **The `fullsend-ai/agents` repo** is tagged with the same version last,
  by the `tag-agents` job, using an org-owned GitHub App token
  (`RELEASE_APP_ID` / `RELEASE_APP_PRIVATE_KEY`). This is the only step
  that can fail after the binary has shipped; when it does, a Slack
  notification is sent and only the agents tag is missing. That tag push
  triggers agents' own `release.yml`, which creates a GitHub Release and,
  for non-prerelease tags, moves its `v0` floating tag.
- **`tag-agents` runs for prereleases too.** Unlike the `v0` move (which
  `release` skips for any tag containing `-`), `tag-agents` tags
  `fullsend-ai/agents` for RC tags as well as final ones — so an RC that
  passes the gate leaves agents tagged at `vX.Y.Z-rc.N`, not just at the
  eventual final version.
- **`sandbox-images.yml` builds independently of the publish gate.**
  GitHub Actions ignores `paths` filters on tag pushes, so every `v*` tag
  triggers a sandbox/code image build and push regardless of whether
  `resolve-agents`/`validate-agents` pass — a blocked release (per the
  note above) can still leave new `:X.Y.Z` images in the registry. The
  images built for the final tag are therefore not guaranteed to match
  the ones built for its RC at the same commit (see the repin step).
  Each tag push is an independent, non-reproducible build.
