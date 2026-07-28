@requires:jira-mock
Feature: Jira poll dispatch

  The Jira poll input driver discovers events on Jira issues and produces
  dispatch records that trigger harness agent workflows. These scenarios
  run the real `fullsend poll --input-driver jira-poll` binary against a
  local mock Jira server, then assert on the dispatch records written to
  the output file.

  Unlike the GitHub dispatch scenarios, this does not exercise workflow
  dispatch or artifact collection — it validates the poll-to-dispatch-
  record path in isolation. End-to-end workflow triggering requires a
  real GitHub Actions environment and is covered separately.

  Background:
    Given the enrolled test repository
    And a mock Jira server

  Scenario: Jira comment with slash command produces triage dispatch
    Given a custom harness "jira-triage" with:
      """
      agent: agents/triage.md
      role: triage
      slug: fullsend-ai-jira-triage
      model: opus
      image: ghcr.io/fullsend-ai/fullsend-sandbox:latest
      trigger: |
        event.source.system == "jira"
        && event.entity.kind == "work_item"
        && event.transition.kind == "comment_added"
        && event.transition.comment.command == "/fs-triage"
      """
    And a Jira issue "PROJ-101" with labels "bug"
    When a comment "/fs-triage check acceptance criteria" is added to Jira issue "PROJ-101"
    And the Jira poller runs
    Then the dispatch output contains a "triage" stage for issue "PROJ-101"

  Scenario: Jira label change produces dispatch for label-triggered harness
    Given a custom harness "jira-code" with:
      """
      agent: agents/code.md
      role: code
      slug: fullsend-ai-jira-code
      model: opus
      image: ghcr.io/fullsend-ai/fullsend-sandbox:latest
      trigger: |
        event.source.system == "jira"
        && event.entity.kind == "work_item"
        && event.transition.kind == "label_changed"
        && event.transition.label.name == "ready-to-code"
      """
    And a Jira issue "PROJ-202" with labels "ready-to-code"
    When the label "ready-to-code" is added to Jira issue "PROJ-202"
    And the Jira poller runs
    Then the dispatch output contains a "code" stage for issue "PROJ-202"
    And the dispatch output does not contain a stage for issue "PROJ-999"
