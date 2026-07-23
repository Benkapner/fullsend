#!/usr/bin/env python3
"""Build a readiness-oriented queue of open issues/PRs via gh GraphQL (stdlib only).

Deterministic core for the `/nextwork` skill. Seeds a queue (assigned work or
explicit refs), follows open GitHub `blockedBy` links breadth-first, classifies
every item into a status catalog (waiting on automation / blocked / assigned
elsewhere / actionable), and optionally applies trivial actions or persists
prose-discovered blockers as real GitHub dependency links.

See skills/nextwork/SKILL.md for the full flag reference and skill loop.
"""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from collections import deque
from dataclasses import dataclass, field
from datetime import UTC, datetime
from typing import Any, Protocol

# --- Shared regex / link-parsing helpers (copied from skills/topissues/scripts/topissues.py) ---

PR_ISSUE_RE = re.compile(
    r"\b(?:closes|fixes|resolves|partial-fix)\s+#(\d+)\b",
    re.IGNORECASE,
)

REF_URL_RE = re.compile(
    r"^https?://github\.com/([^/\s]+/[^/\s]+)/(?:issues|pull)/(\d+)/?(?:[/?#].*)?$"
)
REF_REPO_HASH_RE = re.compile(r"^([^/#\s]+/[^/#\s]+)#(\d+)$")
REF_BARE_RE = re.compile(r"^#?(\d+)$")

# Control labels that indicate an issue is already on a known automation path.
ISSUE_CONTROL_LABELS = {
    "needs-info",
    "ready-to-code",
    "triaged",
    "duplicate",
    "blocked",
    "ready-for-triage",
    "question",
}

# Statuses whose next action is a single trivial gh mutation (assign or slash comment).
TRIVIAL_STATUSES = {"needs_assign", "needs_triage", "trigger_code", "trigger_review", "trigger_fix"}

SLASH_COMMAND_BY_STATUS = {
    "needs_triage": "/fs-triage",
    "trigger_code": "/fs-code",
    "trigger_review": "/fs-review",
    "trigger_fix": "/fs-fix",
}

BODY_TRUNCATE_CHARS = 1000
COMMENT_TRUNCATE_CHARS = 500
INCLUDE_TEXT_COMMENT_COUNT = 3

MAX_QUEUE_VISITS = 50


# ------------------------------- Ref parsing -------------------------------


class RefError(ValueError):
    """Raised when a CLI-supplied item reference cannot be parsed."""


def parse_ref(text: str, default_repo: str | None = None) -> tuple[str, int]:
    """Parse `owner/repo#N`, `#N`, `N`, or a GitHub issue/PR URL into (repo, number)."""
    text = text.strip()
    match = REF_URL_RE.match(text)
    if match:
        return match.group(1), int(match.group(2))
    match = REF_REPO_HASH_RE.match(text)
    if match:
        return match.group(1), int(match.group(2))
    match = REF_BARE_RE.match(text)
    if match:
        if not default_repo:
            raise RefError(f"cannot resolve bare ref {text!r} without --repo")
        return default_repo, int(match.group(1))
    raise RefError(f"cannot parse ref: {text!r}")


def format_ref(repo: str, number: int) -> str:
    return f"{repo}#{number}"


# ------------------------------- Time helpers -------------------------------


def parse_iso(value: str) -> datetime:
    return datetime.fromisoformat(value.replace("Z", "+00:00"))


def hours_since(iso_value: str, now: datetime) -> float:
    return (now - parse_iso(iso_value)).total_seconds() / 3600.0


def is_stale(iso_value: str, stale_hours: float, now: datetime) -> bool:
    return hours_since(iso_value, now) >= stale_hours


# ------------------------- Linked-PR helpers (from topissues) -------------------------


def parse_pr_links(body: str | None, closing_issue_numbers: list[int]) -> set[int]:
    """Collect issue numbers linked from a PR body and closing-issue refs."""
    linked = set(closing_issue_numbers)
    if body:
        for match in PR_ISSUE_RE.finditer(body):
            linked.add(int(match.group(1)))
    return linked


def build_pr_links_by_issue(pulls: list[dict[str, Any]]) -> dict[int, list[int]]:
    """Map issue number -> sorted list of open PR numbers that reference it."""
    by_issue: dict[int, set[int]] = {}
    for pr in pulls:
        pr_number = pr["number"]
        closing = [
            node["number"] for node in pr.get("closingIssuesReferences", {}).get("nodes", [])
        ]
        for issue_num in parse_pr_links(pr.get("body"), closing):
            by_issue.setdefault(issue_num, set()).add(pr_number)
    return {k: sorted(v) for k, v in by_issue.items()}


def parse_open_blockers(blocked_by: dict[str, Any] | None) -> list[dict[str, Any]]:
    """Return open issues/PRs that block this item (GitHub blockedBy links)."""
    blockers: list[dict[str, Any]] = []
    for node in (blocked_by or {}).get("nodes", []):
        if node.get("state") != "OPEN":
            continue
        repo = (node.get("repository") or {}).get("nameWithOwner", "")
        blockers.append({"repo": repo, "number": node["number"]})
    return blockers


# HTML markers from internal/statuscomment — durable signal that an agent run is live.
AGENT_STATUS_MARKER = "fullsend:agent-status:"
AGENT_TERMINAL_MARKER = "fullsend:status:terminal"

# Role word in the status body (e.g. "🤖 Review · Started …") → waiting status.
_INFLIGHT_ROLE_PATTERNS: tuple[tuple[re.Pattern[str], str], ...] = (
    (re.compile(r"\bReview\b", re.IGNORECASE), "waiting_review"),
    (re.compile(r"\bFix\b", re.IGNORECASE), "waiting_fix"),
    (re.compile(r"\bCode\b", re.IGNORECASE), "waiting_code"),
    (re.compile(r"\bTriage\b", re.IGNORECASE), "waiting_triage"),
)

