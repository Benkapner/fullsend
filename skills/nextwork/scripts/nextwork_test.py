#!/usr/bin/env python3
"""Unit tests for nextwork.py (no network)."""

from __future__ import annotations

import json
import os
import sys
import unittest
from datetime import UTC, datetime
from unittest.mock import patch

sys.path.insert(0, os.path.dirname(__file__))

from nextwork import (  # noqa: E402
    DECISION_STATUSES,
    RefError,
    apply_trivial_actions,
    build_pr_links_by_issue,
    build_queue,
    classify_issue,
    classify_pr,
    format_json_output,
    format_markdown_output,
    graphql_var_flags,
    hours_since,
    is_stale,
    link_blocker,
    normalize_item,
    parse_inflight_agent,
    parse_link_blocker_spec,
    parse_open_blockers,
    parse_pr_links,
    parse_ref,
    parse_take_over_specs,
    take_over,
)

NOW = datetime(2024, 1, 10, tzinfo=UTC)  # 8 days after 2024-01-02T00:00:00Z


class TestGraphqlVarFlags(unittest.TestCase):
    def test_int_uses_typed_flag(self):
        self.assertEqual(
            graphql_var_flags({"owner": "acme", "number": 5479}),
            ["-f", "owner=acme", "-F", "number=5479"],
        )

    def test_bool_uses_typed_flag(self):
        self.assertEqual(
            graphql_var_flags({"draft": True}),
            ["-F", "draft=true"],
        )

    def test_skips_none(self):
        self.assertEqual(graphql_var_flags({"cursor": None, "owner": "acme"}), ["-f", "owner=acme"])


def load_fixture(name: str):
    path = os.path.join(os.path.dirname(__file__), "testdata", name)
    with open(path, encoding="utf-8") as f:
        return json.load(f)


def make_issue(**overrides):
    item = {
        "kind": "issue",
        "repo": "acme/widget",
        "number": 1,
        "title": "An issue",
        "url": "https://github.com/acme/widget/issues/1",
        "state": "OPEN",
        "author": "alice",
        "assignees": [],
        "labels": [],
        "created_at": "2024-01-09T00:00:00Z",
        "updated_at": "2024-01-09T00:00:00Z",
        "body": "",
        "comments": [],
        "blockers": [],
        "linked_prs": [],
    }
    item.update(overrides)
    return item


def make_pr(**overrides):
    item = {
        "kind": "pull",
        "repo": "acme/widget",
        "number": 99,
        "title": "A pull request",
        "url": "https://github.com/acme/widget/pull/99",
        "state": "OPEN",
        "author": "alice",
        "assignees": [],
        "labels": [],
        "created_at": "2024-01-09T00:00:00Z",
        "updated_at": "2024-01-09T00:00:00Z",
        "body": "",
        "comments": [],
        "blockers": [],
        "linked_prs": [],
        "is_draft": False,
        "review_decision": None,
        "mergeable": "MERGEABLE",
        "merge_state_status": "CLEAN",
        "unresolved_review_threads": 0,
        "checks_state": "SUCCESS",
        "in_merge_queue": False,
    }
    item.update(overrides)
    return item


