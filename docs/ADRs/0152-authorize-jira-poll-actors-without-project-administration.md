---
title: "152. Authorize Jira poll actors without project administration"
status: Accepted
relates_to:
  - security-threat-model
  - agent-architecture
topics:
  - authorization
  - jira
  - least-privilege
---

# 152. Authorize Jira poll actors without project administration

Date: 2026-10-28

## Status

Accepted

## Context

The `jira-poll` input driver
([ADR 0063](0063-polling-based-work-discovery.md)) authorizes each event
author under
[Authorization Contract v1](../normative/authorization/v1/README.md)
([ADR 0054](0054-require-authorization-on-all-agent-dispatch-paths.md)).
It maps the author's Jira project role name to a Fullsend role. At
`189d3637ce77b05c21009ac2cc69c0fa9223d89e`, `Poller.Run`
(`internal/jirapoll/poller.go`) calls `loadRoleStructure` on every cycle
that selects at least one issue, and `LiveClient.GetProjectRoleActors`
(`internal/forge/jira/client.go`) reads `GET /rest/api/3/project/<KEY>/role`
and each role's detail. Jira Cloud serves those endpoints only to accounts
with **Administer Projects**. So the long-lived polling credential must
hold the project's Administrators role just to read who may trigger
agents ([#8401](https://github.com/fullsend-ai/fullsend/issues/8401)).
That is broader than polling needs, which conflicts with the least-privilege
principle in the
[security threat model](../problems/security-threat-model.md).

At the same SHA, `authorizeEvent` (`internal/harnessdispatch/auth.go`)
already authorizes GitHub label-added events without a role threshold,
because applying a GitHub label needs triage access. Jira has an equivalent
check: changing labels needs the **Edit Issues** permission.

## Options

- **Keep project roles as the only source.** No change, but every poller
  keeps Administer Projects.
- **Trust any commenter.** No Jira permission needed, but anyone who can
  comment could run write-gated agents.
- **A Jira permission-check API.** Jira Cloud offers these only to
  administrators. `/user/permission/search` needs Administer Projects, and
  `/permissions/check` for another user needs Administer Jira.
- **Config-owned lists plus Jira's own label permission** (chosen).

## Decision

Jira actors are authorized without the project role roster unless a
repository opts into it:

1. **Label-added events are authorized by Jira's Edit Issues permission**
   by default, like GitHub label-added events. The dispatch gate admits a
   Jira `label_changed` event with `label.action: added` for every matched
   harness. The poller still drops its own account's events as bot events.
2. **A `jira` provider under `authorization:`** in `.fullsend/config.yaml`
   lists Jira groups and account IDs for the Fullsend `write` and `triage`
   roles. It also sets `labels:` to `jira_editors` (the default), `allowlist`,
   or `project_roles`. It applies only to events with `source.system: jira`.
   When the provider sets a `write` or `triage` list (even an empty one),
   the lists are authoritative for slash commands and every other non-label
   event, so emptying them denies everyone rather than re-enabling the
   project role fallback. An actor in `write`
   is `write`, an actor only in `triage` is `triage`, and anyone else is
   `none` (denied). Groups are resolved with `GET /rest/api/3/user/groups`,
   which needs only Browse users and groups, and are cached per actor per
   cycle. Account entries need no Jira call. `labels: allowlist` checks label
   authors against the same lists. `labels: project_roles` uses the roster.
3. **The roster is loaded only when an event uses it.** That happens for a
   slash command when no lists are configured, and for a label-added event
   under `labels: project_roles`. A load failure still fails the cycle
   without advancing checkpoints. Without lists, the other events (issue
   opened, edited, closed or reopened, plain comments, label removals) are
   not resolved, carry `actor.role: none`, and pass no role threshold. A
   setup that uses the lists, or only label triggers under `jira_editors`,
   never calls `GET /rest/api/3/project/<KEY>/role`.

With no `jira` provider, slash commands keep the project-role lookup.

## Forge scope

This decision applies to Jira-sourced events from the shared `jira-poll`
driver. It is the same under GitHub Actions and GitLab CI, which both run
that driver. GitHub, GitLab, and Forgejo event authorization, and the
`owners_file` provider, are unchanged.

## Consequences

- A Jira poller needs only Browse Projects, Edit Issues, Add Comments, and
  Edit Own Comments. It also needs Browse users and groups when the lists use
  groups, and Administer Projects only when project roles are used.
- By default, Jira label triggers are authorized by Edit Issues. In many
  projects that is broader than the Developers role, so repositories that
  want the old rule must set `labels: project_roles`.
- Without lists, Jira issue opened and edited events no longer pass role
  thresholds. A trigger on them, such as the default triage harness's, needs
  the `jira` lists.
- Repository configuration becomes a second place to keep Jira
  authorization in sync with Jira group membership.