_INFLIGHT_REASON = {
    "waiting_review": "Review agent run in progress (non-terminal status comment)",
    "waiting_fix": "Fix agent run in progress (non-terminal status comment)",
    "waiting_code": "Code agent run in progress (non-terminal status comment)",
    "waiting_triage": "Triage agent run in progress (non-terminal status comment)",
    "waiting_agent": "Agent run in progress (non-terminal status comment)",
}


def parse_inflight_agent(comments: list[dict[str, Any]]) -> str | None:
    """Return a waiting_* status if the latest agent-status comment is non-terminal.

    Convention (internal/statuscomment): in-flight comments contain
    ``fullsend:agent-status:<runID>`` without ``fullsend:status:terminal``.
    The chronologically latest agent-status comment wins — a Finished update
    on the same run replaces the Started body and adds the terminal tag.
    """
    agent_comments = [
        c for c in comments if AGENT_STATUS_MARKER in (c.get("body") or "")
    ]
    if not agent_comments:
        return None

    def sort_key(c: dict[str, Any]) -> str:
        return c.get("created_at") or ""

    latest = max(agent_comments, key=sort_key)
    body = latest.get("body") or ""
    if AGENT_TERMINAL_MARKER in body:
        return None
    for pattern, status in _INFLIGHT_ROLE_PATTERNS:
        if pattern.search(body):
            return status
    return "waiting_agent"


# ------------------------------- Classification -------------------------------


@dataclass
class Classification:
    status: str
    reason: str
    eliminated: bool
    blockers: list[dict[str, Any]] = field(default_factory=list)
    linked_prs: list[int] = field(default_factory=list)
    suggested_actions: list[str] = field(default_factory=list)


def classification_for_inflight(status: str) -> Classification:
    return Classification(
        status=status,
        reason=_INFLIGHT_REASON.get(status, _INFLIGHT_REASON["waiting_agent"]),
        eliminated=True,
    )


def classify_issue(
    item: dict[str, Any], user: str, stale_hours: float, now: datetime
) -> Classification | None:
    """Classify a normalized open issue. Returns None if it should be dropped entirely."""
    labels = set(item["labels"])
    assignees = item["assignees"]

    if "duplicate" in labels:
        return None

    if item["blockers"] or "blocked" in labels:
        return Classification(
            status="blocked_by",
            reason="Blocked by open issue(s)/PR(s)" if item["blockers"] else "Labeled blocked",
            eliminated=True,
            blockers=item["blockers"],
        )

    if assignees and user not in assignees:
        return Classification(
            status="assigned_elsewhere",
            reason=f"Assigned to {', '.join(sorted(assignees))}",
            eliminated=True,
        )

    inflight = parse_inflight_agent(item.get("comments") or [])
    if inflight:
        return classification_for_inflight(inflight)

    if item["linked_prs"]:
        refs = ", ".join(f"#{n}" for n in item["linked_prs"])
        return Classification(
            status="waiting_linked_pr",
            reason=f"Open linked PR(s): {refs}",
            eliminated=True,
            linked_prs=item["linked_prs"],
        )

    if "needs-info" in labels:
        if item["author"] == user:
            return Classification(
                status="needs_info_self",
                reason="Needs-info; you are the author",
                eliminated=False,
                suggested_actions=["Provide the requested information or edit the issue body"],
            )
        return Classification(
            status="waiting_info_other",
            reason="Needs-info; waiting on the reporter",
            eliminated=True,
        )

    has_control_label = bool(labels & ISSUE_CONTROL_LABELS)
    if "ready-for-triage" in labels or not has_control_label:
        if is_stale(item["created_at"], stale_hours, now):
            return Classification(
                status="needs_triage",
                reason="Stale triage wait; re-trigger",
                eliminated=False,
                suggested_actions=["comment:/fs-triage"],
            )
        return Classification(
            status="waiting_triage",
            reason="Waiting for triage automation",
            eliminated=True,
        )

    if "ready-to-code" in labels:
        if is_stale(item["updated_at"], stale_hours, now):
            return Classification(
                status="trigger_code",
                reason="Stale ready-to-code wait; re-trigger",
                eliminated=False,
                suggested_actions=["comment:/fs-code"],
            )
        return Classification(
            status="waiting_code",
            reason="Waiting for the code agent",
            eliminated=True,
        )

    if "triaged" in labels:
        return Classification(
            status="promote_code",
            reason="Triaged; needs a promotion decision (feature work)",
            eliminated=False,
            suggested_actions=[
                "decision: promote to ready-to-code, or comment:/fs-code once confirmed"
            ],
        )

    if not assignees:
        return Classification(
            status="needs_assign",
            reason="Unassigned; no automation signal",
            eliminated=False,
            suggested_actions=["assign:self"],
        )

    return Classification(
        status="human_work",
        reason="Assigned; no waiting/blocked signal",
        eliminated=False,
        suggested_actions=["Implement directly, or comment:/fs-code if eligible"],
    )


def _is_bot_author(login: str | None) -> bool:
    return bool(login) and login.endswith("[bot]")


