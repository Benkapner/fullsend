# Authorization Contract v2

Normative rules for event actors, entity CEL facts, and model-bound records.

This version applies a verified `triage`-or-higher human floor to
ordinary event admission, actor-originated entity facts needed for actionable
CEL results, and Fullsend-controlled model-bound records. Harness CEL may
impose stronger role conditions.
[ADR 0151](../../../ADRs/0151-common-actor-admission-floor.md) establishes this
common floor while preserving separate bot and verified-label grants and fresh
model-bound checkpoints.
This version also adopts selective entity acquisition and decision-focused
audit evidence for the future entity-first path under
[ADR 0147](../../../ADRs/0147-evaluate-entity-cel-over-selectively-acquired-data.md),
with bounded CEL actor-verdict reuse under
[ADR 0148](../../../ADRs/0148-reuse-verified-actor-roles-for-entity-cel.md).
Existing dispatch continues to use [v1](../v1/README.md) until migrated.
The historical decision and rationale are recorded in
[ADR 0054](../../../ADRs/0054-require-authorization-on-all-agent-dispatch-paths.md).
The [NormalizedEvent v1](../../normalized-event/v1/) specification defines the
`actor.role`, `actor.kind`, `actor.role_verified`, and `actor.bot_role` fields
consumed by this contract. Bot identity and role resolution are specified by
[ADR 0107](../../../ADRs/0107-bot-identity-resolution-for-dispatch-authorization.md).

> **ADR 0107 target contract — not yet implemented:** The bot identity fields,
> resolver outcomes, and bot-specific fail-closed rows below describe the
> behavior to be implemented by the adapters and dispatch path. Until that
> migration lands, existing compatibility behavior remains authoritative.

## Role ordering

Fullsend uses a forge-neutral role hierarchy. Roles are listed from most
to least privileged:

```
admin > maintain > write > triage > read > none > external
```

| Role | Meaning |
|------|---------|
| `admin` | Full repository control |
| `maintain` | Repository settings without destructive admin |
| `write` | Push, label, comment, merge |
| `triage` | Label and moderate without push access |
| `read` | Read-only collaborator |
| `none` | Authenticated user without explicit repository permission |
| `external` | Actor outside the repository or project (currently Jira-only; GitHub maps non-collaborators to `read` on public repositories or `none`; GitLab maps them to `none`) |

### Forge-native permission mapping

| Forge | Native level | Fullsend role |
|-------|-------------|---------------|
| **GitHub** | `admin` | `admin` |
| | `maintain` | `maintain` |
| | `write` | `write` |
| | `triage` | `triage` |
| | `read` | `read` |
| | _(no collaborator entry)_ | `none` |
| | _(fork/non-collaborator)_ | `read` (public repository) or `none` |
| **GitLab** | Owner | `admin` |
| | Maintainer | `maintain` |
| | Developer | `write` |
| | Reporter | `triage` |
| | Guest | `read` |
| **Jira** | Administrators | `admin` |
| | Developers | `write` |
| | _(other named role)_ | `read` |
| | _(no project membership)_ | `external` |

