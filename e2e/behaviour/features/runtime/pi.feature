# Runtime-specific coverage for pi (#6464). The rest of the suite runs every
# stage under the dummy runtime, which proves dispatch, harness loading and
# the sandbox boundary but never exercises a real runtime's Bootstrap/Run,
# the sandbox hook adapter, the Vertex credential path, or the stream
# parser. This scenario does, on the same leased per-repo install, by
# selecting pi for one run.
#
# Gated on the `runtime-pi` capability: the harness pins
# fullsend-sandbox:latest, which only carries the pinned pi CLI once the
# image change is published from main. Enable with
# BEHAVIOUR_CAPABILITIES=runtime-pi (costs one real model run on Vertex).
Feature: pi runtime runs a fleet agent unattended

  @requires:capability:runtime-pi
  Scenario: pi triage run selects the runtime, calls tools through the hook adapter, and reports metrics
    Given the enrolled test repository
    And the repository runtime is "pi"
    And a custom harness "pi-smoke" with:
      """
      agent: agents/triage.md
      role: triage
      slug: fullsend-ai-pi-smoke
      model: haiku
      image: ghcr.io/fullsend-ai/fullsend-sandbox:latest
      trigger: |
        event.entity.kind == "work_item"
        && event.transition.kind == "label_changed"
        && event.transition.label.name == "ready-for-pi-smoke"
      """
    And an issue
    When the issue is labeled "ready-for-pi-smoke"
    Then the harness "pi-smoke" workflow completes successfully
    And the run selected the "pi" runtime
    And the pi session transcript records at least one tool call
    And the run metrics report tokens