def classify_pr(
    item: dict[str, Any], user: str, stale_hours: float, now: datetime
) -> Classification | None:
    """Classify a normalized open pull request. Returns None if it should be dropped."""
    labels = set(item["labels"])
    assignees = item["assignees"]

    if "blocked" in labels:
        return Classification(
            status="blocked_by",
            reason="Labeled blocked",
            eliminated=True,
            blockers=item.get("blockers", []),
        )

    if assignees and user not in assignees:
        return Classification(
            status="assigned_elsewhere",
            reason=f"Assigned to {', '.join(sorted(assignees))}",
            eliminated=True,
        )

    inflight = parse_inflight_agent(item.get("comments") or [])
    if inflight:
        return classification_for_inflight(inflight)

    if item.get("merge_state_status") == "DIRTY" or item.get("mergeable") == "CONFLICTING":
        return Classification(
            status="fix_conflicts",
            reason="Merge conflicts must be resolved",
            eliminated=False,
            suggested_actions=["Resolve merge conflicts"],
        )

    if "requires-manual-review" in labels or "needs-human" in labels:
        return Classification(
            status="needs_review_decision",
            reason="Requires a manual review decision",
            eliminated=False,
            suggested_actions=["Review and decide the next step"],
        )

    review_decision = item.get("review_decision")
    checks_pending = item.get("checks_state") in (
        "PENDING",
        "EXPECTED",
        "IN_PROGRESS",
        "QUEUED",
    )
    unresolved = int(item.get("unresolved_review_threads") or 0)
    merge_state = item.get("merge_state_status")
    merge_ready_states = {"CLEAN", "UNSTABLE"}

    if "ready-for-merge" in labels:
        if item.get("in_merge_queue"):
            return Classification(
                status="waiting_merge_queue",
                reason="Already enqueued in the merge queue",
                eliminated=True,
            )
        # Stale ready-for-merge must not win over pending checks or incomplete review.
        if checks_pending:
            return Classification(
                status="waiting_ci",
                reason="ready-for-merge label present but required checks are still running",
                eliminated=True,
            )
        if unresolved > 0:
            return Classification(
                status="needs_review_decision",
                reason=f"{unresolved} unresolved review conversation(s)",
                eliminated=False,
                suggested_actions=["Resolve or reply to open review threads"],
            )
        if review_decision in ("REVIEW_REQUIRED", "CHANGES_REQUESTED"):
            # Fall through to CHANGES_REQUESTED / waiting_review handling below.
            pass
        elif merge_state == "BLOCKED":
            return Classification(
                status="needs_review_decision",
                reason="Merge blocked by branch protection",
                eliminated=False,
                suggested_actions=["Satisfy branch protection (reviews, conversations, checks)"],
            )
        elif (
            merge_state in merge_ready_states
            and item.get("mergeable") != "CONFLICTING"
        ):
            return Classification(
                status="ready_to_merge",
                reason="Approved and ready to merge",
                eliminated=False,
                suggested_actions=["Merge, or enqueue in the merge queue"],
            )
        else:
            # UNKNOWN / DRAFT / BEHIND / missing state — never claim ready_to_merge.
            return Classification(
                status="needs_review_decision",
                reason=f"ready-for-merge label present but merge state is {merge_state or 'unknown'}",
                eliminated=False,
                suggested_actions=["Inspect PR merge readiness on GitHub"],
            )

    if review_decision == "CHANGES_REQUESTED":
        fix_eligible = "fullsend-no-fix" not in labels and (
            _is_bot_author(item.get("author")) or "fullsend-fix" in labels
        )
        if fix_eligible:
            if is_stale(item["updated_at"], stale_hours, now):
                return Classification(
                    status="trigger_fix",
                    reason="Stale changes-requested wait; re-trigger fix",
                    eliminated=False,
                    suggested_actions=["comment:/fs-fix"],
                )
            return Classification(
                status="waiting_fix",
                reason="Waiting for the fix agent",
                eliminated=True,
            )
        return Classification(
            status="human_work",
            reason="Changes requested; not fix-eligible (missing fullsend-fix label)",
            eliminated=False,
            suggested_actions=["Address review feedback directly, or add the fullsend-fix label"],
        )

    if checks_pending:
        return Classification(
            status="waiting_ci",
            reason="Required checks are still running",
            eliminated=True,
        )

    if item.get("is_draft"):
        return Classification(
            status="human_work",
            reason="Draft PR; mark ready for review when done",
            eliminated=False,
            suggested_actions=["Mark ready for review when complete"],
        )

    if "ready-for-review" in labels or review_decision in (None, "REVIEW_REQUIRED"):
        if is_stale(item["updated_at"], stale_hours, now):
            return Classification(
                status="trigger_review",
                reason="Stale review wait; re-trigger",
                eliminated=False,
                suggested_actions=["comment:/fs-review"],
            )
        return Classification(
            status="waiting_review",
            reason="Waiting for review",
            eliminated=True,
        )

    return Classification(
        status="human_work",
        reason="Open PR; no clear next action",
        eliminated=False,
        suggested_actions=["Investigate PR status manually"],
    )


def classify_item(
    item: dict[str, Any], user: str, stale_hours: float, now: datetime
) -> Classification | None:
    if item["kind"] == "issue":
        return classify_issue(item, user, stale_hours, now)
    return classify_pr(item, user, stale_hours, now)


# ------------------------------- gh CLI plumbing -------------------------------


def _gh_not_found() -> None:
    print("error: gh CLI not found; install https://cli.github.com/", file=sys.stderr)
    sys.exit(1)


def try_run_gh(args: list[str]) -> str | None:
    """Run gh and return stdout, or None if the command failed."""
    try:
        result = subprocess.run(["gh", *args], check=True, capture_output=True, text=True)
    except FileNotFoundError:
        _gh_not_found()
    except subprocess.CalledProcessError:
        return None
    return result.stdout.strip()