On GitHub the mapping source is the collaborator permission API
(`GET /repos/{owner}/{repo}/collaborators/{username}/permission`), which
returns the user's **effective** role including inherited org grants
regardless of membership visibility. A built-in `role_name` is used as is.
Custom repository roles are an organization-level GitHub Enterprise Cloud
feature, so most repositories only ever return built-in names; Fullsend
does not interpret a custom role's name or its fine-grained permissions.
A custom repository role resolves to the strongest base role set to `true`
in `user.permissions` (`admin`, `maintain`, `push` → `write`, `triage`,
`pull` → `read`), or `none` if no flag is set. Only when
`user.permissions` is absent or null does the legacy `permission` field
(`admin`, `write`, `read`) apply; anything else is `none`. Legacy `read`
stays `read`, because it cannot distinguish Read from Triage. Fork authors
and non-collaborators are mapped to `read` (public repositories) or `none`, never `external`.
The `external` role is currently produced only by the Jira adapter for
actors without project membership. The common human floor denies `read`,
`none`, and `external` alike. The `author_association` field is
**not** used because it does not correctly reflect private org membership
(see [Excluded fields](#excluded-fields)).

A GitHub repository that lists `owners_file` under `authorization` in
`.fullsend/config.yaml` consults its repo-root Prow `OWNERS` file before the
collaborator API:

| OWNERS list | Role granted |
|-------------|--------------|
| `approvers` | at least `write` |
| `reviewers` | at least `triage` |
| _(not listed)_ | none; the collaborator API decides |

OWNERS can only raise a role, never lower one. An entry that names an
`OWNERS_ALIASES` key stands for that alias's members. A login equal to any alias
key never matches, and nested aliases are not expanded. The files are read from the base branch for PR-scoped events and
from the default branch otherwise, so a PR cannot add its author. See the
[`authorization` field](../../../reference/config-reference.md#authorization).

On Jira the mapping source is the project's role membership roster for the
configured `--jira-project`, resolved once per poll cycle for event-backed
dispatch. Entity-source CEL may reuse a completed
provider-verified actor verdict across poll cycles and bound `run` checks only
under the bounded cache rules below; event admission does not use that cache.
An actor is matched by Jira account ID. Role names are matched
case-insensitively against the names above; unrecognized role names fall
through to `read`. Actors with no membership in the configured project —
or whose issue belongs to a different project — are mapped to `external`
(fail-closed). See the
[Jira integration guide](../../../guides/user/jira-integration.md) for
details on the name-based matching limitation.

### Bot identity fields

The adapter/provider MUST classify the verified actor using authoritative
source-system metadata before authorization. This may include a provider actor
name when that forge gives it bot-specific semantics, such as GitHub's `[bot]`
logins; labels, review types, and arbitrary event-content strings are not bot
identity signals. For post-migration producers with `actor.kind: bot`,
`actor.role` MUST be `none`, and
`actor.role_verified` is true exactly when the provider completes the bot-role
lookup, including a successful no-match result; it is false when resolution
fails. For `actor.kind: human`, `actor.bot_role` MUST be absent or `null`, and
`actor.role` contains the forge permission role when `actor.role_verified` is
true. For non-label dispatch, only a non-null, provider-resolved
`actor.bot_role` can pass the bot gate. The human `triage` floor does not apply
to a recognized bot; platform authorization instead requires a valid
normalized event, an applicable source and target, and a transition supported
by the selected harness. CEL may further restrict routing but cannot create or
broaden bot identity authorization.

For humans on ordinary non-label paths, `role_verified: false` makes
`actor.role` unusable as a forge-permission claim. It does not invalidate an
independently verified configured-source grant such as OWNERS for the same
verified actor and target. Fullsend denies the event if no verified source
establishes an effective role meeting the common floor. The versioned CEL
input MUST carry that effective role and its verification source without
reinterpreting v1 `actor.role_verified`. A missing `role_verified` on a trusted
pre-migration event is treated as legacy input and retains the current human
authorization behavior; new adapters MUST emit the field. Legacy status is
determined by the adapter implementation contract, not by guessing from this
field's absence. On the permanent label exception, `role_verified` is not an
authorization input: an accepted forge label mutation authorizes a human, and
a bot additionally requires positive provider bot classification. For bots,
omission means that bot-role lookup is not applicable, not that it failed.

## Common human authorization floor

For an ordinary human prompting event, Fullsend MUST resolve a fresh, verified
effective normalized role of at least `triage` before harness CEL runs. The
effective role comes from the source provider or a trusted configured
permission source such as OWNERS. The same floor applies independently to
actor-originated entity facts whose authority is needed to make CEL actionable
or prove an actionable absence or aggregate, using a live or eligible bounded
verdict under the rules below. Raw state used only for conservative deferral
follows the causal classification below and does not require authorizing every
historical updater. A Fullsend-controlled model-bound record has its own fresh
check against the floor at the required exposure checkpoints.
`read`, `none`, `external`, and unresolved human roles do not pass. Event
admission does not authorize another actor's fact or model-bound record.
Trusted Fullsend invocation provenance, rather than a human event actor,
authorizes eventless entity discovery; actor-originated facts that serve as
authority still pass their own floor.

This floor does not depend on a legacy workflow stage, harness `role`, token
`privilege_levels`, or selected harness. The platform MUST expose to CEL the
same verified effective human role it used for admission, including any valid
configured OWNERS upgrade. Harness CEL MAY require `write` or a stronger role
and MAY add other restrictions, but it cannot admit an actor or fact rejected
by the platform floor. A false CEL predicate is a harness nonmatch, not a
platform authorization denial.

### Current dispatch compatibility

The existing GitHub bash dispatcher retains its route-specific human guards
and bot compatibility behavior, as recorded in
[Authorization v1](../v1/README.md). Those routes do not classify CEL
harnesses or set their role requirements. After provider-backed
`actor.bot_role` recognition, bots use the separate bot gate above.

> **Current Go implementation:** `IsAuthorized()` in `harnessdispatch` still
> applies `write+` to ordinary events before `MatchHarnesses()`, including
> poll-based dispatch. This is current compatibility behavior, not the v2
> common floor. Before lowering that gate, migration must make the
> OWNERS-adjusted effective role visible in a versioned CEL input and add
> intended stronger role conditions to first-party mutation harness
> predicates. Today the Go path applies an OWNERS upgrade to its platform
> check but passes the original event role to CEL.

## Fail-closed behavior

The ordinary human role gate is **fail-closed**: when the effective role cannot
be determined, that path is denied. The verified label-transition grant and
registered-bot gate below use their own provider evidence.

The entity-evaluation rows below specify the future ADR 0098 path as revised by
[ADR 0147](../../../ADRs/0147-evaluate-entity-cel-over-selectively-acquired-data.md)
and [ADR 0148](../../../ADRs/0148-reuse-verified-actor-roles-for-entity-cel.md)
and do not describe behavior currently implemented by `fullsend dispatch` or
`fullsend poll`.

The human role-lookup rows below apply to ordinary non-label paths. The
bot-specific rows are ADR 0107 target behavior; legacy events remain subject
to the compatibility rules until implementation migration is complete.

| Condition | Outcome |
|-----------|---------|
| Collaborator API returns a custom `role_name` with no effective permission signal | Forge role maps to `none`; a separate verified OWNERS grant may satisfy the floor, otherwise deny |
| Collaborator API returns an error or times out | Forge role is unusable; a separate verified OWNERS grant may satisfy the floor, otherwise deny |
| Bot-role lookup returns no registered identity on a non-label path | `actor.role` remains `none`; `actor.role_verified` is true; `actor.bot_role` is absent; denied |
| Bot-role lookup fails or is unverifiable on a non-label path | `actor.role` remains `none`; `actor.role_verified` is false; `actor.bot_role` is absent; denied, with the failure retained in resolver/audit diagnostics |
| Collaborator API response is not valid JSON or does not match the expected shape | Forge role is unusable; a separate verified OWNERS grant may satisfy the floor, otherwise deny |
| `actor.role` is empty or missing | Event fails `NormalizedEvent` validation; never reaches dispatch |
| `actor.role_verified` is false for a human on an ordinary non-label path | Ignore `actor.role`; require a separate verified configured-source grant meeting the floor, otherwise deny |
| Legacy bot event has an `actor.role` other than `none` | Valid in the v1 compatibility schema; authorization follows current compatibility behavior during migration and is not an ADR 0107 bot-role authorization result |
| `actor.role_verified` is false but `actor.bot_role` is non-null for a bot | Event fails `NormalizedEvent` validation; never reaches dispatch |
| `actor.bot_role` is non-null for a human | Event fails `NormalizedEvent` validation; never reaches dispatch |
| Username is empty | Denied |
| `OWNERS` is missing or malformed, or `OWNERS_ALIASES` is present but malformed (`owners_file` enabled) | OWNERS check skipped; the collaborator API decides |
| Username contains characters outside `[A-Za-z0-9-]` (`owners_file` enabled) | OWNERS check skipped; the collaborator API decides |
| Fullsend invocation provenance for poll discovery or a bound run's eventless check is missing or unverifiable | That eventless evaluation is denied |
| Harness entity sources are missing or malformed | That harness is skipped for scheduled evaluation |
| Effective platform eligibility policy is missing, malformed, or unverifiable | Scheduled evaluations governed by that policy are denied |
| Effective platform eligibility policy uses a wildcard without explicit platform-level justification | Scheduled evaluations governed by that policy are denied |
| A required entity field or history scope is not yet loaded, or pagination/coverage is incomplete | Its value is unknown, never false or empty; read more or defer to bound `run`; no sandbox starts from an unresolved result |
| A poll candidate remains inconclusive, cannot be handed off, or its bound run fails before completion or safe deferral | Retain a durable retry path or prove source rediscovery after checkpoint advancement; no sandbox starts from an unresolved decision |
| A required entity source is unsupported, unavailable, fails, or cannot establish complete coverage within allowed bounds | Evaluation fails closed; no sandbox starts and the failure is not recorded as a false CEL result |
| A conservative dependency or provider query cannot be shown to include every authorized fact that could change a definitive CEL result | Expand the plan or leave the result unknown; if it cannot be resolved, evaluation fails closed |
| Current attribution or a live or eligible cached actor-role verdict for a fact requiring the actor gate is missing or unverifiable | The fact cannot be admitted as authority; if it could affect an actionable result, evaluation remains unknown or fails closed and cannot start a sandbox |
| A versioned platform policy with an explicit maximum age no greater than five minutes for cross-check CEL actor-verdict reuse is missing or unverifiable | Reuse is disabled; obtain a live authoritative role verdict or leave dependent CEL unknown, with no sandbox on unknown |
| A needed CEL actor-verdict cache entry is expired, invalidated, or scoped to another source, actor, repository, policy, or revision | Refresh from the authoritative actor-role source; if that cannot complete, the dependent result remains unknown and cannot start a sandbox |
| The actor of a fact requiring the gate is human and below the common `triage` floor, or is a bot with no recognized role | The fact is excluded from the authorized CEL view and cannot trigger execution |
| Versioned normalized-entity contract is missing or incomplete | Entity-first execution denied |

## Exceptions

Only the verified label-added transition below bypasses the ordinary human
`triage` floor in v2. Registered-bot roles use a separate gate. The legacy
compatibility paths described below do not create additional v2 grants or
authorize unrelated entity facts or model-bound records.

### Label application

**Current compatibility behavior:** GitHub authorizes an added-label event
without applying the ordinary `actor.role` threshold. This existing behavior
preserves bot-to-bot handoffs such as adding `ready-to-code` after triage
completes, including handoffs from provider bots that are not Fullsend-
registered.

**ADR 0107 target behavior:** When
`transition.kind` is `label_changed` and `label.action` is `added`, GitHub
authorizes the event regardless of `actor.role` once the adapter has
established an accepted forge label mutation and authoritative actor-to-
transition provenance. This is a separate platform authorization grant, not an
application of the ordinary human floor. At the time of this ADR, GitHub
is the only implementation; another adapter MUST provide equivalent
forge-authoritative evidence before enabling the same grant, and absent or
unverifiable evidence denies the event. For a bot actor, the adapter MUST
positively classify it using provider-controlled metadata, but `actor.bot_role`
lookup is not required. The platform gate MUST NOT inspect the label name or
maintain an agent-role label allowlist; harness/CEL routing owns that mapping
and may further narrow the dispatch.

### Bot-submitted reviews (GitHub)

**Current compatibility behavior:** Until the resolver is implemented,
`fullsend dispatch` continues to authorize a GitHub `review_submitted` event
from a bot without requiring a collaborator permission or `actor.bot_role`.
This existing exception is not evidence that the bot identity was resolved.

**ADR 0107 target behavior:** After the resolver is implemented, a GitHub
review event is authorized only when the provider classifies the actor as a
bot and resolves its exact registered `actor.bot_role`; `actor.role` remains
`none`. The downstream harness CEL trigger may further constrain which bot
role and review state are accepted.

### Legacy lifecycle close (pull\_request\_target.closed)

**Current bash compatibility behavior:** The GitHub bash dispatcher routes
`pull_request_target.closed` to retro without a separate collaborator-role
check, so any closer may trigger it. This is not a read-only capability:
retro's token can write issues and pull requests, and its post-script can
file issues, comment, and apply labels. The Go `IsAuthorized()` path has no
close-event exception and applies its current `write+` gate.

The ungated bash route is not a general v2 grant for CEL-based dispatch or
entity facts. Under this contract, an ordinary human close-event actor must
meet the common `triage` floor unless a separate platform exception is
adopted. Harness CEL may add stricter conditions. The legacy `/fs-retro`
slash command retains its bash `write` guard.

### Schedule and manual dispatch

When `source.system` is `schedule` or `manual`, the actor is the configured
operator or service identity (for example, a GitHub App bot or workflow
`GITHUB_ACTOR`). Adapters MUST classify that identity from authoritative
provider metadata. Human identities use verified forge permissions and the
common human floor; bot identities use provider-backed `actor.bot_role`
resolution and retain `actor.role` as `none`. An unrecognized or unresolved
bot identity is denied.

**Current compatibility behavior:** Until provider-backed bot-role resolution
is wired into the adapters and dispatch path, adapters set `actor.role` to the
service identity's effective repository permission (typically `write`), so
the standard permission gate applies.

> **Target contract, not yet implemented:** Existing schedule/manual
> compatibility handling remains authoritative until provider-backed bot-role
> resolution is wired into the adapters and dispatch path.

### Entity-source CEL evaluation

This future path was adopted by
[ADR 0098](../../../ADRs/0098-entity-first-harness-evaluation.md), clarified by
[ADR 0144](../../../ADRs/0144-reuse-poll-and-entity-bound-run-processes.md),
and revised for selective acquisition and audit by
[ADR 0147](../../../ADRs/0147-evaluate-entity-cel-over-selectively-acquired-data.md),
with bounded cross-check actor-verdict reuse under
[ADR 0148](../../../ADRs/0148-reuse-verified-actor-roles-for-entity-cel.md).
[ADR 0151](../../../ADRs/0151-common-actor-admission-floor.md) supplies the
common human floor.
It applies to an entity-source harness's CEL predicate whether a verified
prompting event is present or `event == null`; legacy event-only dispatch is
unchanged. The entity-source path is not yet implemented by `fullsend dispatch`,
`fullsend poll`, or bound `fullsend run`. Entity-first execution MUST NOT be
enabled until the versioned normalized-entity contract defines CEL-eligible and
prompt-eligible fields and their coverage states. That contract is a platform
allowlist; a harness may request fewer fields but cannot expand it.

With a prompting event, the event-actor authorization gate above still runs
before harness CEL; neither discovery nor an entity-source query replaces it.
The event actor authorizes that event, not other actor-originated entity facts.
The verified GitHub label-added exception authorizes CEL to use that exact
accepted label transition without the ordinary human floor. It
does not authorize other label applications, the entity's current label state
without transition provenance, or other records from the same actor.
Current label state may use that grant only when the adapter proves it is the
projection of the same accepted transition, with no intervening replacement.
For an eventless check, the following Fullsend-origin and platform-eligibility
gate applies instead.

`fullsend poll` may enumerate candidates without synthesizing a
`NormalizedEvent` or event actor. A bound `fullsend run` may instead recheck
only its canonical entity with `event == null`, including after consuming an
initial event. Both paths require eligible entity sources and effective
platform policy permitting eventless evaluation. They are authorized only when
the caller has trusted Fullsend-controlled invocation provenance: a verified,
non-user-assertable platform execution identity bound to the invocation and its
target, such as an attested workflow/job identity or installation credential.
Neither the actor of an earlier event nor an authenticated wake peer supplies
this authority. A CLI flag, request header, or other caller-supplied claim is
insufficient. A caller that cannot establish that provenance MUST be denied.
Wildcard eligibility (`*` or `all`) requires the same explicit platform-level
justification as any other wildcard allowlist in this contract.

Each entity-source evaluation's audit record MUST identify its verified
initiator (the event actor for an event-bearing check, or the trusted Fullsend
invocation identity for an eventless check), target repository, effective
policy, harness revision, canonical entity, acquisition-plan identity, and
decision outcome, including `UNKNOWN`, deferred, or denied outcomes. It MUST
record which source scopes and bounded queries were needed, their coverage and
completeness evidence, and the current attribution, live or eligible cached
verified role, and platform-gate outcome for individual actor-originated
records used as decisive witnesses. Complete negative or aggregate claims
require bounded query and source-coverage evidence bound to the effective
authorization policy, permission source, and check time or equivalent
authorization epoch.
Summaries MAY use counts and digests of admitted, excluded, and unresolved actor
checks; they do not require
an audit roster of every record and actor examined to establish completeness.
When an actor verdict is reused for CEL, the audit MUST record bounded evidence
of its authoritative source, lookup start time or age measured from that start,
the completed verdict, the maximum age allowed by the effective platform
policy, effective authorization policy and source revision, and whether a live,
local-cached, or shared-cached verdict was used. This may be aggregated for
collection-wide checks rather than listing every actor.
If a cached actor verdict contributes to a false CEL result, the audit MUST
also identify the bounded recheck or rediscovery guarantee, or show that the
candidate remains retryable.
Unrelated history is not fetched or listed solely for audit. The audit does not
retain raw rejected content. An eventless child cycle attributes initiation to
the Fullsend identity, never to an earlier event actor or historical entity
actor.

Authorization of an event actor or Fullsend invocation origin does not
authorize other entity content. For each evaluated harness, Fullsend derives a
conservative acquisition plan from its CEL predicate and the normalized-entity
contract. The platform retains a closed classification of action-indicating
categories; it need not fetch every category for every predicate. The plan
MUST cover every field, history range, or derived fact that could change the
predicate's result, including actor-originated issue or
change-proposal bodies, comments, reviews, and label applications or current
label state when used as action-indicating evidence. Counts, existence tests,
and other aggregates over actor-originated records have the same dependency.
The evaluator MAY defer planned reads and stop once partial evaluation yields
a definitive result.
A source field is actor-neutral for a particular CEL use only when an
actor-originated change cannot make the predicate newly actionable. For a
service-clock predicate, raw authoritative `updated_at` means time since any
forge update; it is not recomputed from actor-filtered history. Where the
provider guarantees that it only advances, `updated_at < moving_cutoff` can
become true as time passes. An update, including one by an unauthorized actor,
can defer that staleness run but cannot make it newly eligible. A harness that
needs time since the last authorized update must acquire and authorize the
relevant history. By contrast, `updated_at >= cutoff` can become true because
of an update and requires provenance and authorization for that causal
occurrence, or remains unknown. Raw `comments_count`, current labels, or
newest-comment metadata cannot enter the authorized CEL view if an unauthorized
record or change could affect the result. Fullsend MUST recompute such a fact
from admitted records, prove the relevant causal occurrence or platform
exception, or leave it unknown. Presenting an action signal as current state
never bypasses the actor gate.
A provider query MAY narrow reads only when it is proven to include every
authorized fact that could affect the result; otherwise Fullsend expands the
query or leaves the dependent result unknown. A provider aggregate may replace
individual reads only if it preserves the actor-filtered meaning under the
eligible CEL verdicts for the current check and proves the coverage needed for
that result. Unanalyzable dynamic access requires a conservative broader plan
or a fail-closed unresolved outcome.
Cached fields or history may satisfy a planned read only after their source
revision and query coverage are validated for the current check. Accepted
events and ordinary entity wakeups invalidate affected entity facts under
[ADR 0144](../../../ADRs/0144-reuse-poll-and-entity-bound-run-processes.md).
They do not by themselves invalidate a separately scoped actor-verdict cache
entry; a trusted authorization change does.

Fullsend distinguishes not requested or not loaded, partially loaded,
successfully empty, absent or inapplicable, unsupported, and failed data.
Unloaded or incomplete data MUST NOT masquerade as an empty collection or a
false condition. For every actor-originated fact whose authority is needed to
make CEL actionable or establish an actionable absence or aggregate under the
causal classification above, Fullsend MUST re-establish its current
source-revision actor and obtain an eligible verified authorization verdict
before admitting the fact into CEL: a human must meet the common
`triage` floor, using the effective role from all configured permission
sources. A bot needs a provider-backed exact registered role under
[ADR 0107](../../../ADRs/0107-bot-identity-resolution-for-dispatch-authorization.md).
The verified effective role remains available to harness CEL, which may
require a stronger human role for a particular predicate. No harness stage
classification changes the platform floor.
Only the exact transition covered by the verified label-added event exception
may bypass the ordinary human floor or registered-bot-role check. It still
requires accepted transition provenance and positive provider bot
classification when the actor is a bot. The exception supplies authority for
that transition only, not for unrelated entity history. An unauthorized fact is
excluded from the authorized view. Missing or unverifiable attribution or
authorization of a fact needed to prove an actionable result remains
unresolved and cannot prove absence. Historical or inferred actor
relationships are insufficient; only the bounded provider-verified actor-role
cache defined below may replace a live actor-permission lookup. CEL may
further restrict the authorized view, but cannot create authority or replace
this platform gate.

#### Bounded CEL actor-verdict cache

This exception to a fresh actor-permission lookup for each entity check is
adopted by
[ADR 0148](../../../ADRs/0148-reuse-verified-actor-roles-for-entity-cel.md).
For entity-source CEL only, Fullsend MAY reuse a completed, authoritative
human permission or exact registered bot-role verdict across entity checks and
harnesses, poll cycles, separate `poll` and bound `run` loops, and the bound
run's final CEL decision. The cached value MUST be the verified effective
role or verified absence, never a platform-floor boolean or harness-specific
CEL result. Every check reapplies the common floor and its harness CEL
conditions. Both positive and verified negative results
may be cached; errors, timeouts, unresolved lookups, caller claims, and
partially verified roles MUST NOT be cached. The key MUST bind the provider,
tenant, canonical repository or project, immutable actor ID, verified actor
kind, effective authorization policy and its revision or digest, and
permission source. When applicable, it also binds the selected ref and the
presence or absence and content or revision of both OWNERS and OWNERS_ALIASES,
or the bot-registry revision. A login or entity ID is not an actor cache key.
Where an authority does not expose a revision token, the authoritative
lookup's start time defines the
observed epoch; known changes to that source still invalidate its entries.

Within-check memoization under
[ADR 0147](../../../ADRs/0147-evaluate-entity-cel-over-selectively-acquired-data.md)
avoids repeated lookups while the verdict remains eligible under an absolute
five-minute hard cap. Cross-check reuse is disabled until a versioned platform
policy defines and enforces an explicit maximum age no greater than five
minutes. The platform MAY choose a shorter age; repository or harness settings
MUST NOT widen the hard cap or the effective platform bound. Only a completed
verdict may enter the cache. Its absolute expiry is measured from the
authoritative lookup's start at the earlier of the hard cap and any shorter
platform bound. An entry expires at that fixed time even if it is read, handed
from `poll` to `run`, or retained by a shared host cache; no hit extends its
lifetime. An expired verdict must be refreshed before it can contribute to a
definitive CEL decision, including during one long progressive check. Known
role, membership, OWNERS,
OWNERS_ALIASES, bot-registry, or effective-policy changes MUST invalidate
affected entries without waiting for TTL expiry. An ordinary entity wakeup
invalidates affected entity facts, not an otherwise valid actor verdict,
unless it carries trusted evidence of an authorization change. Shared cache
readers MUST enforce expiry with a trusted clock or a conservative skew
allowance; uncertain age makes an entry unusable. An expired, invalidated, or
mismatched entry requires a new authoritative lookup. If that lookup cannot
complete, the dependent CEL result remains `UNKNOWN` or is denied
under the fail-closed rules above; the stale verdict is never used. A shared
host cache MUST remain inaccessible to the sandbox and agent.

A false CEL result dependent on any cached actor verdict MUST cause a recheck
after that verdict expires or is invalidated, and no later than the first of
those events plus a bounded, platform-controlled discovery or reconciliation
interval, even when the entity has not changed. The recheck obtains a new
verdict and may be scheduled directly or guaranteed by source rediscovery. The
platform MUST define that interval and either retain a durable candidate
through checkpoint or watermark advancement or prove rediscovery after it. If
timely re-evaluation cannot be guaranteed, the cached verdict MUST NOT close
the candidate: Fullsend uses a
live verdict for a final false decision or keeps the candidate inconclusive
and durably retryable under a bounded retry schedule. A cached actor result
alone cannot permanently close the candidate.

This deliberately accepts CEL actor-role evidence up to the platform
maximum age. A still-valid positive cache entry may admit a newly observed
actor-originated record after an unobserved revocation, and a negative entry
may exclude a newly promoted actor until expiry and the required bounded
recheck. A positive entry may likewise keep a negative
predicate false after an unobserved revocation. The bound run still checks the
current entity facts, source-revision actor, and coverage before its final CEL
decision; only the verified actor-role verdict may be reused within TTL.
The CEL cache MUST NOT satisfy event-actor admission or
[ADR 0133](../../../ADRs/0133-actor-attributed-data-security-boundary.md)'s
model-bound record admission and exposure checkpoints. Those gates continue to
resolve fresh authority under their separate rules. Fresh model-bound checks
may redact records or deny further execution, but they do not undo a sandbox
launch admitted using a positive CEL cache entry. The launch may already have
occurred after an unobserved revocation within the permitted age.

Fullsend MAY evaluate the predicate over a partially materialized authorized
view. It may return a definitive `true` or `false` only when that result is the
same for every possible completion of still-unloaded inputs admitted under the
actor-verdict rules above and consistent with established source coverage.
This quantifier includes still-valid cached CEL verdicts within the bounded
platform age, even if actor permission changed without a known invalidation.
Coverage MUST account for pagination and concurrent source changes; a required
collection remains incomplete unless a stable view or sound reconciliation
establishes its relevant range. For example, an authorized witness may settle
an existence test as true; false or a negative condition requires complete
coverage of its relevant authorized search space. An `UNKNOWN` result requires
further relevant field or history reads and re-evaluation in `poll` or bound
`run`, or retention
as retryable inconclusive work. A provider failure, unsupported source,
unresolved authorization, or inability to prove completeness in a remaining
decision dependency within allowed bounds fails closed: it cannot be converted
to false, empty, or a sandbox run.

`fullsend poll` MAY hand an unresolved candidate and its acquisition hints to
a bound `fullsend run` without claiming a CEL match. A definitive true result
in `poll` also selects only a candidate, not a sandbox launch. The bound run
MUST independently re-resolve current entity state and actor provenance, use a
live or eligible cached CEL actor verdict, and reach a definitive true result
before starting a sandbox. A definitive false result does not start one. Any
deciding verdict that expires or is invalidated before sandbox admission MUST
be refreshed and CEL re-evaluated; otherwise the decision remains inconclusive
and the sandbox does not start. Any handoff is provisional. Until the bound run
reports a definitive false decision with any cache-dependent recheck durably
arranged, completes actionable work, or safely defers it under
[ADR 0144](../../../ADRs/0144-reuse-poll-and-entity-bound-run-processes.md),
discovery MUST retain a durable retry record or prove that its source query
will rediscover the candidate after advancing a checkpoint or watermark.
Scheduling a run, delivering a wakeup, or receiving a process-start receipt is
not completion. If the bound run fails or remains inconclusive, the candidate
MUST remain retryable and cannot be recorded as false, empty, or handled.

Fullsend MUST omit or minimize unneeded untrusted fields where the normalized
entity contract permits. Further trust or injection filtering MAY run after CEL
routing and before the harness pre-script. Retained content remains untrusted
for dispatch and CEL evaluation throughout.
[ADR 0133](../../../ADRs/0133-actor-attributed-data-security-boundary.md)
defines the separate model-bound admission and filtering gate, as revised
for the human floor by
[ADR 0151](../../../ADRs/0151-common-actor-admission-floor.md): only filtered
content whose source record and actor pass the fresh gate below may be exposed
to the model under bounded harness scope. Any resulting agent run uses the
harness's configured identity and permissions.

## Model-bound actor-attributed data admission

The model-bound gate applies to every source-system record that Fullsend may
expose to a model, regardless of whether the record arrived in an event, was
found by polling or snapshotting, or was read through a proactive or
Fullsend-controlled API. Fullsend MUST establish the record's current actor
attribution through the source system and resolve that actor's current
authorization. A human MUST have a fresh, verified effective role of at least
`triage` at each required checkpoint. A bot MUST have fresh
provider-backed recognition of its exact registered `actor.bot_role` under
ADR 0107; its `actor.role: none` is not compared with the human floor.
Missing or unverifiable attribution, an unrecognized or unresolved bot
identity, and a human below the floor MUST redact the record; there is no
data-only fallback. The platform does not infer a stronger threshold from a
workflow stage or harness identity. Harness-specific conditions for selecting
the run belong in CEL; later model-bound record admission uses this floor and
ADR 0133 filtering. A label-event grant applies only to its exact verified
transition and does not admit unrelated content.

Source-record attribution MAY come from a provider-verified event or be
resolved from the source system. Model-bound actor authorization MUST be
resolved from the current authoritative permission or registered-role source
under the effective policy after event or retrieval admission; it MAY be
co-fetched with a later record read or lazily resolved when first needed. A
resolved result MAY be cached only for the logical lifetime of the
containing event or entity structure and agent invocation, subject to the
fresh checkpoints below. Re-reading or re-fetching the data,
creating a new structure for retry or requeue, or terminating and re-invoking
the agent ends that lifetime and requires fresh resolution, even if the same
in-memory structure is reused. The cache lifetime is not extended by object
identity or by retaining the structure.

Event-carried authorization evidence MAY support event admission and routing,
but MUST NOT satisfy model-bound checks. Event-triggered runs MUST resolve
record-actor authorization after event admission and before model admission,
again immediately before sandbox initialization, and again immediately before
first model exposure. If sandbox initialization and first exposure are
contiguous, one fresh check MAY serve both. Non-event retrieval paths MUST
perform the equivalent checks after retrieval admission. A
Fullsend-controlled capability invoked after sandbox initialization MUST
resolve authorization immediately before releasing each result to the model.
That release check is the mid-run equivalent of the sandbox checkpoints.

A cache entry MUST NOT cross any checkpoint. Implementations MAY coalesce
contiguous checks and MAY batch or cache lookups for multiple records from the
same actor, provided the cache remains within this logical lifetime. Failure,
unavailability, or loss of authorization at any checkpoint MUST redact the
record and, if no authorized records remain, deny the run. Revocation after
first exposure cannot retract content already present in the model context,
but every later retrieval result requires its own release check.

## Excluded fields

The following fields are **not** authorization evidence and must not be
used for dispatch gating:

| Field | Why excluded |
|-------|-------------|
| `author_association` | Does not reflect private org membership; an org admin with private membership gets `CONTRIBUTOR` instead of `MEMBER` ([github/gh-aw-mcpg#2862](https://github.com/github/gh-aw-mcpg/issues/2862)). Also reflects contribution history, not current authority. |
| Contribution history | Past contributions do not confer repository permissions. A former maintainer's contributions cannot substitute for a provider-verified role verdict; a valid CEL cache entry may still reflect a role from before revocation within its bounded age. |
| `actor.is_entity_author` | Being the author of an issue or PR does not grant repository permissions. This field supports routing decisions in CEL triggers, not authorization. |

**Principle:** relationship and contribution-history fields are not
evidence of current authority. Event-backed authorization must be derived from
the source provider's verified permission model or a trusted configured
permission source at event time and meet the common human floor, not from
cached or inferred relationships.
Fullsend-originated eventless evaluation must instead be authorized from
trusted Fullsend-controlled invocation provenance for poll enumeration or
bound rechecks. Actor-originated entity facts needed for an actionable CEL
result are separately gated by current source-revision attribution and a live
or eligible bounded role verdict against the same floor. Deferral-only raw
state follows the causal classification above.
Model-bound records receive separate fresh checks. Historical actors and
relationship fields must not serve as authorization evidence.

## Enforcement point

The common human floor is a **platform-level gate** that CEL cannot override.
Event-backed dispatch checks the normalized event actor's fresh verified
effective role before routing, subject only to the specific bot and transition
grants above. Fullsend-originated eventless evaluation checks trusted
invocation provenance and platform eligibility before resolving the entity. An
entity-source predicate may then be evaluated progressively. An actor-originated
fact that could make CEL actionable, or whose actor-filtered coverage is needed
to prove an actionable absence or aggregate, passes its own floor check using
live or eligible bounded role evidence. Its current source-revision
attribution and coverage are checked separately. Deferral-only raw state
follows the causal classification above. Not-yet-loaded facts remain unknown
rather than being treated as unauthorized, absent, or empty.

```
Forge event
  --> NormalizedEvent (adapter)
  --> Event-actor floor or narrow grant      <-- enforced here
  --> Event-only CEL routing or entity-source resolution
      --> Dependency-driven acquisition and actor gate where required
      --> CEL true / false / UNKNOWN
      --> Execution only on definitive true

Fullsend poll discovery / bound run eventless recheck
  --> Trusted-origin authorization gate     <-- enforced here
  --> Enumerate and resolve candidates (poll) / resolve canonical entity (run)
  --> Evaluate each candidate harness (poll) or the bound harness (run):
      --> Dependency-driven acquisition and actor gate where required
      --> CEL true / false / UNKNOWN (event is null)
      --> Unknown may continue in bound run after current entity re-resolution
      --> Further trust/injection filtering and execution only on true
```

### CEL triggers: harness-specific conditions

Harness `trigger` expressions select runs and may require stronger human roles
or narrower bot roles, labels, and entity states. Such harness-specific
conditions belong in CEL; they never weaken the platform floor or create a
bot or transition grant. A CEL nonmatch is not a platform denial. On the
event-backed path, an event that fails the platform gate never reaches CEL.

The gate precedes harness selection: `IsAuthorized()` currently runs before
`MatchHarnesses()` in the dispatch core. On the Fullsend-originated eventless
path, the trusted-origin gate runs before poll enumeration or bound-entity
resolution and CEL evaluation. For each eligible entity-source harness, the
platform admits actor-originated facts needed for an actionable CEL result
only after current source-revision attribution and a live or eligible bounded
role verdict meeting the common floor. CEL receives that verified effective
role and may apply stronger conditions. The verified label-event grant
authorizes only
its exact transition, as described above. Eventless checks use
`event == null`. Legacy event-only harness routing does not acquire entity
history under this section.

### Per-repo configurability

The common floor and the narrow bot and transition grants are platform-level
security boundaries. Individual repositories cannot disable or lower them.
Per-repo harness registration and CEL triggers operate within those
boundaries: a repository may disable a harness or require stronger conditions,
but cannot make it available to an actor rejected by the platform.

A repository can add permission sources through the `authorization`
field (today only `owners_file`). These sources may raise the verified
effective role used by the platform and visible to CEL; they do not change
the floor or bypass the gate. The current bash
`has_repo_permission` helper and Go `IsAuthorized` check retain their
compatibility behavior until migrated.

## Versioning

This is the v2 normative contract under
[ADR 0015](../../../ADRs/0015-normative-specifications-directory.md).
[ADR 0147](../../../ADRs/0147-evaluate-entity-cel-over-selectively-acquired-data.md)
supersedes v1's future full-history entity-evaluation and audit requirements;
[ADR 0148](../../../ADRs/0148-reuse-verified-actor-roles-for-entity-cel.md)
adds bounded cross-check reuse of provider-verified actor-role verdicts for
entity-source CEL only.
[ADR 0151](../../../ADRs/0151-common-actor-admission-floor.md) sets the common
human floor and moves stronger harness-specific role conditions into CEL.
This changes v1's stage-specific human authorization rules when an
implementation migrates to v2, while preserving the separate fresh
model-bound release checkpoints. Migration also requires versioned event and
entity CEL inputs and an entity-context-snapshot v2 contract for the changed
model-record admission meaning. The
[v1 snapshot](../../entity-context-snapshot/v1/README.md) retains its
historical stage-derived rule until that migration. Authorization v1 remains
authoritative for existing dispatch during migration, including its bash
thresholds and Go `write+` behavior.
Breaking changes to v2 require `docs/normative/authorization/v3/`.

**ADR 0107 migration exception:** The migration from legacy bot authorization
exceptions to provider-backed bot identity is also permitted within v2. It is a
policy tightening, not a new supported actor population: existing
compatibility behavior remains authoritative until an adapter implementation is
updated; once updated, registered bots use `actor.bot_role` and unregistered
non-label bots are denied. The adapter implementation itself is the migration
boundary; no per-event mode marker or separate deprecation window is required.
An updated adapter MUST enforce the target behavior and MUST NOT silently fall
back to legacy bot authorization when its resolver fails. The normalized-event
fields used to carry this distinction are additive, and the permanent
label-added exception is preserved. This exception does not alter the general
v3 rule for unrelated authorization changes.

| Change | v2 impact |
|--------|-----------|
| **Breaking** (requires v3): remove a role from the hierarchy, change the common human floor, alter a target bot or transition grant, change fail-closed to fail-open, remove a fresh model-bound checkpoint, or weaken the actor and coverage gates beyond the bounded CEL cache adopted here | Dispatch implementations must migrate |
| **Adapter bot-identity migration** (allowed in v2): update an adapter to emit and enforce the ADR 0107 target representation | The adapter's existing compatibility behavior applies until that implementation update; afterward, registered bots use `actor.bot_role` and unregistered non-label bots are denied |
| **Non-breaking** (allowed in v2): add a role without changing the floor, add equivalent forge mappings, or clarify documentation | Existing dispatch behavior is preserved |