class TestParseInflightAgent(unittest.TestCase):
    def test_no_comments(self):
        self.assertIsNone(parse_inflight_agent([]))

    def test_started_only_is_inflight(self):
        comments = [
            {
                "body": "<!-- fullsend:agent-status:run-1 -->\n🤖 Review · Started 1:00 PM UTC",
                "created_at": "2024-01-09T13:00:00Z",
            }
        ]
        self.assertEqual(parse_inflight_agent(comments), "waiting_review")

    def test_terminal_is_not_inflight(self):
        comments = [
            {
                "body": (
                    "<!-- fullsend:agent-status:run-1 -->\n"
                    "<!-- fullsend:status:terminal -->\n"
                    "🤖 Finished Review · ✅ Success · Started 1:00 PM UTC · Completed 1:10 PM UTC"
                ),
                "created_at": "2024-01-09T13:10:00Z",
            }
        ]
        self.assertIsNone(parse_inflight_agent(comments))

    def test_latest_terminal_wins_over_older_started(self):
        comments = [
            {
                "body": "<!-- fullsend:agent-status:run-1 -->\n🤖 Review · Started 1:00 PM UTC",
                "created_at": "2024-01-09T13:00:00Z",
            },
            {
                "body": (
                    "<!-- fullsend:agent-status:run-1 -->\n"
                    "<!-- fullsend:status:terminal -->\n"
                    "🤖 Finished Review · ✅ Success"
                ),
                "created_at": "2024-01-09T13:10:00Z",
            },
        ]
        self.assertIsNone(parse_inflight_agent(comments))

    def test_latest_started_wins_over_older_terminal(self):
        comments = [
            {
                "body": (
                    "<!-- fullsend:agent-status:run-1 -->\n"
                    "<!-- fullsend:status:terminal -->\n"
                    "🤖 Finished Fix · ✅ Success"
                ),
                "created_at": "2024-01-09T12:00:00Z",
            },
            {
                "body": "<!-- fullsend:agent-status:run-2 -->\n🤖 Review · Started 1:00 PM UTC",
                "created_at": "2024-01-09T13:00:00Z",
            },
        ]
        self.assertEqual(parse_inflight_agent(comments), "waiting_review")

    def test_role_mapping(self):
        cases = [
            ("🤖 Fix · Started", "waiting_fix"),
            ("🤖 Code · Started", "waiting_code"),
            ("🤖 Triage · Started", "waiting_triage"),
            ("🤖 Working · Started", "waiting_agent"),
        ]
        for body_role, expected in cases:
            with self.subTest(body_role=body_role):
                comments = [
                    {
                        "body": f"<!-- fullsend:agent-status:x -->\n{body_role}",
                        "created_at": "2024-01-09T13:00:00Z",
                    }
                ]
                self.assertEqual(parse_inflight_agent(comments), expected)

    def test_issue_inflight_code_before_ready_to_code(self):
        item = make_issue(
            labels=["ready-to-code"],
            assignees=["alice"],
            comments=[
                {
                    "body": "<!-- fullsend:agent-status:r1 -->\n🤖 Code · Started 1:00 PM UTC",
                    "created_at": "2024-01-09T13:00:00Z",
                }
            ],
        )
        result = classify_issue(item, "alice", 6, NOW)
        self.assertEqual(result.status, "waiting_code")
        self.assertTrue(result.eliminated)


class TestParseRef(unittest.TestCase):
    def test_repo_hash_number(self):
        self.assertEqual(parse_ref("acme/widget#42"), ("acme/widget", 42))

    def test_bare_hash_with_default_repo(self):
        self.assertEqual(parse_ref("#42", "acme/widget"), ("acme/widget", 42))

    def test_bare_number_with_default_repo(self):
        self.assertEqual(parse_ref("42", "acme/widget"), ("acme/widget", 42))

    def test_bare_number_without_default_repo_raises(self):
        with self.assertRaises(RefError):
            parse_ref("42", None)

    def test_issue_url(self):
        self.assertEqual(parse_ref("https://github.com/acme/widget/issues/42"), ("acme/widget", 42))

    def test_pull_url_with_trailing_slash(self):
        self.assertEqual(parse_ref("https://github.com/acme/widget/pull/42/"), ("acme/widget", 42))

    def test_pull_url_with_query_suffix(self):
        self.assertEqual(
            parse_ref("https://github.com/acme/widget/pull/42/files"), ("acme/widget", 42)
        )

    def test_garbage_raises(self):
        with self.assertRaises(RefError):
            parse_ref("not-a-ref")


class TestTimeHelpers(unittest.TestCase):
    def test_hours_since(self):
        now = datetime(2024, 1, 2, 6, tzinfo=UTC)
        self.assertAlmostEqual(hours_since("2024-01-02T00:00:00Z", now), 6.0)

    def test_is_stale_true(self):
        now = datetime(2024, 1, 2, 6, tzinfo=UTC)
        self.assertTrue(is_stale("2024-01-02T00:00:00Z", 6, now))

    def test_is_stale_false(self):
        now = datetime(2024, 1, 2, 5, tzinfo=UTC)
        self.assertFalse(is_stale("2024-01-02T00:00:00Z", 6, now))


class TestParsePrLinks(unittest.TestCase):
    def test_body_keywords_and_closing_refs(self):
        body = "This closes #42 and partial-fix #43"
        self.assertEqual(parse_pr_links(body, [44]), {42, 43, 44})

    def test_ignores_bare_hash_mentions(self):
        self.assertEqual(parse_pr_links("See also #99 for context", []), set())