def run_gh(args: list[str], *, quiet: bool = False) -> str:
    try:
        result = subprocess.run(["gh", *args], check=True, capture_output=True, text=True)
    except FileNotFoundError:
        _gh_not_found()
    except subprocess.CalledProcessError as exc:
        if not quiet:
            if exc.stderr:
                print(exc.stderr.strip(), file=sys.stderr)
            if exc.stdout:
                print(exc.stdout.strip(), file=sys.stderr)
        sys.exit(3)
    return result.stdout.strip()


def graphql_var_flags(variables: dict[str, Any]) -> list[str]:
    """Build gh api graphql -f/-F flags. Int/bool/float must use -F (typed JSON)."""
    flags: list[str] = []
    for key, value in variables.items():
        if value is None:
            continue
        # -f always sends a string; GraphQL Int!/Boolean! reject coerced strings.
        if isinstance(value, bool):
            flags.extend(["-F", f"{key}={json.dumps(value)}"])
        elif isinstance(value, int) and not isinstance(value, bool):
            flags.extend(["-F", f"{key}={value}"])
        elif isinstance(value, float):
            flags.extend(["-F", f"{key}={value}"])
        else:
            flags.extend(["-f", f"{key}={value}"])
    return flags


def gh_graphql(query: str, variables: dict[str, Any], *, quiet: bool = False) -> dict[str, Any]:
    args = ["api", "graphql", "-f", f"query={query}", *graphql_var_flags(variables)]
    raw = run_gh(args, quiet=quiet)
    data = json.loads(raw)
    if data.get("errors"):
        if not quiet:
            print(json.dumps(data["errors"], indent=2), file=sys.stderr)
        sys.exit(3)
    return data["data"]


def gh_graphql_or_none(
    query: str, variables: dict[str, Any], *, quiet: bool = False
) -> dict[str, Any] | None:
    """Like gh_graphql, but returns None on failure instead of exiting."""
    args = ["api", "graphql", "-f", f"query={query}", *graphql_var_flags(variables)]
    try:
        result = subprocess.run(["gh", *args], check=True, capture_output=True, text=True)
    except FileNotFoundError:
        _gh_not_found()
    except subprocess.CalledProcessError as exc:
        if not quiet:
            err = (exc.stderr or exc.stdout or "").strip()
            if err:
                print(err, file=sys.stderr)
        return None
    try:
        data = json.loads(result.stdout)
    except json.JSONDecodeError:
        return None
    if data.get("errors"):
        if not quiet:
            print(json.dumps(data["errors"], indent=2), file=sys.stderr)
        return None
    return data.get("data")


def resolve_repo(override: str | None) -> str:
    if override:
        if "/" not in override or override.count("/") != 1:
            print(f"error: --repo must be owner/name, got: {override!r}", file=sys.stderr)
            sys.exit(2)
        return override
    raw = run_gh(["repo", "view", "--json", "nameWithOwner"])
    repo = json.loads(raw)["nameWithOwner"]
    if not repo:
        print(
            "error: not inside a git repository known to gh; use --repo owner/name",
            file=sys.stderr,
        )
        sys.exit(1)
    return repo


def resolve_user(override: str | None, *, quiet: bool = False) -> str:
    if override:
        return override
    return run_gh(["api", "user", "--jq", ".login"], quiet=quiet)


# ------------------------------- GraphQL queries -------------------------------

ITEM_QUERY = """
query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    issueOrPullRequest(number: $number) {
      __typename
      ... on Issue {
        number
        title
        url
        state
        author { login }
        assignees(first: 20) { nodes { login } }
        labels(first: 50) { nodes { name } }
        createdAt
        updatedAt
        body
        comments(last: 10) { nodes { author { login } body createdAt } }
        blockedBy(first: 20) {
          nodes { number state repository { nameWithOwner } }
        }
      }
      ... on PullRequest {
        number
        title
        url
        state
        isDraft
        author { login }
        assignees(first: 20) { nodes { login } }
        labels(first: 50) { nodes { name } }
        createdAt
        updatedAt
        body
        comments(last: 10) { nodes { author { login } body createdAt } }
        reviewDecision
        mergeable
        mergeStateStatus
        reviewThreads(first: 50) {
          nodes { isResolved }
        }
        commits(last: 1) {
          nodes { commit { statusCheckRollup { state } } }
        }
      }
    }
  }
}
"""

OPEN_PULLS_FOR_LINKING_QUERY = """
query($owner: String!, $name: String!, $cursor: String) {
  repository(owner: $owner, name: $name) {
    pullRequests(
      first: 100
      after: $cursor
      states: OPEN
      orderBy: {field: CREATED_AT, direction: DESC}
    ) {
      pageInfo { hasNextPage endCursor }
      nodes {
        number
        body
        closingIssuesReferences(first: 20) {
          nodes { number }
        }
      }
    }
  }
}
"""

MERGE_QUEUE_QUERY = """
query($owner: String!, $name: String!, $branch: String!) {
  repository(owner: $owner, name: $name) {
    mergeQueue(branch: $branch) {
      entries(first: 100) {
        nodes { pullRequest { number } }
      }
    }
  }
}
"""

DEFAULT_BRANCH_QUERY = """
query($owner: String!, $name: String!) {
  repository(owner: $owner, name: $name) {
    defaultBranchRef { name }
  }
}
"""

ISSUE_ID_AND_BLOCKERS_QUERY = """
query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    issue(number: $number) {
      id
      blockedBy(first: 50) {
        nodes { number repository { nameWithOwner } }
      }
    }
  }
}
"""

NODE_ID_QUERY = """
query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    issueOrPullRequest(number: $number) {
      __typename
      ... on Issue { id }
      ... on PullRequest { id }
    }
  }
}
"""

