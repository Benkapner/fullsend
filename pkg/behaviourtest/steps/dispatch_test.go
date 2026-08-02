package steps

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fullsend-ai/fullsend/internal/forge"
	"github.com/fullsend-ai/fullsend/pkg/behaviourtest/world"
)

func TestGivenCustomHarness_Validation(t *testing.T) {
	w := &world.World{}
	require.Error(t, givenCustomHarness(w, "", "doc"))
	require.Error(t, givenCustomHarness(w, "agent", ""))
}

func TestDispatchSteps_RequireScenarioStart(t *testing.T) {
	w := &world.World{}
	require.Error(t, thenHarnessWorkflowCompletes(w, "agent"))
	require.Error(t, thenHarnessAgentDidNotRun(w, "agent"))
}

func TestDispatchSteps_RequirePullRequest(t *testing.T) {
	w := &world.World{ScenarioStart: time.Now()}
	require.Error(t, whenPullRequestLabeled(w, "label"))
	require.Error(t, whenPullRequestReviewComment(w))
}

func TestEnsureHarnessArtifacts_NoWorkflowRun(t *testing.T) {
	w := &world.World{ScenarioStart: time.Now()}
	err := ensureHarnessArtifacts(w, "agent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "workflow run")
}

func TestNegativeSettleDuration(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		world    *world.World
		now      time.Time
		wantZero bool          // true if settle should be skipped
		wantDur  time.Duration // exact expected duration (checked when wantZero is false)
	}{
		{
			name: "WorkflowRun set — skip settle",
			world: &world.World{
				ScenarioStart: now.Add(-30 * time.Second),
				WorkflowRun:   &forge.WorkflowRun{ID: 1},
			},
			now:      now,
			wantZero: true,
		},
		{
			name: "standalone negative — full settle",
			world: &world.World{
				ScenarioStart: now,
			},
			now:     now,
			wantDur: defaultSettleDuration,
		},
		{
			name:    "ScenarioStart zero — full settle (safety)",
			world:   &world.World{},
			now:     now,
			wantDur: defaultSettleDuration,
		},
		{
			name: "partial elapsed — remaining settle",
			world: &world.World{
				ScenarioStart: now.Add(-60 * time.Second),
			},
			now:     now,
			wantDur: 30 * time.Second,
		},
		{
			name: "elapsed >= settle budget — skip settle",
			world: &world.World{
				ScenarioStart: now.Add(-90 * time.Second),
			},
			now:      now,
			wantZero: true,
		},
		{
			name: "elapsed > settle budget — skip settle",
			world: &world.World{
				ScenarioStart: now.Add(-120 * time.Second),
			},
			now:      now,
			wantZero: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := negativeSettleDuration(tc.world, tc.now)
			if tc.wantZero {
				assert.Equal(t, time.Duration(0), got)
			} else {
				assert.Equal(t, tc.wantDur, got)
			}
		})
	}
}

func TestGivenCustomHarness_CommitsAgentFile(t *testing.T) {
	scm := &fakeURLSCM{files: map[string][]byte{
		"test-org/test-repo/.fullsend/config.yaml": []byte("version: \"1\"\nagents: []\n"),
	}}
	w := &world.World{
		Install: &fakeURLInstall{owner: "test-org", repo: "test-repo"},
		SCM:     scm,
	}
	err := givenCustomHarness(w, "local-test", "agent: agents/triage.md\nrole: triage\nslug: local-test")
	require.NoError(t, err)

	// The agent resource should be committed under .fullsend/ on the config repo.
	agentData := scm.files["test-org/test-repo/.fullsend/agents/triage.md"]
	require.NotNil(t, agentData, "agent resource should be committed to config repo")
	assert.Equal(t, minimalAgentContent, string(agentData))

	// The harness YAML should also be committed.
	harnessData := scm.files["test-org/test-repo/.fullsend/harness/local-test.yaml"]
	require.NotNil(t, harnessData, "harness YAML should be committed")
}

func TestGivenCustomHarness_CommitsAgentAndPolicy(t *testing.T) {
	scm := &fakeURLSCM{files: map[string][]byte{
		"test-org/test-repo/.fullsend/config.yaml": []byte("version: \"1\"\nagents: []\n"),
	}}
	w := &world.World{
		Install: &fakeURLInstall{owner: "test-org", repo: "test-repo"},
		SCM:     scm,
	}
	err := givenCustomHarness(w, "local-test", "agent: agents/test.md\npolicy: policies/test.md\nrole: triage\nslug: local-test")
	require.NoError(t, err)

	agentData := scm.files["test-org/test-repo/.fullsend/agents/test.md"]
	require.NotNil(t, agentData, "agent resource should be committed")
	assert.Equal(t, minimalAgentContent, string(agentData))

	policyData := scm.files["test-org/test-repo/.fullsend/policies/test.md"]
	require.NotNil(t, policyData, "policy resource should be committed")
	assert.Contains(t, string(policyData), "Minimal policy")
}

