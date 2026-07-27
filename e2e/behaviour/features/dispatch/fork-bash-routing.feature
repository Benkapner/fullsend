Feature: Fork PR bash routing smoke

  Verify that a fork pull request dispatches through the bash routing
  path (reusable-dispatch.yml route job) on a per-repo installation.
  This is a time-boxed guard until harness CEL cutover (#2902).

  Background:
    Given the enrolled test repository
    And a fork "test-repo-fork" of the enrolled test repository

  Scenario: Fork PR labeled ready-for-review dispatches review via bash routing
    Given a dummy agent that would:
      | description          | op            | args                                                       |
      | Prove bash routing   | write_fixture | output/bash-routing-ok.json, fixtures/dispatch/ok.json     |
    When a fork pull request is opened
    And the fork pull request is labeled "ready-for-review"
    Then the fullsend workflow dispatches the review stage
    And the agent will succeed to Prove bash routing