class TestBuildPrLinksByIssue(unittest.TestCase):
    def test_fixture_pulls(self):
        pulls = load_fixture("pulls_for_linking_sample.json")
        by_issue = build_pr_links_by_issue(pulls)
        self.assertEqual(by_issue[100], [10])
        self.assertEqual(by_issue[200], [10])
        self.assertEqual(by_issue[999], [11])  # "fixes #999" keyword match, no closing ref


class TestParseOpenBlockers(unittest.TestCase):
    def test_open_blocker_kept_closed_dropped(self):
        blocked_by = {
            "nodes": [
                {"number": 7, "state": "OPEN", "repository": {"nameWithOwner": "acme/widget"}},
                {"number": 8, "state": "CLOSED", "repository": {"nameWithOwner": "acme/widget"}},
            ]
        }
        self.assertEqual(parse_open_blockers(blocked_by), [{"repo": "acme/widget", "number": 7}])

    def test_empty(self):
        self.assertEqual(parse_open_blockers(None), [])


class TestNormalizeItem(unittest.TestCase):
    def test_issue_node(self):
        node = load_fixture("issue_node_sample.json")
        item = normalize_item("acme/widget", node)
        self.assertEqual(item["kind"], "issue")
        self.assertEqual(item["number"], 42)
        self.assertEqual(item["assignees"], ["alice"])
        self.assertEqual(item["labels"], ["ready-to-code"])
        self.assertEqual(item["blockers"], [{"repo": "acme/widget", "number": 7}])
        self.assertEqual(len(item["comments"]), 1)

    def test_pull_node(self):
        node = load_fixture("pr_node_sample.json")
        item = normalize_item("acme/widget", node)
        self.assertEqual(item["kind"], "pull")
        self.assertEqual(item["review_decision"], "REVIEW_REQUIRED")
        self.assertEqual(item["mergeable"], "MERGEABLE")
        self.assertEqual(item["merge_state_status"], "CLEAN")
        self.assertEqual(item["unresolved_review_threads"], 1)
        self.assertEqual(item["checks_state"], "SUCCESS")
        self.assertFalse(item["is_draft"])
        self.assertFalse(item["in_merge_queue"])
        self.assertEqual(item["blockers"], [])