func TestGivenCustomHarness_SkipsAbsoluteAgentPath(t *testing.T) {
	scm := &fakeURLSCM{files: map[string][]byte{
		"test-org/test-repo/.fullsend/config.yaml": []byte("version: \"1\"\nagents: []\n"),
	}}
	w := &world.World{
		Install: &fakeURLInstall{owner: "test-org", repo: "test-repo"},
		SCM:     scm,
	}
	err := givenCustomHarness(w, "local-test", "agent: https://example.com/agent.md\nrole: triage\nslug: local-test")
	require.NoError(t, err)

	// No extra agent file should be committed — only harness YAML and config.
	for key := range scm.files {
		assert.NotContains(t, key, "agent.md", "URL agent paths should not be committed as files")
	}
}

func TestGivenDisabledCustomHarness_CommitsAgentFile(t *testing.T) {
	scm := &fakeURLSCM{files: map[string][]byte{
		"test-org/test-repo/.fullsend/config.yaml": []byte("version: \"1\"\nagents: []\n"),
	}}
	w := &world.World{
		Install: &fakeURLInstall{owner: "test-org", repo: "test-repo"},
		SCM:     scm,
	}
	err := givenDisabledCustomHarness(w, "disabled-test", "agent: agents/triage.md\nrole: triage\nslug: disabled-test")
	require.NoError(t, err)

	agentData := scm.files["test-org/test-repo/.fullsend/agents/triage.md"]
	require.NotNil(t, agentData, "agent resource should be committed for disabled harness")
	assert.Equal(t, minimalAgentContent, string(agentData))
}

func TestCommitLocalHarnessResources_CommitsAgentFile(t *testing.T) {
	scm := &fakeURLSCM{files: map[string][]byte{}}
	w := &world.World{
		Install: &fakeURLInstall{owner: "org", repo: "repo"},
		SCM:     scm,
	}
	err := commitLocalHarnessResources(context.Background(), w, "test",
		"agent: agents/triage.md\nrole: triage")
	require.NoError(t, err)
	assert.Equal(t, minimalAgentContent, string(scm.files["org/repo/.fullsend/agents/triage.md"]))
}

func TestCommitLocalHarnessResources_SkipsURLAgentPath(t *testing.T) {
	scm := &fakeURLSCM{files: map[string][]byte{}}
	w := &world.World{
		Install: &fakeURLInstall{owner: "org", repo: "repo"},
		SCM:     scm,
	}
	err := commitLocalHarnessResources(context.Background(), w, "test",
		"agent: https://example.com/agents/triage.md\nrole: triage")
	require.NoError(t, err)
	assert.Empty(t, scm.files, "URL agent paths should not be committed")
}

func TestCommitLocalHarnessResources_SkipsAbsoluteAgentPath(t *testing.T) {
	scm := &fakeURLSCM{files: map[string][]byte{}}
	w := &world.World{
		Install: &fakeURLInstall{owner: "org", repo: "repo"},
		SCM:     scm,
	}
	err := commitLocalHarnessResources(context.Background(), w, "test",
		"agent: /absolute/agents/triage.md\nrole: triage")
	require.NoError(t, err)
	assert.Empty(t, scm.files, "absolute agent paths should not be committed")
}

func TestCommitLocalHarnessResources_NoAgentField(t *testing.T) {
	scm := &fakeURLSCM{files: map[string][]byte{}}
	w := &world.World{
		Install: &fakeURLInstall{owner: "org", repo: "repo"},
		SCM:     scm,
	}
	err := commitLocalHarnessResources(context.Background(), w, "test",
		"role: triage\nslug: test")
	require.NoError(t, err)
	assert.Empty(t, scm.files, "no files should be committed without agent field")
}

func TestCommitLocalHarnessResources_InvalidYAML(t *testing.T) {
	scm := &fakeURLSCM{files: map[string][]byte{}}
	w := &world.World{
		Install: &fakeURLInstall{owner: "org", repo: "repo"},
		SCM:     scm,
	}
	err := commitLocalHarnessResources(context.Background(), w, "test",
		"invalid: [yaml: content")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing harness YAML")
}
