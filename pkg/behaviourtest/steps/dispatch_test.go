package steps

import (
	"context"
	"fmt"
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

// --- givenKillSwitchActive tests ---

func TestGivenKillSwitchActive_SetsKillSwitch(t *testing.T) {
	scm := &fakeDispatchSCM{
		fileContent: []byte("version: \"1\"\nroles:\n  - triage\n"),
	}
	w := &world.World{
		SCM:     scm,
		Install: &fakeDispatchInstall{owner: "org", repo: "repo"},
	}
	err := givenKillSwitchActive(w)
	require.NoError(t, err)
	assert.True(t, scm.commitCalled, "CommitFile should have been called")
	assert.Contains(t, string(scm.committedContent), "kill_switch: true")
}

func TestGivenKillSwitchActive_GetFileContentError(t *testing.T) {
	scm := &fakeDispatchSCM{
		getFileErr: fmt.Errorf("not found"),
	}
	w := &world.World{
		SCM:     scm,
		Install: &fakeDispatchInstall{owner: "org", repo: "repo"},
	}
	err := givenKillSwitchActive(w)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading config")
}

func TestGivenKillSwitchActive_CommitFileError(t *testing.T) {
	scm := &fakeDispatchSCM{
		fileContent: []byte("version: \"1\"\nroles:\n  - triage\n"),
		commitErr:   fmt.Errorf("commit failed"),
	}
	w := &world.World{
		SCM:     scm,
		Install: &fakeDispatchInstall{owner: "org", repo: "repo"},
	}
	err := givenKillSwitchActive(w)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "updating config")
}

// fakeDispatchInstall implements install.State for dispatch step tests.
type fakeDispatchInstall struct {
	owner string
	repo  string
}

func (f *fakeDispatchInstall) Mode() string               { return "per-repo" }
func (f *fakeDispatchInstall) TestRepo() string           { return f.repo }
func (f *fakeDispatchInstall) ConfigOwner() string        { return f.owner }
func (f *fakeDispatchInstall) ConfigRepo() string         { return f.repo }
func (f *fakeDispatchInstall) ConfigPathPrefix() string   { return ".fullsend" }
func (f *fakeDispatchInstall) TriageWorkflowRepo() string { return f.repo }
func (f *fakeDispatchInstall) TriageWorkflowFile() string { return "" }
func (f *fakeDispatchInstall) AgentWorkflowFile() string  { return "" }
func (f *fakeDispatchInstall) AgentArtifactName() string  { return "" }

// fakeDispatchSCM implements scm.Driver for dispatch step tests.
type fakeDispatchSCM struct {
	fileContent      []byte
	getFileErr       error
	commitCalled     bool
	committedContent []byte
	commitErr        error
}

func (f *fakeDispatchSCM) GetFileContent(_ context.Context, _, _, _ string) ([]byte, error) {
	return f.fileContent, f.getFileErr
}
func (f *fakeDispatchSCM) CommitFile(_ context.Context, _, _, _, _ string, content []byte) error {
	f.commitCalled = true
	f.committedContent = content
	return f.commitErr
}
func (f *fakeDispatchSCM) CreateIssue(context.Context, string, string, string, string, ...string) (*forge.Issue, error) {
	return nil, nil
}
func (f *fakeDispatchSCM) AddIssueLabels(context.Context, string, string, int, ...string) error {
	return nil
}
func (f *fakeDispatchSCM) AddComment(context.Context, string, string, int, string) (*forge.IssueComment, error) {
	return nil, nil
}
func (f *fakeDispatchSCM) GetIssue(context.Context, string, string, int) (*forge.Issue, error) {
	return nil, nil
}
func (f *fakeDispatchSCM) CreateBranch(context.Context, string, string, string) error { return nil }
func (f *fakeDispatchSCM) DeleteBranch(context.Context, string, string, string) error { return nil }
func (f *fakeDispatchSCM) CommitFileToBranch(context.Context, string, string, string, string, string, []byte) error {
	return nil
}
func (f *fakeDispatchSCM) CreateChangeProposal(context.Context, string, string, string, string, string, string) (*forge.ChangeProposal, error) {
	return nil, nil
}
func (f *fakeDispatchSCM) SubmitPullRequestReview(context.Context, string, string, int, string) error {
	return nil
}
func (f *fakeDispatchSCM) CloseIssue(context.Context, string, string, int) error { return nil }
func (f *fakeDispatchSCM) DeleteRepo(context.Context, string, string) error      { return nil }
func (f *fakeDispatchSCM) CreateFork(context.Context, string, string, string) (string, error) {
	return "", nil
}
func (f *fakeDispatchSCM) CommitFileToFork(context.Context, string, string, string, string, string, []byte) error {
	return nil
}
func (f *fakeDispatchSCM) CreateForkChangeProposal(context.Context, string, string, string, string, string, string, string, string) (*forge.ChangeProposal, error) {
	return nil, nil
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