class TestClassifyIssue(unittest.TestCase):
    def test_duplicate_drops(self):
        item = make_issue(labels=["duplicate"])
        self.assertIsNone(classify_issue(item, "alice", 6, NOW))

    def test_blocked_by_structured_blocker(self):
        item = make_issue(blockers=[{"repo": "acme/widget", "number": 7}])
        result = classify_issue(item, "alice", 6, NOW)
        self.assertEqual(result.status, "blocked_by")
        self.assertTrue(result.eliminated)
        self.assertEqual(result.blockers, [{"repo": "acme/widget", "number": 7}])

    def test_blocked_by_label_only(self):
        item = make_issue(labels=["blocked"])
        result = classify_issue(item, "alice", 6, NOW)
        self.assertEqual(result.status, "blocked_by")
        self.assertEqual(result.blockers, [])

    def test_assigned_elsewhere(self):
        item = make_issue(assignees=["bob"])
        result = classify_issue(item, "alice", 6, NOW)
        self.assertEqual(result.status, "assigned_elsewhere")
        self.assertTrue(result.eliminated)

    def test_waiting_linked_pr(self):
        item = make_issue(linked_prs=[10, 11])
        result = classify_issue(item, "alice", 6, NOW)
        self.assertEqual(result.status, "waiting_linked_pr")
        self.assertEqual(result.linked_prs, [10, 11])

    def test_needs_info_self(self):
        item = make_issue(author="alice", labels=["needs-info"])
        result = classify_issue(item, "alice", 6, NOW)
        self.assertEqual(result.status, "needs_info_self")
        self.assertFalse(result.eliminated)

    def test_waiting_info_other(self):
        item = make_issue(author="bob", labels=["needs-info"])
        result = classify_issue(item, "alice", 6, NOW)
        self.assertEqual(result.status, "waiting_info_other")
        self.assertTrue(result.eliminated)

    def test_waiting_triage_no_labels_recent(self):
        item = make_issue(created_at="2024-01-09T23:00:00Z")  # 1 hour before NOW
        result = classify_issue(item, "alice", 6, NOW)
        self.assertEqual(result.status, "waiting_triage")
        self.assertTrue(result.eliminated)

    def test_needs_triage_no_labels_stale(self):
        item = make_issue(created_at="2024-01-01T00:00:00Z")  # long before NOW
        result = classify_issue(item, "alice", 6, NOW)
        self.assertEqual(result.status, "needs_triage")
        self.assertFalse(result.eliminated)
        self.assertIn("comment:/fs-triage", result.suggested_actions)

    def test_waiting_triage_label_takes_priority_over_other_control_labels(self):
        # ready-for-triage forces the triage-wait branch even alongside another control label,
        # as long as the wait itself isn't stale.
        item = make_issue(labels=["ready-for-triage", "triaged"], created_at="2024-01-09T23:00:00Z")
        result = classify_issue(item, "alice", 6, NOW)
        self.assertEqual(result.status, "waiting_triage")

    def test_needs_triage_label_stale(self):
        item = make_issue(labels=["ready-for-triage"], created_at="2024-01-01T00:00:00Z")
        result = classify_issue(item, "alice", 6, NOW)
        self.assertEqual(result.status, "needs_triage")
        self.assertFalse(result.eliminated)

    def test_waiting_code(self):
        item = make_issue(labels=["ready-to-code"], updated_at="2024-01-09T23:00:00Z")
        result = classify_issue(item, "alice", 6, NOW)
        self.assertEqual(result.status, "waiting_code")
        self.assertTrue(result.eliminated)

    def test_trigger_code_stale(self):
        item = make_issue(labels=["ready-to-code"], updated_at="2024-01-01T00:00:00Z")
        result = classify_issue(item, "alice", 6, NOW)
        self.assertEqual(result.status, "trigger_code")
        self.assertFalse(result.eliminated)
        self.assertIn("comment:/fs-code", result.suggested_actions)

    def test_promote_code(self):
        item = make_issue(labels=["triaged"])
        result = classify_issue(item, "alice", 6, NOW)
        self.assertEqual(result.status, "promote_code")
        self.assertFalse(result.eliminated)

    def test_needs_assign_unassigned_with_control_label(self):
        item = make_issue(labels=["triaged"], assignees=[])
        result = classify_issue(item, "alice", 6, NOW)
        # triaged is checked before the unassigned fallback
        self.assertEqual(result.status, "promote_code")

    def test_needs_assign_no_signal(self):
        item = make_issue(labels=["question"], assignees=[])
        result = classify_issue(item, "alice", 6, NOW)
        self.assertEqual(result.status, "needs_assign")
        self.assertIn("assign:self", result.suggested_actions)

    def test_human_work_assigned_no_signal(self):
        item = make_issue(labels=["question"], assignees=["alice"])
        result = classify_issue(item, "alice", 6, NOW)
        self.assertEqual(result.status, "human_work")
        self.assertFalse(result.eliminated)


