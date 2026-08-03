package steps

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fullsend-ai/fullsend/internal/forge"
	"github.com/fullsend-ai/fullsend/internal/runtime"
	"github.com/fullsend-ai/fullsend/pkg/behaviourtest/world"
)

func TestShouldRemoveArtifactDir(t *testing.T) {
	t.Parallel()

	ciRoot := "/tmp/behaviour-artifacts"
	assert.False(t, shouldRemoveArtifactDir(ciRoot, ciRoot))
	assert.False(t, shouldRemoveArtifactDir(ciRoot+"/run-123", ciRoot))
	assert.True(t, shouldRemoveArtifactDir("/tmp/behaviour-artifacts-evil/run-123", ciRoot))
	assert.True(t, shouldRemoveArtifactDir("/var/tmp/local-run", ciRoot))
	assert.True(t, shouldRemoveArtifactDir("/tmp/local-run", ""))
}

func TestArtifactDirUnderCIRoot(t *testing.T) {
	t.Parallel()

	ciRoot := "/tmp/behaviour-artifacts"
	assert.True(t, artifactDirUnderCIRoot(ciRoot, ciRoot))
	assert.True(t, artifactDirUnderCIRoot(ciRoot+"/run-456", ciRoot))
	assert.False(t, artifactDirUnderCIRoot("/tmp/behaviour-artifacts-evil/run", ciRoot))
}

func TestCleanupScenario_ClosesForkPR(t *testing.T) {
	t.Parallel()

	scmDriver := &fakeCleanupSCM{}
	w := &world.World{
		RepoOwner:    "org",
		RepoName:     "repo",
		ForkPRNumber: 42,
		SCM:          scmDriver,
	}
	CleanupScenario(w)
	require.Len(t, scmDriver.closedIssues, 1)
	assert.Equal(t, "org", scmDriver.closedIssues[0].owner)
	assert.Equal(t, "repo", scmDriver.closedIssues[0].repo)
	assert.Equal(t, 42, scmDriver.closedIssues[0].number)
}

func TestCleanupScenario_ClosesForkPR_Error(t *testing.T) {
	t.Parallel()

	var logged []string
	scmDriver := &fakeCleanupSCM{closeIssueErr: fmt.Errorf("close failed")}
	w := &world.World{
		RepoOwner:    "org",
		RepoName:     "repo",
		ForkPRNumber: 42,
		SCM:          scmDriver,
		Logf:         func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) },
	}
	CleanupScenario(w)
	require.Len(t, logged, 1)
	assert.Contains(t, logged[0], "close fork PR #42")
}

func TestCleanupScenario_SkipsForkCleanupWhenNotSet(t *testing.T) {
	t.Parallel()

	scmDriver := &fakeCleanupSCM{}
	w := &world.World{
		RepoOwner: "org",
		RepoName:  "repo",
		SCM:       scmDriver,
	}
	CleanupScenario(w)
	assert.Empty(t, scmDriver.closedIssues)
}

func TestCleanupScenario_DeletesForkBranch(t *testing.T) {
	t.Parallel()

	scmDriver := &fakeCleanupSCM{}
	w := &world.World{
		RepoOwner:    "org",
		RepoName:     "repo",
		ForkPRNumber: 10,
		ForkOwner:    "org",
		ForkRepo:     "fork-repo",
		ForkPRBranch: "test-branch",
		SCM:          scmDriver,
	}
	CleanupScenario(w)

	require.Len(t, scmDriver.closedIssues, 1)
	assert.Equal(t, 10, scmDriver.closedIssues[0].number)

	require.Len(t, scmDriver.deletedBranches, 1)
	assert.Equal(t, "org", scmDriver.deletedBranches[0].owner)
	assert.Equal(t, "fork-repo", scmDriver.deletedBranches[0].repo)
	assert.Equal(t, "test-branch", scmDriver.deletedBranches[0].branch)
}

func TestCleanupScenario_DeleteBranchNotFound_SilentlyIgnored(t *testing.T) {
	t.Parallel()

	var logged []string
	scmDriver := &fakeCleanupSCM{deleteBranchErr: fmt.Errorf("delete branch: %w", forge.ErrNotFound)}
	w := &world.World{
		RepoOwner:    "org",
		RepoName:     "repo",
		ForkOwner:    "org",
		ForkRepo:     "fork-repo",
		ForkPRBranch: "gone-branch",
		SCM:          scmDriver,
		Logf:         func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) },
	}
	CleanupScenario(w)

	// 404/ErrNotFound is silently ignored — no log output for branch deletion.
	for _, msg := range logged {
		assert.NotContains(t, msg, "fork branch", "ErrNotFound should be silently ignored")
	}
}