ISSUE_NODE_ID_QUERY = """
query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    issue(number: $number) { id }
  }
}
"""

ADD_BLOCKED_BY_MUTATION = """
mutation($issueId: ID!, $blockingIssueId: ID!) {
  addBlockedBy(input: {issueId: $issueId, blockingIssueId: $blockingIssueId}) {
    issue { number }
  }
}
"""


# ------------------------------- Fetch / normalize -------------------------------


def normalize_item(repo: str, node: dict[str, Any]) -> dict[str, Any]:
    """Turn a raw issueOrPullRequest GraphQL node into the internal item schema."""
    kind = "issue" if node["__typename"] == "Issue" else "pull"
    labels = [n["name"] for n in node.get("labels", {}).get("nodes", [])]
    assignees = [n["login"] for n in node.get("assignees", {}).get("nodes", [])]
    comments = [
        {
            "author": (c.get("author") or {}).get("login"),
            "body": (c.get("body") or "")[:COMMENT_TRUNCATE_CHARS],
            "created_at": c.get("createdAt"),
        }
        for c in node.get("comments", {}).get("nodes", [])
    ]
    item: dict[str, Any] = {
        "kind": kind,
        "repo": repo,
        "number": node["number"],
        "title": node["title"],
        "url": node["url"],
        "state": node["state"],
        "author": (node.get("author") or {}).get("login"),
        "assignees": assignees,
        "labels": labels,
        "created_at": node["createdAt"],
        "updated_at": node["updatedAt"],
        "body": node.get("body") or "",
        "comments": comments,
        "blockers": [],
        "linked_prs": [],
    }
    if kind == "issue":
        item["blockers"] = parse_open_blockers(node.get("blockedBy"))
    else:
        item["is_draft"] = node.get("isDraft", False)
        item["review_decision"] = node.get("reviewDecision")
        item["mergeable"] = node.get("mergeable")
        item["merge_state_status"] = node.get("mergeStateStatus")
        threads = (node.get("reviewThreads") or {}).get("nodes") or []
        item["unresolved_review_threads"] = sum(
            1 for t in threads if t.get("isResolved") is False
        )
        checks_state = None
        commit_nodes = node.get("commits", {}).get("nodes", [])
        if commit_nodes:
            rollup = commit_nodes[-1].get("commit", {}).get("statusCheckRollup")
            if rollup:
                checks_state = rollup.get("state")
        item["checks_state"] = checks_state
        item["in_merge_queue"] = False
    return item


class ItemFetcher(Protocol):
    """Structural interface build_queue depends on, so tests can stub it without gh."""

    def fetch_item(self, repo: str, number: int) -> dict[str, Any] | None: ...


class GhFetcher:
    """Fetches and caches item + linking data from gh GraphQL. Isolated for testability."""

    def __init__(self, *, quiet: bool = False):
        self.quiet = quiet
        self._pulls_by_repo: dict[str, list[dict[str, Any]]] = {}
        self._default_branch_by_repo: dict[str, str | None] = {}

    def fetch_item(self, repo: str, number: int) -> dict[str, Any] | None:
        owner, name = repo.split("/", 1)
        data = gh_graphql_or_none(
            ITEM_QUERY, {"owner": owner, "name": name, "number": number}, quiet=self.quiet
        )
        if data is None:
            return None
        node = (data.get("repository") or {}).get("issueOrPullRequest")
        if node is None:
            return None
        item = normalize_item(repo, node)
        if item["kind"] == "issue":
            item["linked_prs"] = self.get_linked_prs(repo, item["number"])
        return item

    def _pulls_for_linking(self, repo: str) -> list[dict[str, Any]]:
        if repo not in self._pulls_by_repo:
            owner, name = repo.split("/", 1)
            nodes: list[dict[str, Any]] = []
            cursor: str | None = None
            while True:
                data = gh_graphql_or_none(
                    OPEN_PULLS_FOR_LINKING_QUERY,
                    {"owner": owner, "name": name, "cursor": cursor},
                    quiet=self.quiet,
                )
                if data is None:
                    break
                conn = data["repository"]["pullRequests"]
                nodes.extend(conn["nodes"])
                page = conn["pageInfo"]
                if not page["hasNextPage"]:
                    break
                cursor = page["endCursor"]
            self._pulls_by_repo[repo] = nodes
        return self._pulls_by_repo[repo]

    def get_linked_prs(self, repo: str, issue_number: int) -> list[int]:
        by_issue = build_pr_links_by_issue(self._pulls_for_linking(repo))
        return by_issue.get(issue_number, [])

    def is_in_merge_queue(self, repo: str, number: int) -> bool:
        owner, name = repo.split("/", 1)
        if repo not in self._default_branch_by_repo:
            data = gh_graphql_or_none(
                DEFAULT_BRANCH_QUERY, {"owner": owner, "name": name}, quiet=self.quiet
            )
            branch = None
            if data is not None:
                ref = (data.get("repository") or {}).get("defaultBranchRef")
                branch = ref.get("name") if ref else None
            self._default_branch_by_repo[repo] = branch
        branch = self._default_branch_by_repo[repo]
        if not branch:
            return False
        data = gh_graphql_or_none(
            MERGE_QUEUE_QUERY, {"owner": owner, "name": name, "branch": branch}, quiet=self.quiet
        )
        if data is None:
            return False
        queue = (data.get("repository") or {}).get("mergeQueue")
        if not queue:
            return False
        entries = queue.get("entries", {}).get("nodes", [])
        return any((e.get("pullRequest") or {}).get("number") == number for e in entries)


# ------------------------------- Seeding + BFS -------------------------------