class TestClassifyPr(unittest.TestCase):
    def test_blocked_by_label(self):
        item = make_pr(labels=["blocked"])
        result = classify_pr(item, "alice", 6, NOW)
        self.assertEqual(result.status, "blocked_by")

    def test_assigned_elsewhere(self):
        item = make_pr(assignees=["bob"])
        result = classify_pr(item, "alice", 6, NOW)
        self.assertEqual(result.status, "assigned_elsewhere")

    def test_fix_conflicts(self):
        item = make_pr(merge_state_status="DIRTY")
        result = classify_pr(item, "alice", 6, NOW)
        self.assertEqual(result.status, "fix_conflicts")
        self.assertFalse(result.eliminated)

    def test_fix_conflicts_via_mergeable_conflicting(self):
        # GraphQL sometimes returns UNKNOWN mergeStateStatus until mergeable is computed.
        item = make_pr(merge_state_status="UNKNOWN", mergeable="CONFLICTING")
        result = classify_pr(item, "alice", 6, NOW)
        self.assertEqual(result.status, "fix_conflicts")

    def test_needs_review_decision_manual_review(self):
        item = make_pr(labels=["requires-manual-review"])
        result = classify_pr(item, "alice", 6, NOW)
        self.assertEqual(result.status, "needs_review_decision")

    def test_needs_review_decision_needs_human(self):
        item = make_pr(labels=["needs-human"])
        result = classify_pr(item, "alice", 6, NOW)
        self.assertEqual(result.status, "needs_review_decision")

    def test_ready_to_merge(self):
        item = make_pr(
            labels=["ready-for-merge"],
            in_merge_queue=False,
            merge_state_status="CLEAN",
            mergeable="MERGEABLE",
            unresolved_review_threads=0,
        )
        result = classify_pr(item, "alice", 6, NOW)
        self.assertEqual(result.status, "ready_to_merge")
        self.assertFalse(result.eliminated)

    def test_ready_for_merge_with_unresolved_threads(self):
        # Mirrors #5328: ready-for-merge label but open review conversations.
        item = make_pr(
            labels=["ready-for-merge"],
            review_decision=None,
            merge_state_status="BLOCKED",
            mergeable="MERGEABLE",
            unresolved_review_threads=2,
        )
        result = classify_pr(item, "alice", 6, NOW)
        self.assertEqual(result.status, "needs_review_decision")
        self.assertIn("unresolved", result.reason)

    def test_ready_for_merge_blocked_without_threads(self):
        item = make_pr(
            labels=["ready-for-merge"],
            review_decision=None,
            merge_state_status="BLOCKED",
            mergeable="MERGEABLE",
            unresolved_review_threads=0,
        )
        result = classify_pr(item, "alice", 6, NOW)
        self.assertEqual(result.status, "needs_review_decision")
        self.assertIn("branch protection", result.reason)

    def test_ready_for_merge_unknown_merge_state_not_ready(self):
        item = make_pr(
            labels=["ready-for-merge"],
            merge_state_status="UNKNOWN",
            mergeable="MERGEABLE",
            unresolved_review_threads=0,
        )
        result = classify_pr(item, "alice", 6, NOW)
        self.assertNotEqual(result.status, "ready_to_merge")
        self.assertEqual(result.status, "needs_review_decision")

    def test_ready_for_merge_with_pending_checks_is_waiting_ci(self):
        item = make_pr(
            labels=["ready-for-merge"],
            checks_state="PENDING",
            review_decision="APPROVED",
        )
        result = classify_pr(item, "alice", 6, NOW)
        self.assertEqual(result.status, "waiting_ci")
        self.assertTrue(result.eliminated)

    def test_ready_for_merge_with_review_required_is_waiting_review(self):
        item = make_pr(
            labels=["ready-for-merge", "ready-for-review"],
            review_decision="REVIEW_REQUIRED",
            updated_at="2024-01-09T23:00:00Z",
        )
        result = classify_pr(item, "alice", 6, NOW)
        self.assertEqual(result.status, "waiting_review")
        self.assertTrue(result.eliminated)

    def test_ready_for_merge_with_inflight_review_comment(self):
        # Mirrors #5488: stale ready-for-merge while review agent has Started.
        item = make_pr(
            labels=["ready-for-merge", "ready-for-review"],
            review_decision="REVIEW_REQUIRED",
            comments=[
                {
                    "author": "fullsend-ai-review[bot]",
                    "body": (
                        "<!-- fullsend:agent-status:30004814427 -->\n"
                        "🤖 Review · Started 11:54 AM UTC\n"
                        "Commit: `7ada4e0`"
                    ),
                    "created_at": "2024-01-09T23:54:00Z",
                }
            ],
        )
        result = classify_pr(item, "alice", 6, NOW)
        self.assertEqual(result.status, "waiting_review")
        self.assertTrue(result.eliminated)

    def test_waiting_merge_queue(self):
        item = make_pr(labels=["ready-for-merge"], in_merge_queue=True)
        result = classify_pr(item, "alice", 6, NOW)
        self.assertEqual(result.status, "waiting_merge_queue")
        self.assertTrue(result.eliminated)

    def test_waiting_fix_bot_author(self):
        item = make_pr(
            author="fullsend-ai-coder[bot]",
            review_decision="CHANGES_REQUESTED",
            updated_at="2024-01-09T23:00:00Z",
        )
        result = classify_pr(item, "alice", 6, NOW)
        self.assertEqual(result.status, "waiting_fix")
        self.assertTrue(result.eliminated)

    def test_trigger_fix_stale_bot_author(self):
        item = make_pr(
            author="fullsend-ai-coder[bot]",
            review_decision="CHANGES_REQUESTED",
            updated_at="2024-01-01T00:00:00Z",
        )
        result = classify_pr(item, "alice", 6, NOW)
        self.assertEqual(result.status, "trigger_fix")
        self.assertFalse(result.eliminated)

    def test_waiting_fix_human_with_fullsend_fix_label(self):
        item = make_pr(
            author="carol",
            labels=["fullsend-fix"],
            review_decision="CHANGES_REQUESTED",
            updated_at="2024-01-09T23:00:00Z",
        )
        result = classify_pr(item, "alice", 6, NOW)
        self.assertEqual(result.status, "waiting_fix")

    def test_human_work_changes_requested_not_fix_eligible(self):
        item = make_pr(author="carol", review_decision="CHANGES_REQUESTED")
        result = classify_pr(item, "alice", 6, NOW)
        self.assertEqual(result.status, "human_work")
        self.assertFalse(result.eliminated)

    def test_fullsend_no_fix_overrides_bot_author(self):
        item = make_pr(
            author="fullsend-ai-coder[bot]",
            labels=["fullsend-no-fix"],
            review_decision="CHANGES_REQUESTED",
        )
        result = classify_pr(item, "alice", 6, NOW)
        self.assertEqual(result.status, "human_work")

    def test_waiting_ci(self):
        item = make_pr(checks_state="PENDING")
        result = classify_pr(item, "alice", 6, NOW)
        self.assertEqual(result.status, "waiting_ci")
        self.assertTrue(result.eliminated)

    def test_draft_pr(self):
        item = make_pr(is_draft=True)
        result = classify_pr(item, "alice", 6, NOW)
        self.assertEqual(result.status, "human_work")
        self.assertFalse(result.eliminated)

    def test_waiting_review(self):
        item = make_pr(labels=["ready-for-review"], updated_at="2024-01-09T23:00:00Z")
        result = classify_pr(item, "alice", 6, NOW)
        self.assertEqual(result.status, "waiting_review")
        self.assertTrue(result.eliminated)

    def test_trigger_review_stale(self):
        item = make_pr(labels=["ready-for-review"], updated_at="2024-01-01T00:00:00Z")
        result = classify_pr(item, "alice", 6, NOW)
        self.assertEqual(result.status, "trigger_review")
        self.assertFalse(result.eliminated)

    def test_fallback_human_work(self):
        item = make_pr(review_decision="APPROVED", checks_state="SUCCESS")
        result = classify_pr(item, "alice", 6, NOW)
        self.assertEqual(result.status, "human_work")
        self.assertFalse(result.eliminated)