func TestCleanupScenario_DeleteBranchError_Logged(t *testing.T) {
	t.Parallel()

	var logged []string
	scmDriver := &fakeCleanupSCM{deleteBranchErr: fmt.Errorf("server error")}
	w := &world.World{
		RepoOwner:    "org",
		RepoName:     "repo",
		ForkOwner:    "org",
		ForkRepo:     "fork-repo",
		ForkPRBranch: "bad-branch",
		SCM:          scmDriver,
		Logf:         func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) },
	}
	CleanupScenario(w)

	require.Len(t, logged, 1)
	assert.Contains(t, logged[0], "delete fork branch bad-branch")
	assert.Contains(t, logged[0], "server error")
}

func TestCleanupScenario_DeletesForkRepo(t *testing.T) {
	t.Parallel()

	scmDriver := &fakeCleanupSCM{}
	w := &world.World{
		RepoOwner: "org",
		RepoName:  "repo",
		ForkOwner: "org",
		ForkRepo:  "repo-fork",
		SCM:       scmDriver,
	}
	CleanupScenario(w)

	require.Len(t, scmDriver.deletedRepos, 1)
	assert.Equal(t, "org", scmDriver.deletedRepos[0].owner)
	assert.Equal(t, "repo-fork", scmDriver.deletedRepos[0].repo)
}

func TestCleanupScenario_DeleteForkRepoNotFound_SilentlyIgnored(t *testing.T) {
	t.Parallel()

	var logged []string
	scmDriver := &fakeCleanupSCM{deleteRepoErr: fmt.Errorf("delete repo: %w", forge.ErrNotFound)}
	w := &world.World{
		RepoOwner: "org",
		RepoName:  "repo",
		ForkOwner: "org",
		ForkRepo:  "repo-fork",
		SCM:       scmDriver,
		Logf:      func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) },
	}
	CleanupScenario(w)

	for _, msg := range logged {
		assert.NotContains(t, msg, "fork repo", "ErrNotFound should be silently ignored")
	}
}

func TestCleanupScenario_DeleteForkRepoError_Logged(t *testing.T) {
	t.Parallel()

	var logged []string
	scmDriver := &fakeCleanupSCM{deleteRepoErr: fmt.Errorf("server error")}
	w := &world.World{
		RepoOwner: "org",
		RepoName:  "repo",
		ForkOwner: "org",
		ForkRepo:  "repo-fork",
		SCM:       scmDriver,
		Logf:      func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) },
	}
	CleanupScenario(w)

	require.Len(t, logged, 1)
	assert.Contains(t, logged[0], "delete fork repo org/repo-fork")
	assert.Contains(t, logged[0], "server error")
}

func TestCleanupScenario_SkipsForkRepoDelete_WhenForkRepoEqualsRepoName(t *testing.T) {
	t.Parallel()

	scmDriver := &fakeCleanupSCM{}
	w := &world.World{
		RepoOwner: "org",
		RepoName:  "repo",
		ForkOwner: "org",
		ForkRepo:  "repo", // same as RepoName — must not be deleted
		SCM:       scmDriver,
	}
	CleanupScenario(w)

	assert.Empty(t, scmDriver.deletedRepos, "repo deletion should be skipped when ForkRepo == RepoName")
}

func TestCleanupScenario_SkipsForkRepoDelete_WhenFieldsMissing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		world *world.World
	}{
		{
			name: "missing ForkOwner",
			world: &world.World{
				RepoOwner: "org",
				RepoName:  "repo",
				ForkRepo:  "repo-fork",
				SCM:       &fakeCleanupSCM{},
			},
		},
		{
			name: "missing ForkRepo",
			world: &world.World{
				RepoOwner: "org",
				RepoName:  "repo",
				ForkOwner: "org",
				SCM:       &fakeCleanupSCM{},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			scm := tt.world.SCM.(*fakeCleanupSCM)
			CleanupScenario(tt.world)
			assert.Empty(t, scm.deletedRepos, "repo deletion should be skipped when fields are missing")
		})
	}
}