def seed_from_cli(items: list[str], default_repo: str | None) -> list[tuple[str, int]]:
    refs: list[tuple[str, int]] = []
    seen: set[tuple[str, int]] = set()
    for text in items:
        repo, number = parse_ref(text, default_repo)
        ref = (repo, number)
        if ref not in seen:
            seen.add(ref)
            refs.append(ref)
    return refs


def seed_from_assigned(repo: str, user: str, *, quiet: bool = False) -> list[tuple[str, int]]:
    refs: list[tuple[str, int]] = []
    # gh defaults --limit to 30; raise so "dozens" of assigned items are not truncated.
    issues_raw = try_run_gh(
        [
            "issue",
            "list",
            "--repo",
            repo,
            "--assignee",
            user,
            "--state",
            "open",
            "--limit",
            "1000",
            "--json",
            "number",
        ]
    )
    if issues_raw:
        for row in json.loads(issues_raw):
            refs.append((repo, row["number"]))
    pulls_raw = try_run_gh(
        [
            "pr",
            "list",
            "--repo",
            repo,
            "--assignee",
            user,
            "--state",
            "open",
            "--limit",
            "1000",
            "--json",
            "number",
        ]
    )
    if pulls_raw:
        for row in json.loads(pulls_raw):
            refs.append((repo, row["number"]))
    return refs


def build_queue(
    seeds: list[tuple[str, int]],
    fetcher: ItemFetcher,
    user: str,
    stale_hours: float,
    now: datetime,
    *,
    max_visits: int = MAX_QUEUE_VISITS,
) -> list[dict[str, Any]]:
    """BFS over the seed refs, following open blockedBy links. Returns classified items."""
    visited: set[tuple[str, int]] = set()
    to_visit: deque[tuple[str, int]] = deque(seeds)
    results: list[dict[str, Any]] = []

    while to_visit and len(visited) < max_visits:
        ref = to_visit.popleft()
        if ref in visited:
            continue
        visited.add(ref)
        repo, number = ref
        item = fetcher.fetch_item(repo, number)
        if item is None or item["state"] != "OPEN":
            continue

        classification = classify_item(item, user, stale_hours, now)
        if classification is None:
            continue

        result = dict(item)
        result["status"] = classification.status
        result["reason"] = classification.reason
        result["eliminated"] = classification.eliminated
        result["suggested_actions"] = classification.suggested_actions
        if classification.status == "assigned_elsewhere":
            result["assignees"] = item["assignees"]
        if classification.blockers:
            result["blockers"] = classification.blockers
        if classification.linked_prs:
            result["linked_prs"] = classification.linked_prs
        results.append(result)

        if classification.status == "blocked_by":
            for blocker in classification.blockers:
                bref = (blocker["repo"], blocker["number"])
                if bref not in visited:
                    to_visit.append(bref)

    return results


def maybe_check_merge_queue(items: list[dict[str, Any]], fetcher: GhFetcher) -> None:
    """Second pass: only hits the merge-queue API for PRs labeled ready-for-merge."""
    for item in items:
        if item["kind"] == "pull" and "ready-for-merge" in item.get("labels", []):
            item["in_merge_queue"] = fetcher.is_in_merge_queue(item["repo"], item["number"])


# ------------------------------- Apply / take-over / link-blocker -------------------------------


def apply_trivial_actions(
    items: list[dict[str, Any]], user: str, *, quiet: bool = False
) -> list[dict[str, Any]]:
    applied: list[dict[str, Any]] = []
    for item in items:
        if item.get("eliminated") or item.get("status") not in TRIVIAL_STATUSES:
            continue
        sub = "issue" if item["kind"] == "issue" else "pr"
        if item["status"] == "needs_assign":
            run_gh(
                [
                    "issue",
                    "edit",
                    str(item["number"]),
                    "--repo",
                    item["repo"],
                    "--add-assignee",
                    user,
                ],
                quiet=quiet,
            )
            action = "assign:self"
        else:
            command = SLASH_COMMAND_BY_STATUS[item["status"]]
            run_gh(
                [sub, "comment", str(item["number"]), "--repo", item["repo"], "--body", command],
                quiet=quiet,
            )
            action = f"comment:{command}"
        applied.append(
            {
                "kind": item["kind"],
                "repo": item["repo"],
                "number": item["number"],
                "status": item["status"],
                "action": action,
            }
        )
    return applied


def take_over(repo: str, number: int, user: str, *, quiet: bool = False) -> dict[str, Any]:
    owner, name = repo.split("/", 1)
    data = gh_graphql_or_none(
        NODE_ID_QUERY, {"owner": owner, "name": name, "number": number}, quiet=quiet
    )
    node = (data or {}).get("repository", {}).get("issueOrPullRequest") if data else None
    if node is None:
        return {"ref": format_ref(repo, number), "action": "error", "detail": "ref not found"}
    sub = "issue" if node["__typename"] == "Issue" else "pr"
    run_gh([sub, "edit", str(number), "--repo", repo, "--add-assignee", user], quiet=quiet)
    return {"ref": format_ref(repo, number), "action": "assigned", "detail": f"assigned to {user}"}