class FakeFetcher:
    """Stub GhFetcher for build_queue tests: no gh subprocess calls."""

    def __init__(self, items_by_ref):
        self._items_by_ref = items_by_ref

    def fetch_item(self, repo, number):
        item = self._items_by_ref.get((repo, number))
        return dict(item) if item else None


class TestBuildQueue(unittest.TestCase):
    def test_bfs_follows_open_blockers_cross_repo(self):
        items = {
            ("acme/widget", 1): make_issue(
                repo="acme/widget",
                number=1,
                blockers=[{"repo": "acme/other", "number": 2}],
            ),
            ("acme/other", 2): make_issue(repo="acme/other", number=2, labels=["question"]),
        }
        fetcher = FakeFetcher(items)
        results = build_queue([("acme/widget", 1)], fetcher, "alice", 6, NOW)
        numbers = {(r["repo"], r["number"]) for r in results}
        self.assertEqual(numbers, {("acme/widget", 1), ("acme/other", 2)})
        first = next(r for r in results if r["number"] == 1)
        self.assertEqual(first["status"], "blocked_by")

    def test_does_not_revisit(self):
        items = {("acme/widget", 1): make_issue(repo="acme/widget", number=1)}
        fetcher = FakeFetcher(items)
        results = build_queue([("acme/widget", 1), ("acme/widget", 1)], fetcher, "alice", 6, NOW)
        self.assertEqual(len(results), 1)

    def test_drops_closed_items(self):
        items = {("acme/widget", 1): make_issue(repo="acme/widget", number=1, state="CLOSED")}
        fetcher = FakeFetcher(items)
        results = build_queue([("acme/widget", 1)], fetcher, "alice", 6, NOW)
        self.assertEqual(results, [])

    def test_drops_duplicate_labeled(self):
        items = {("acme/widget", 1): make_issue(repo="acme/widget", number=1, labels=["duplicate"])}
        fetcher = FakeFetcher(items)
        results = build_queue([("acme/widget", 1)], fetcher, "alice", 6, NOW)
        self.assertEqual(results, [])

    def test_respects_max_visits_cap(self):
        # Build a long blocker chain: 1 <- 2 <- 3 <- ... to verify the visit cap stops BFS.
        items = {}
        for n in range(1, 10):
            blockers = [{"repo": "acme/widget", "number": n + 1}] if n < 9 else []
            items[("acme/widget", n)] = make_issue(repo="acme/widget", number=n, blockers=blockers)
        fetcher = FakeFetcher(items)
        results = build_queue([("acme/widget", 1)], fetcher, "alice", 6, NOW, max_visits=3)
        self.assertEqual(len(results), 3)