func TestCleanupScenario_SkipsBranchDelete_WhenFieldsMissing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		world *world.World
	}{
		{
			name: "missing ForkPRBranch",
			world: &world.World{
				RepoOwner: "org",
				RepoName:  "repo",
				ForkOwner: "org",
				ForkRepo:  "fork-repo",
				SCM:       &fakeCleanupSCM{},
			},
		},
		{
			name: "missing ForkOwner",
			world: &world.World{
				RepoOwner:    "org",
				RepoName:     "repo",
				ForkRepo:     "fork-repo",
				ForkPRBranch: "branch",
				SCM:          &fakeCleanupSCM{},
			},
		},
		{
			name: "missing ForkRepo",
			world: &world.World{
				RepoOwner:    "org",
				RepoName:     "repo",
				ForkOwner:    "org",
				ForkPRBranch: "branch",
				SCM:          &fakeCleanupSCM{},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			scm := tt.world.SCM.(*fakeCleanupSCM)
			CleanupScenario(tt.world)
			assert.Empty(t, scm.deletedBranches, "branch deletion should be skipped when fields are missing")
		})
	}
}

// --- URL harness hosting repo cleanup tests ---

func TestCleanupScenario_DeletesHostingRepo(t *testing.T) {
	t.Parallel()

	scmDriver := &fakeCleanupSCM{}
	w := &world.World{
		RepoOwner:           "org",
		RepoName:            "repo",
		URLHarnessRepoOwner: "org",
		URLHarnessRepoName:  "test-repo-07-url-harness-host",
		SCM:                 scmDriver,
	}
	CleanupScenario(w)

	require.Len(t, scmDriver.deletedRepos, 1)
	assert.Equal(t, "org", scmDriver.deletedRepos[0].owner)
	assert.Equal(t, "test-repo-07-url-harness-host", scmDriver.deletedRepos[0].repo)
}

func TestCleanupScenario_SkipsHostingRepoDelete_WhenEqualsRepoName(t *testing.T) {
	t.Parallel()

	scmDriver := &fakeCleanupSCM{}
	w := &world.World{
		RepoOwner:           "org",
		RepoName:            "repo",
		URLHarnessRepoOwner: "org",
		URLHarnessRepoName:  "repo", // same as RepoName — must not be deleted
		SCM:                 scmDriver,
	}
	CleanupScenario(w)

	assert.Empty(t, scmDriver.deletedRepos, "repo deletion should be skipped when URLHarnessRepoName == RepoName")
}

func TestCleanupScenario_SkipsHostingRepoDelete_WhenFieldsMissing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		world *world.World
	}{
		{
			name: "missing URLHarnessRepoOwner",
			world: &world.World{
				RepoOwner:          "org",
				RepoName:           "repo",
				URLHarnessRepoName: "host-repo",
				SCM:                &fakeCleanupSCM{},
			},
		},
		{
			name: "missing URLHarnessRepoName",
			world: &world.World{
				RepoOwner:           "org",
				RepoName:            "repo",
				URLHarnessRepoOwner: "org",
				SCM:                 &fakeCleanupSCM{},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			scm := tt.world.SCM.(*fakeCleanupSCM)
			CleanupScenario(tt.world)
			assert.Empty(t, scm.deletedRepos, "repo deletion should be skipped when hosting repo fields are missing")
		})
	}
}

func TestCleanupScenario_DeleteHostingRepoNotFound_SilentlyIgnored(t *testing.T) {
	t.Parallel()

	var logged []string
	scmDriver := &fakeCleanupSCM{deleteRepoErr: fmt.Errorf("delete repo: %w", forge.ErrNotFound)}
	w := &world.World{
		RepoOwner:           "org",
		RepoName:            "repo",
		URLHarnessRepoOwner: "org",
		URLHarnessRepoName:  "host-repo",
		SCM:                 scmDriver,
		Logf:                func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) },
	}
	CleanupScenario(w)

	for _, msg := range logged {
		assert.NotContains(t, msg, "harness-hosting repo", "ErrNotFound should be silently ignored")
	}
}

