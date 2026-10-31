# File-level skill overrides and the persona manifest (#6158). A harness
# `skills:` entry in map form names files inside the skill: null removes a
# file from this run's copy, a URL replaces (or adds) one. The runner copies
# the skill into a per-run directory, applies the overrides there, and
# writes the persona manifest (.fullsend/personas.json in the sandbox
# workspace, FULLSEND_PERSONAS_FILE) that every runtime reads. The dummy
# runtime checks that manifest from inside the sandbox, so this needs no
# inference.
#
# The skill is the shipped pr-review skill from fullsend-ai/agents, pinned at
# commit 4a6645268c6c8063c15ce5cb1b88a4f3930c74da. The directory hash is the
# runner's tree hash (sha256 over the sorted "path:sha256(content)" lines of
# every file under skills/pr-review, nested github/ and gitlab/ included);
# the file hash is sha256 of correctness.md at that commit. Re-pin both
# together. security.md is replaced by upstream correctness.md, so its staged
# bytes, and therefore its manifest sha256, must equal correctness.md's.
#
# Skill directories are fetched by github.com tree URL, which the install's
# default allowlist (raw.githubusercontent.com) does not cover, so the
# repository allowlist gains the github.com prefix the harness declares.
Feature: Skill file overrides stage a per-run skill copy and a persona manifest

  Scenario: a URL skill with a removed and a replaced sub-agent records both in the persona manifest
    Given the enrolled test repository
    And the repository allows remote resources from "https://github.com/fullsend-ai/agents/"
    And a custom harness "skill-overrides" with:
      """
      agent: agents/skill-overrides.md
      role: triage
      slug: fullsend-ai-skill-overrides
      model: opus
      image: ghcr.io/fullsend-ai/fullsend-sandbox:latest
      policy: policies/base.yaml
      trigger: |
        event.entity.kind == "work_item"
        && event.transition.kind == "label_changed"
        && event.transition.label.name == "ready-for-skill-overrides"
      allowed_remote_resources:
        - https://github.com/fullsend-ai/agents/
        - https://raw.githubusercontent.com/fullsend-ai/agents/
      skills:
        - https://github.com/fullsend-ai/agents/tree/4a6645268c6c8063c15ce5cb1b88a4f3930c74da/skills/pr-review#sha256=d0a639e6870c3e204db83855cdf1e4e10e6eb3ecfaee1695bbf8630843cb5c6c:
            sub-agents/user-experience.md: null
            sub-agents/security.md: https://raw.githubusercontent.com/fullsend-ai/agents/4a6645268c6c8063c15ce5cb1b88a4f3930c74da/skills/pr-review/sub-agents/correctness.md#sha256=aa6bcb51be7ed37131de404af76ec152edf07842ae03f74045a59c9346c15ec2
      """
    And a dummy agent that would:
      | description                                   | op            | args                                                                                                                                                                                                                         |
      | See user-experience.md recorded as removed    | assert_jq     | .fullsend/personas.json, any(.removed[]; .skill == "pr-review" and .path == "sub-agents/user-experience.md")                                                                                                                |
      | See no persona staged from user-experience.md | assert_jq     | .fullsend/personas.json, all(.personas[]; .path != "sub-agents/user-experience.md")                                                                                                                                         |
      | See security.md staged from the override      | assert_jq     | .fullsend/personas.json, any(.personas[]; .skill == "pr-review" and .path == "sub-agents/security.md" and .origin == "override")                                                                                            |
      | See correctness.md staged from upstream       | assert_jq     | .fullsend/personas.json, any(.personas[]; .skill == "pr-review" and .path == "sub-agents/correctness.md" and .origin == "upstream")                                                                                         |
      | See security.md carry correctness.md's bytes  | assert_jq     | .fullsend/personas.json, [.personas[] \| select(.skill == "pr-review" and (.path == "sub-agents/security.md" or .path == "sub-agents/correctness.md")) \| .sha256] \| length == 2 and .[0] == .[1] and (.[0] \| test("^[0-9a-f]{64}$")) |
      | Emit result                                   | write_fixture | output/agent-result.json, fixtures/dispatch/ok.json                                                                                                                                                                          |
    And an issue
    When the issue is labeled "ready-for-skill-overrides"
    Then the harness "skill-overrides" workflow completes successfully
    And the agent will succeed to See user-experience.md recorded as removed
    And the agent will succeed to See no persona staged from user-experience.md
    And the agent will succeed to See security.md staged from the override
    And the agent will succeed to See correctness.md staged from upstream
    And the agent will succeed to See security.md carry correctness.md's bytes
    And the agent will succeed to Emit result