class TestApplyTrivialActions(unittest.TestCase):
    @patch("nextwork.run_gh")
    def test_assigns_unassigned(self, mock_run_gh):
        items = [
            {
                "kind": "issue",
                "repo": "acme/widget",
                "number": 1,
                "status": "needs_assign",
                "eliminated": False,
            }
        ]
        applied = apply_trivial_actions(items, "alice")
        self.assertEqual(len(applied), 1)
        self.assertEqual(applied[0]["action"], "assign:self")
        mock_run_gh.assert_called_once_with(
            ["issue", "edit", "1", "--repo", "acme/widget", "--add-assignee", "alice"],
            quiet=False,
        )

    @patch("nextwork.run_gh")
    def test_posts_slash_command_for_pr(self, mock_run_gh):
        items = [
            {
                "kind": "pull",
                "repo": "acme/widget",
                "number": 99,
                "status": "trigger_review",
                "eliminated": False,
            }
        ]
        applied = apply_trivial_actions(items, "alice")
        self.assertEqual(applied[0]["action"], "comment:/fs-review")
        mock_run_gh.assert_called_once_with(
            ["pr", "comment", "99", "--repo", "acme/widget", "--body", "/fs-review"],
            quiet=False,
        )

    @patch("nextwork.run_gh")
    def test_skips_eliminated_and_non_trivial(self, mock_run_gh):
        items = [
            {
                "kind": "issue",
                "repo": "a/b",
                "number": 1,
                "status": "waiting_code",
                "eliminated": True,
            },
            {
                "kind": "issue",
                "repo": "a/b",
                "number": 2,
                "status": "human_work",
                "eliminated": False,
            },
        ]
        applied = apply_trivial_actions(items, "alice")
        self.assertEqual(applied, [])
        mock_run_gh.assert_not_called()


class TestTakeOver(unittest.TestCase):
    @patch("nextwork.run_gh")
    @patch("nextwork.gh_graphql_or_none")
    def test_assigns_issue(self, mock_gql, mock_run_gh):
        mock_gql.return_value = {
            "repository": {"issueOrPullRequest": {"__typename": "Issue", "id": "I_1"}}
        }
        result = take_over("acme/widget", 1, "alice")
        self.assertEqual(result["action"], "assigned")
        mock_run_gh.assert_called_once_with(
            ["issue", "edit", "1", "--repo", "acme/widget", "--add-assignee", "alice"],
            quiet=False,
        )

    @patch("nextwork.run_gh")
    @patch("nextwork.gh_graphql_or_none")
    def test_assigns_pull_request(self, mock_gql, mock_run_gh):
        mock_gql.return_value = {
            "repository": {"issueOrPullRequest": {"__typename": "PullRequest", "id": "PR_1"}}
        }
        result = take_over("acme/widget", 99, "alice")
        self.assertEqual(result["action"], "assigned")
        mock_run_gh.assert_called_once_with(
            ["pr", "edit", "99", "--repo", "acme/widget", "--add-assignee", "alice"],
            quiet=False,
        )

    @patch("nextwork.gh_graphql_or_none", return_value=None)
    def test_ref_not_found(self, _mock_gql):
        result = take_over("acme/widget", 404, "alice")
        self.assertEqual(result["action"], "error")