func TestCleanupScenario_DeleteHostingRepoError_Logged(t *testing.T) {
	t.Parallel()

	var logged []string
	scmDriver := &fakeCleanupSCM{deleteRepoErr: fmt.Errorf("server error")}
	w := &world.World{
		RepoOwner:           "org",
		RepoName:            "repo",
		URLHarnessRepoOwner: "org",
		URLHarnessRepoName:  "host-repo",
		SCM:                 scmDriver,
		Logf:                func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) },
	}
	CleanupScenario(w)

	// The shared deleteRepoErr causes log messages for both hosting repo
	// (no fork fields set, so only hosting repo cleanup fires).
	require.Len(t, logged, 1)
	assert.Contains(t, logged[0], "delete harness-hosting repo org/host-repo")
	assert.Contains(t, logged[0], "server error")
}

// fakeCleanupSCM implements scm.Driver for cleanup unit tests.
type fakeCleanupSCM struct {
	closedIssues     []closedIssueRecord
	closeIssueErr    error
	deletedBranches  []deletedBranchRecord
	deleteBranchErr  error
	deletedRepos     []deletedRepoRecord
	deleteRepoErr    error
	commitFileCalled bool
	commitFileErr    error
}

type closedIssueRecord struct {
	owner  string
	repo   string
	number int
}

type deletedBranchRecord struct {
	owner  string
	repo   string
	branch string
}

type deletedRepoRecord struct {
	owner string
	repo  string
}

func (f *fakeCleanupSCM) CloseIssue(_ context.Context, owner, repo string, number int) error {
	if f.closeIssueErr != nil {
		return f.closeIssueErr
	}
	f.closedIssues = append(f.closedIssues, closedIssueRecord{owner: owner, repo: repo, number: number})
	return nil
}

func (f *fakeCleanupSCM) DeleteBranch(_ context.Context, owner, repo, branch string) error {
	if f.deleteBranchErr != nil {
		return f.deleteBranchErr
	}
	f.deletedBranches = append(f.deletedBranches, deletedBranchRecord{owner: owner, repo: repo, branch: branch})
	return nil
}

func (f *fakeCleanupSCM) DeleteRepo(_ context.Context, owner, repo string) error {
	if f.deleteRepoErr != nil {
		return f.deleteRepoErr
	}
	f.deletedRepos = append(f.deletedRepos, deletedRepoRecord{owner: owner, repo: repo})
	return nil
}

// Unused scm.Driver methods — required for interface satisfaction.

func (f *fakeCleanupSCM) CreateIssue(context.Context, string, string, string, string, ...string) (*forge.Issue, error) {
	return nil, nil
}

func (f *fakeCleanupSCM) AddIssueLabels(context.Context, string, string, int, ...string) error {
	return nil
}

func (f *fakeCleanupSCM) AddComment(context.Context, string, string, int, string) (*forge.IssueComment, error) {
	return nil, nil
}

func (f *fakeCleanupSCM) GetIssue(context.Context, string, string, int) (*forge.Issue, error) {
	return nil, nil
}

func (f *fakeCleanupSCM) GetFileContent(context.Context, string, string, string) ([]byte, error) {
	return nil, nil
}

func (f *fakeCleanupSCM) CommitFile(_ context.Context, _, _, _, _ string, _ []byte) error {
	f.commitFileCalled = true
	return f.commitFileErr
}

func (f *fakeCleanupSCM) CreateBranch(context.Context, string, string, string) error {
	return nil
}

func (f *fakeCleanupSCM) CommitFileToBranch(context.Context, string, string, string, string, string, []byte) error {
	return nil
}

func (f *fakeCleanupSCM) CreateChangeProposal(context.Context, string, string, string, string, string, string) (*forge.ChangeProposal, error) {
	return nil, nil
}

func (f *fakeCleanupSCM) SubmitPullRequestReview(context.Context, string, string, int, string) error {
	return nil
}

func (f *fakeCleanupSCM) CreateRepo(context.Context, string, string, string) error {
	return nil
}

func (f *fakeCleanupSCM) EnsureRepoPublic(context.Context, string, string) error {
	return nil
}

func (f *fakeCleanupSCM) GetDefaultBranch(context.Context, string, string) (string, error) {
	return "main", nil
}

func (f *fakeCleanupSCM) GetBranchRef(context.Context, string, string, string) (string, error) {
	return "abc123", nil
}

func (f *fakeCleanupSCM) CreateFork(context.Context, string, string, string) (string, error) {
	return "", nil
}

