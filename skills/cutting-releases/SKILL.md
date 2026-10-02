---
name: cutting-releases
description: >
  Use when the user wants to tag a release, cut a release candidate, or ship a
  new version. Also use when asking about release process, versioning, or how
  GoReleaser is configured.
allowed-tools: Read, Grep, Glob, AskUserQuestion, Agent, Bash(git tag:*), Bash(git log:*), Bash(git diff:*), Bash(git pull:*), Bash(git push:*), Bash(gh release:*), Bash(gh run:*), Bash(gh api:*), Bash(gh pr:*), Bash(git checkout:*), Bash(git fetch:*), Bash(skopeo inspect:*), Bash(bash skills/cutting-releases/scripts/install-binary.sh:*)
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

Check the latest tag:

```
git tag --sort=-v:refname | head -5
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

### 5. Gather changes since last tag

```
git log --oneline <previous-tag>..HEAD
```

Summarize changes into categories (features, fixes, refactors). Exclude
`docs:`, `test:`, `chore:`, `ci:`, `build:` commits — GoReleaser filters these anyway.

### 6. Create the RC tag

Every release starts as a release candidate — even one you expect to ship
with no changes after the gate run. Build the tag message:

- **Line 1 (subject):** The custom title from step 4, if one was given.
  If no custom title, **use the tag name itself** (e.g. `v0.9.0-rc.1`) —
  git's `%(contents:subject)` skips leading blank lines, so a blank first
  line still picks up the first category header as `.TagSubject`. Using the
  tag name as subject ensures `.TagSubject == .Tag`, which the goreleaser
  guard suppresses, producing a clean release title with no suffix.
- **Line 2:** Blank.
- **Lines 3+:** Summary of highlights organized by category.

```
git tag -a v0.X.0-rc.1 -m "<message>"
```

Record the commit the RC was cut from (`git log -1 --format=%H`) — call it
commit X. The final tag in step 9 must point at this same commit unless a
new RC is required in the meantime.

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
passes, GoReleaser publishes the `:X.Y.Z-rc.N` binaries and images and
marks the GitHub release as a prerelease; `v0` does not move for a
prerelease tag, but `tag-agents` still runs and tags
`fullsend-ai/agents` at this same prerelease version (see Notes).

**If the gate fails:** fix forward on `main`, increment N, and cut
`rc.N+1` from the new commit (back to step 6). The failed RC's images stay
published in the registry but unused — never delete a published tag (see
Notes).

### 8. Repin the fleet harness images

Once the RC gate is green, repin the 7 harness images in
`fullsend-ai/agents` to this RC's digests before tagging the final. The
final tag in step 9 must wait until this repin PR is merged.

1. **Resolve the RC digests.** The `sandbox-images.yml` workflow publishes
   images tagged with the bare semver (no `v` prefix):

   ```
   skopeo inspect --no-tags docker://ghcr.io/fullsend-ai/fullsend-sandbox:X.Y.Z-rc.N
   skopeo inspect --no-tags docker://ghcr.io/fullsend-ai/fullsend-code:X.Y.Z-rc.N
   ```

   Check each result's `org.opencontainers.image.revision` label equals
   commit X. If it doesn't, the registry tag was overwritten by a later
   build — do not proceed; investigate which commit actually built it.
2. **Open a repin PR against `fullsend-ai/agents`** replacing the `image:`
   line in the 7 harness files with the resolved digests:
   - `fullsend-sandbox` → `harness/prioritize.yaml`, `harness/retro.yaml`,
     `harness/scribe.yaml`, `harness/triage.yaml`
   - `fullsend-code` → `harness/code.yaml`, `harness/fix.yaml`,
     `harness/review.yaml`

   Pin the multi-arch index digest only (`@sha256:...`) — never `:latest`
   or a per-platform digest. Use
   [fullsend-ai/agents#1570](https://github.com/fullsend-ai/agents/pull/1570)
   as the template for the PR diff and description.
3. **Hold window.** Between pushing the RC tag and merging the repin PR,
   do not merge PRs in this repo touching `images/sandbox`, `images/code`,
   or `.github/workflows/sandbox-images.yml`. A merge in that window can
   change what the *next* build produces, and because the image build is
   not reproducible (see Notes), that leaves no way to re-obtain the
   digests being repinned if they need re-verifying.
4. **Wait for the repin PR to merge** before proceeding to step 9.

### 9. Tag and push the final release

Confirm the final tag can point at commit X. If you are tagging later than
the RC (commit Y, e.g. because the repin PR merge is the current `main`
tip), run:

```
git log --oneline X..Y -- images/sandbox images/code .github/workflows/sandbox-images.yml
```

- **Prints nothing:** the fleet images haven't changed since the RC was
  built, so the final tag can safely point at Y (or X).
- **Prints commits:** a change since the RC could produce different
  images than the ones just repinned. Do not tag the final yet — cut
  `rc.N+1` at Y instead (back to step 6) and repin again.

Once clear, tag and push the final at the confirmed commit, reusing the
subject and highlights from step 6 with the `-rc.N` suffix dropped:

```
git tag -a vX.Y.Z -m "<message>"
git push origin vX.Y.Z
```

Verify it starts the same way as step 7. This run moves `v0` and tags
`fullsend-ai/agents` at the non-prerelease version once GoReleaser
succeeds (see Notes).

### 10. Run post-flight verification

Read [post-flight.md](post-flight.md) in this skill's directory and
follow the post-flight verification procedure.

### 11. Write release highlights

After post-flight confirms the release is published, write a short user-facing
summary highlighting the changes that matter most to end users.

1. **Gather the raw changelog.** Run `gh release view <tag> --json body -q .body`
   to get the auto-generated release body.
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
- **Never delete a published tag.** If a release is bad, cut a new patch or RC.
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
  **nothing is published** — no binaries, no GitHub Release, no moved
  `v0` tag — and a Slack notification reports the release as blocked. The
  fix is to resolve the cause and re-run the failed jobs; the tag stays
  as it is.
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
  note above) can still leave new `:X.Y.Z` images in the registry. This is
  also why the images built for the final tag are not guaranteed to match
  the ones built for its RC at the same commit (see the repin step):
  each tag push is an independent, non-reproducible build.