def link_blocker(
    dependent: tuple[str, int], blocker: tuple[str, int], *, quiet: bool = False
) -> dict[str, Any]:
    dep_repo, dep_number = dependent
    blk_repo, blk_number = blocker
    dep_owner, dep_name = dep_repo.split("/", 1)

    data = gh_graphql_or_none(
        ISSUE_ID_AND_BLOCKERS_QUERY,
        {"owner": dep_owner, "name": dep_name, "number": dep_number},
        quiet=quiet,
    )
    issue = (data or {}).get("repository", {}).get("issue") if data else None
    if issue is None:
        return {
            "dependent": format_ref(dep_repo, dep_number),
            "blocker": format_ref(blk_repo, blk_number),
            "action": "error",
            "detail": "dependent ref is not an open Issue (GitHub blocked-by is issue-only)",
        }

    existing = {
        (n["repository"]["nameWithOwner"], n["number"]) for n in issue["blockedBy"]["nodes"]
    }
    if (blk_repo, blk_number) in existing:
        return {
            "dependent": format_ref(dep_repo, dep_number),
            "blocker": format_ref(blk_repo, blk_number),
            "action": "already_linked",
            "detail": "blockedBy link already exists",
        }

    blk_owner, blk_name = blk_repo.split("/", 1)
    # addBlockedBy requires Issue IDs on both sides — do not use issueOrPullRequest here.
    blocker_data = gh_graphql_or_none(
        ISSUE_NODE_ID_QUERY,
        {"owner": blk_owner, "name": blk_name, "number": blk_number},
        quiet=quiet,
    )
    blocker_issue = (
        (blocker_data or {}).get("repository", {}).get("issue") if blocker_data else None
    )
    if blocker_issue is None or not blocker_issue.get("id"):
        return {
            "dependent": format_ref(dep_repo, dep_number),
            "blocker": format_ref(blk_repo, blk_number),
            "action": "error",
            "detail": "blocker ref is not an Issue (GitHub blocked-by is issue-only)",
        }

    gh_graphql(
        ADD_BLOCKED_BY_MUTATION,
        {"issueId": issue["id"], "blockingIssueId": blocker_issue["id"]},
        quiet=quiet,
    )
    return {
        "dependent": format_ref(dep_repo, dep_number),
        "blocker": format_ref(blk_repo, blk_number),
        "action": "linked",
        "detail": "created blockedBy link",
    }


def parse_link_blocker_spec(spec: str) -> tuple[str, str]:
    if "=" not in spec:
        raise RefError(f"--link-blocker must be DEPENDENT=BLOCKER, got: {spec!r}")
    dependent, blocker = spec.split("=", 1)
    dependent, blocker = dependent.strip(), blocker.strip()
    if not dependent or not blocker:
        raise RefError(f"--link-blocker must be DEPENDENT=BLOCKER, got: {spec!r}")
    return dependent, blocker


def parse_take_over_specs(specs: list[str]) -> list[str]:
    refs: list[str] = []
    for spec in specs:
        for part in spec.split(","):
            part = part.strip()
            if part:
                refs.append(part)
    return refs


# ------------------------------- Output formatting -------------------------------

DECISION_STATUSES = {
    "needs_info_self",
    "promote_code",
    "needs_review_decision",
    "ready_to_merge",
    "fix_conflicts",
    "human_work",
}

WAITING_PREFIX = "waiting_"


def item_output_dict(item: dict[str, Any], *, include_text: bool) -> dict[str, Any]:
    out = {
        "kind": item["kind"],
        "repo": item["repo"],
        "number": item["number"],
        "title": item["title"],
        "url": item["url"],
        "status": item["status"],
        "eliminated": item["eliminated"],
        "reason": item["reason"],
        "assignees": item.get("assignees", []) if item["status"] == "assigned_elsewhere" else [],
        "blockers": item.get("blockers", []),
        "suggested_actions": item.get("suggested_actions", []),
    }
    if item.get("linked_prs"):
        out["linked_prs"] = item["linked_prs"]
    if include_text:
        out["body"] = item.get("body", "")[:BODY_TRUNCATE_CHARS]
        out["comments"] = item.get("comments", [])[-INCLUDE_TEXT_COMMENT_COUNT:]
    return out


def format_json_output(
    items: list[dict[str, Any]],
    repo: str,
    user: str,
    stale_hours: float,
    applied: list[dict[str, Any]],
    *,
    include_text: bool,
    link_results: list[dict[str, Any]] | None = None,
    take_over_results: list[dict[str, Any]] | None = None,
) -> str:
    payload: dict[str, Any] = {
        "repo": repo,
        "user": user,
        "generated_at": datetime.now(UTC).isoformat(),
        "stale_hours": stale_hours,
        "items": [item_output_dict(i, include_text=include_text) for i in items],
        "applied": applied,
    }
    if link_results:
        payload["link_results"] = link_results
    if take_over_results:
        payload["take_over_results"] = take_over_results
    return json.dumps(payload, indent=2)


def _format_item_line(item: dict[str, Any]) -> str:
    link = f"[{item['kind']}#{item['number']}]({item['url']})"
    title = item["title"].replace("|", "\\|")
    return f"- {link} {title} — _{item['status']}_: {item['reason']}"


def format_markdown_output(
    items: list[dict[str, Any]],
    repo: str,
    user: str,
    stale_hours: float,
    applied: list[dict[str, Any]],
    *,
    show_blocked: bool,
) -> str:
    do_now = [i for i in items if not i["eliminated"]]
    waiting = [i for i in items if i["eliminated"] and i["status"].startswith(WAITING_PREFIX)]
    blocked = [i for i in items if i["eliminated"] and i["status"] == "blocked_by"]
    elsewhere = [i for i in items if i["eliminated"] and i["status"] == "assigned_elsewhere"]

    lines = ["## Do now", ""]
    if do_now:
        lines.extend(_format_item_line(i) for i in do_now)
    else:
        lines.append("_Nothing actionable right now._")
    lines.append("")

    if show_blocked:
        for title, group in (
            ("Waiting", waiting),
            ("Blocked", blocked),
            ("Assigned elsewhere", elsewhere),
        ):
            lines.append(f"## {title}")
            lines.append("")
            if group:
                lines.extend(_format_item_line(i) for i in group)
            else:
                lines.append("_None._")
            lines.append("")

    if applied:
        lines.append("## Applied")
        lines.append("")
        for action in applied:
            lines.append(
                f"- {action['kind']}#{action['number']} ({action['repo']}): {action['action']}"
            )
        lines.append("")

    ts = datetime.now(UTC).strftime("%Y-%m-%d %H:%M UTC")
    lines.append(
        f"_Generated {ts} · {repo} · user {user} · stale-hours {stale_hours:g} · "
        f"{len(do_now)} actionable, {len(items) - len(do_now)} waiting/blocked/elsewhere_"
    )
    return "\n".join(lines)