func (f *fakeCleanupSCM) CommitFileToFork(context.Context, string, string, string, string, string, []byte) error {
	return nil
}

func (f *fakeCleanupSCM) CreateForkChangeProposal(context.Context, string, string, string, string, string, string, string, string) (*forge.ChangeProposal, error) {
	return nil, nil
}

// --- Issue cleanup tests ---

func TestCleanupScenario_ClosesIssue(t *testing.T) {
	t.Parallel()

	scmDriver := &fakeCleanupSCM{}
	w := &world.World{
		RepoOwner:   "org",
		RepoName:    "repo",
		IssueNumber: 10,
		SCM:         scmDriver,
	}
	CleanupScenario(w)
	require.Len(t, scmDriver.closedIssues, 1)
	assert.Equal(t, "org", scmDriver.closedIssues[0].owner)
	assert.Equal(t, "repo", scmDriver.closedIssues[0].repo)
	assert.Equal(t, 10, scmDriver.closedIssues[0].number)
}

func TestCleanupScenario_ClosesIssue_Error(t *testing.T) {
	t.Parallel()

	var logged []string
	scmDriver := &fakeCleanupSCM{closeIssueErr: fmt.Errorf("close failed")}
	w := &world.World{
		RepoOwner:   "org",
		RepoName:    "repo",
		IssueNumber: 7,
		SCM:         scmDriver,
		Logf:        func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) },
	}
	CleanupScenario(w)
	require.Len(t, logged, 1)
	assert.Contains(t, logged[0], "close issue #7")
}

// --- Artifact cleanup tests ---

func TestCleanupScenario_RemovesArtifactDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	scmDriver := &fakeCleanupSCM{}
	w := &world.World{
		RepoOwner:   "org",
		RepoName:    "repo",
		ArtifactDir: dir,
		SCM:         scmDriver,
	}
	CleanupScenario(w)
	// Verify the directory no longer exists.
	assert.NoDirExists(t, dir)
}

// --- Dummy script cleanup tests ---

func TestCleanupScenario_ClearsDummyOps(t *testing.T) {
	t.Parallel()

	scmDriver := &fakeCleanupSCM{}
	installDriver := &fakeCleanupInstall{owner: "org", repo: "repo"}
	w := &world.World{
		RepoOwner: "org",
		RepoName:  "repo",
		DummyOps:  []runtime.BehaviourOperation{{Op: "echo", Args: "hello"}},
		Install:   installDriver,
		SCM:       scmDriver,
	}
	CleanupScenario(w)
	assert.True(t, scmDriver.commitFileCalled, "should commit empty ops to clear dummy script")
}

func TestCleanupScenario_ClearsDummyOps_Error(t *testing.T) {
	t.Parallel()

	var logged []string
	scmDriver := &fakeCleanupSCM{commitFileErr: fmt.Errorf("commit failed")}
	installDriver := &fakeCleanupInstall{owner: "org", repo: "repo"}
	w := &world.World{
		RepoOwner: "org",
		RepoName:  "repo",
		DummyOps:  []runtime.BehaviourOperation{{Op: "echo", Args: "hello"}},
		Install:   installDriver,
		SCM:       scmDriver,
		Logf:      func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) },
	}
	CleanupScenario(w)
	require.Len(t, logged, 1)
	assert.Contains(t, logged[0], "clear dummy script")
}

// fakeCleanupInstall satisfies the Install interface for cleanup tests.
type fakeCleanupInstall struct {
	owner string
	repo  string
}

func (f *fakeCleanupInstall) Mode() string               { return "per-repo" }
func (f *fakeCleanupInstall) TestRepo() string           { return f.repo }
func (f *fakeCleanupInstall) ConfigOwner() string        { return f.owner }
func (f *fakeCleanupInstall) ConfigRepo() string         { return f.repo }
func (f *fakeCleanupInstall) ConfigPathPrefix() string   { return ".fullsend" }
func (f *fakeCleanupInstall) TriageWorkflowRepo() string { return f.repo }
func (f *fakeCleanupInstall) TriageWorkflowFile() string { return "fullsend.yaml" }
func (f *fakeCleanupInstall) AgentWorkflowFile() string  { return "reusable-triage.yml" }
func (f *fakeCleanupInstall) AgentArtifactName() string  { return "fullsend-triage" }
