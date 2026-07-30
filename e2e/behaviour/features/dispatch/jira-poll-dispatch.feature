Feature: Jira poll dispatch

  The Jira poll input driver discovers events on Jira issues and produces
  dispatch records that trigger harness agent workflows. These scenarios
  run the real poller against a local mock Jira server, then assert on
  the dispatch records written to the output file.

  Unlike the GitHub dispatch scenarios, this does not exercise workflow
  dispatch or artifact collection — it validates the poll-to-dispatch-
  record path in isolation.

  Background:
    Given a mock Jira server

  Scenario: Jira comment with slash command produces triage dispatch
    Given a Jira issue "PROJ-101" with labels "bug"
    When a comment "/fs-triage check acceptance criteria" is added to Jira issue "PROJ-101"
    And the Jira poller runs
    Then the dispatch output contains a "triage" stage for issue "PROJ-101"

  Scenario: Jira label change produces dispatch for label-triggered harness
    Given a Jira issue "PROJ-202" with labels "backlog"
    When the label "ready-to-code" is added to Jira issue "PROJ-202"
    And the Jira poller runs
    Then the dispatch output contains a "code" stage for issue "PROJ-202"
    And the dispatch output does not contain a stage for issue "PROJ-999"