# ------------------------------- CLI -------------------------------


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description=(
            "Build a readiness-oriented queue of open issues/PRs: assigned work, "
            "GitHub blockedBy links (BFS, cross-repo), and recommended next actions."
        ),
    )
    parser.add_argument(
        "items",
        nargs="*",
        metavar="ITEMS",
        help="Seed refs: owner/repo#N, #N, N (needs --repo), or a GitHub issue/PR URL",
    )
    parser.add_argument("--repo", help="Repository as owner/name (default: current repo)")
    parser.add_argument("--user", help="GitHub login (default: authenticated user)")
    parser.add_argument(
        "--format", choices=("markdown", "json"), default="markdown", help="Output format"
    )
    parser.add_argument(
        "--show-blocked",
        action="store_true",
        help="Include waiting/blocked/assigned-elsewhere details in markdown output",
    )
    parser.add_argument(
        "--apply",
        action="store_true",
        help="Perform trivial actions: assign unassigned items to self; post /fs-* comments",
    )
    parser.add_argument(
        "--take-over",
        action="append",
        default=[],
        metavar="REFS",
        help="Assign listed refs (comma-separated or repeatable) to --user, then classify normally",
    )
    parser.add_argument(
        "--link-blocker",
        action="append",
        default=[],
        metavar="DEPENDENT=BLOCKER",
        help="Persist a real GitHub blockedBy link (repeatable). Idempotent if already linked.",
    )
    parser.add_argument(
        "--decisions-only",
        action="store_true",
        help="Filter output to non-trivial decisions only (hides waiting/blocked/trivial items)",
    )
    parser.add_argument(
        "--stale-hours",
        type=float,
        default=6,
        metavar="N",
        help="Hours after which a waiting-on-automation item becomes actionable (default: 6)",
    )
    parser.add_argument("--quiet", action="store_true", help="Suppress stderr on API failures")
    parser.add_argument(
        "--include-text",
        action="store_true",
        help="Include truncated body + last comments in JSON output (for prose-dependency mining)",
    )
    args = parser.parse_args(argv)
    if args.stale_hours < 0:
        print("error: --stale-hours must be non-negative", file=sys.stderr)
        sys.exit(2)
    return args


def main(argv: list[str] | None = None) -> None:
    args = parse_args(argv)
    repo = resolve_repo(args.repo)
    user = resolve_user(args.user, quiet=args.quiet)
    now = datetime.now(UTC)

    link_results: list[dict[str, Any]] = []
    for spec in args.link_blocker:
        try:
            dep_text, blk_text = parse_link_blocker_spec(spec)
            dependent = parse_ref(dep_text, repo)
            blocker = parse_ref(blk_text, repo)
        except RefError as exc:
            print(f"error: {exc}", file=sys.stderr)
            sys.exit(2)
        link_results.append(link_blocker(dependent, blocker, quiet=args.quiet))

    take_over_results: list[dict[str, Any]] = []
    for ref_text in parse_take_over_specs(args.take_over):
        try:
            take_repo, take_number = parse_ref(ref_text, repo)
        except RefError as exc:
            print(f"error: {exc}", file=sys.stderr)
            sys.exit(2)
        take_over_results.append(take_over(take_repo, take_number, user, quiet=args.quiet))

    try:
        if args.items:
            seeds = seed_from_cli(args.items, repo)
        else:
            seeds = seed_from_assigned(repo, user, quiet=args.quiet)
    except RefError as exc:
        print(f"error: {exc}", file=sys.stderr)
        sys.exit(2)

    fetcher = GhFetcher(quiet=args.quiet)
    items = build_queue(seeds, fetcher, user, args.stale_hours, now)
    maybe_check_merge_queue(items, fetcher)
    # Merge-queue membership can change ready_to_merge -> waiting_merge_queue; reclassify.
    for item in items:
        if item["kind"] == "pull" and "ready-for-merge" in item.get("labels", []):
            classification = classify_pr(item, user, args.stale_hours, now)
            if classification is not None:
                item["status"] = classification.status
                item["reason"] = classification.reason
                item["eliminated"] = classification.eliminated
                item["suggested_actions"] = classification.suggested_actions

    applied: list[dict[str, Any]] = []
    if args.apply:
        applied = apply_trivial_actions(items, user, quiet=args.quiet)
        applied_refs = {(a["repo"], a["number"]) for a in applied}
        for item in items:
            if (item["repo"], item["number"]) in applied_refs:
                item["reason"] = f"{item['reason']} (applied)"

    if args.decisions_only:
        items = [i for i in items if i["status"] in DECISION_STATUSES]

    if args.format == "json":
        print(
            format_json_output(
                items,
                repo,
                user,
                args.stale_hours,
                applied,
                include_text=args.include_text,
                link_results=link_results,
                take_over_results=take_over_results,
            )
        )
    else:
        print(
            format_markdown_output(
                items, repo, user, args.stale_hours, applied, show_blocked=args.show_blocked
            )
        )


if __name__ == "__main__":
    main()