class TestLinkBlocker(unittest.TestCase):
    @patch("nextwork.gh_graphql")
    @patch("nextwork.gh_graphql_or_none")
    def test_creates_new_link(self, mock_gql, mock_mutation):
        mock_gql.side_effect = [
            {"repository": {"issue": {"id": "I_DEP", "blockedBy": {"nodes": []}}}},
            {"repository": {"issue": {"id": "I_BLK"}}},
        ]
        result = link_blocker(("acme/widget", 1), ("acme/widget", 2))
        self.assertEqual(result["action"], "linked")
        mock_mutation.assert_called_once()
        _args, kwargs = mock_mutation.call_args
        self.assertEqual(_args[1], {"issueId": "I_DEP", "blockingIssueId": "I_BLK"})

    @patch("nextwork.gh_graphql")
    @patch("nextwork.gh_graphql_or_none")
    def test_already_linked_is_idempotent(self, mock_gql, mock_mutation):
        mock_gql.return_value = {
            "repository": {
                "issue": {
                    "id": "I_DEP",
                    "blockedBy": {
                        "nodes": [{"number": 2, "repository": {"nameWithOwner": "acme/widget"}}]
                    },
                }
            }
        }
        result = link_blocker(("acme/widget", 1), ("acme/widget", 2))
        self.assertEqual(result["action"], "already_linked")
        mock_mutation.assert_not_called()

    @patch("nextwork.gh_graphql")
    @patch("nextwork.gh_graphql_or_none", return_value={"repository": {"issue": None}})
    def test_dependent_not_an_issue_errors(self, _mock_gql, mock_mutation):
        result = link_blocker(("acme/widget", 99), ("acme/widget", 2))
        self.assertEqual(result["action"], "error")
        mock_mutation.assert_not_called()

    @patch("nextwork.gh_graphql")
    @patch("nextwork.gh_graphql_or_none")
    def test_blocker_not_an_issue_errors(self, mock_gql, mock_mutation):
        mock_gql.side_effect = [
            {"repository": {"issue": {"id": "I_DEP", "blockedBy": {"nodes": []}}}},
            {"repository": {"issue": None}},
        ]
        result = link_blocker(("acme/widget", 1), ("acme/widget", 404))
        self.assertEqual(result["action"], "error")
        self.assertIn("issue-only", result["detail"])
        mock_mutation.assert_not_called()


class TestSpecParsing(unittest.TestCase):
    def test_parse_link_blocker_spec(self):
        self.assertEqual(
            parse_link_blocker_spec("acme/widget#1=acme/widget#2"),
            ("acme/widget#1", "acme/widget#2"),
        )

    def test_parse_link_blocker_spec_missing_equals(self):
        with self.assertRaises(RefError):
            parse_link_blocker_spec("acme/widget#1")

    def test_parse_take_over_specs_comma_and_repeat(self):
        self.assertEqual(
            parse_take_over_specs(["a/b#1,a/b#2", "a/b#3"]),
            ["a/b#1", "a/b#2", "a/b#3"],
        )


class TestFormatOutputs(unittest.TestCase):
    def setUp(self):
        self.items = [
            {
                "kind": "issue",
                "repo": "acme/widget",
                "number": 1,
                "title": "Actionable | item",
                "url": "https://github.com/acme/widget/issues/1",
                "status": "needs_assign",
                "eliminated": False,
                "reason": "Unassigned",
                "suggested_actions": ["assign:self"],
                "blockers": [],
            },
            {
                "kind": "issue",
                "repo": "acme/widget",
                "number": 2,
                "title": "Blocked item",
                "url": "https://github.com/acme/widget/issues/2",
                "status": "blocked_by",
                "eliminated": True,
                "reason": "Blocked by open issue(s)/PR(s)",
                "suggested_actions": [],
                "blockers": [{"repo": "acme/widget", "number": 3}],
            },
        ]

    def test_json_output_shape(self):
        out = format_json_output(self.items, "acme/widget", "alice", 6, [], include_text=False)
        payload = json.loads(out)
        self.assertEqual(payload["repo"], "acme/widget")
        self.assertEqual(len(payload["items"]), 2)
        self.assertEqual(payload["items"][0]["status"], "needs_assign")
        self.assertNotIn("body", payload["items"][0])

    def test_json_output_include_text(self):
        item = dict(self.items[0], body="hello world", comments=[{"author": "bob", "body": "hi"}])
        out = format_json_output([item], "acme/widget", "alice", 6, [], include_text=True)
        payload = json.loads(out)
        self.assertIn("body", payload["items"][0])
        self.assertIn("comments", payload["items"][0])

    def test_markdown_hides_blocked_by_default(self):
        out = format_markdown_output(self.items, "acme/widget", "alice", 6, [], show_blocked=False)
        self.assertIn("Actionable", out)
        self.assertNotIn("## Blocked", out)

    def test_markdown_shows_blocked_when_requested(self):
        out = format_markdown_output(self.items, "acme/widget", "alice", 6, [], show_blocked=True)
        self.assertIn("## Blocked", out)
        self.assertIn("Blocked item", out)

    def test_decision_statuses_disjoint_from_trivial(self):
        from nextwork import TRIVIAL_STATUSES

        self.assertEqual(DECISION_STATUSES & TRIVIAL_STATUSES, set())


if __name__ == "__main__":
    unittest.main()
