# Profile import is exercised whenever a harness declares providers, triggering
# the providers-v2 code path in fullsend run (ImportProfiles).  With
# GODOG_CONCURRENCY > 1 the suite runs these scenarios alongside others on the
# same runner, so concurrent sandbox starts exercise the flock serialization
# added in #6448 against the real openshell gateway.
Feature: Profile import under concurrent sandbox starts

  Background:
    Given the enrolled test repository

  Scenario: Provider-backed harness imports profiles successfully
    Given a custom harness "profile-import-a" with:
      """
      agent: agents/triage.md
      role: triage
      slug: fullsend-ai-profile-import-a
      model: opus
      image: ghcr.io/fullsend-ai/fullsend-sandbox:latest
      providers:
        - github
      trigger: |
        event.entity.kind == "work_item"
        && event.transition.kind == "label_changed"
        && event.transition.label.name == "ready-for-profile-import-a"
      """
    And a dummy agent that would:
      | description      | op            | args                                                             |
      | Emit triage JSON | write_fixture | output/agent-result.json, fixtures/triage/sufficient.json        |
    And an issue
    When the issue is labeled "ready-for-profile-import-a"
    Then the harness "profile-import-a" workflow completes successfully
    And the agent will succeed to Emit triage JSON

  Scenario: Second concurrent provider-backed harness imports profiles
    Given a custom harness "profile-import-b" with:
      """
      agent: agents/triage.md
      role: triage
      slug: fullsend-ai-profile-import-b
      model: opus
      image: ghcr.io/fullsend-ai/fullsend-sandbox:latest
      providers:
        - github
      trigger: |
        event.entity.kind == "work_item"
        && event.transition.kind == "label_changed"
        && event.transition.label.name == "ready-for-profile-import-b"
      """
    And a dummy agent that would:
      | description      | op            | args                                                             |
      | Emit triage JSON | write_fixture | output/agent-result.json, fixtures/triage/sufficient.json        |
    And an issue
    When the issue is labeled "ready-for-profile-import-b"
    Then the harness "profile-import-b" workflow completes successfully
    And the agent will succeed to Emit triage JSON
