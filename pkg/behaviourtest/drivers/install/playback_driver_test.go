package install

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fullsend-ai/fullsend/internal/forge"
	"github.com/fullsend-ai/fullsend/pkg/behaviourtest/drivers/install/common"
)

// fakeBaseDriver is a minimal Driver test double for verifying that
// PlaybackDriver delegates to its wrapped base Driver.
type fakeBaseDriver struct {
	allocateCalls  int
	allocateName   string
	allocateErr    error
	deallocateName string
	deallocateErr  error
	finalizeCalled bool
	finalizeErr    error
	capacity       int
}

func (f *fakeBaseDriver) AllocateRepo(context.Context) (string, error) {
	f.allocateCalls++
	return f.allocateName, f.allocateErr
}

func (f *fakeBaseDriver) DeallocateRepo(_ context.Context, repoName string) error {
	f.deallocateName = repoName
	return f.deallocateErr
}

func (f *fakeBaseDriver) Finalize(context.Context) error {
	f.finalizeCalled = true
	return f.finalizeErr
}

func (f *fakeBaseDriver) Capacity() int { return f.capacity }

var _ Driver = (*fakeBaseDriver)(nil)

func TestPlaybackDriver_DelegatesToBase(t *testing.T) {
	base := &fakeBaseDriver{allocateName: "test-repo-01", capacity: 7}
	pd := NewPlaybackDriver(base, "acme", forge.NewFakeClient(), t.Logf)

	name, err := pd.AllocateRepo(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "test-repo-01", name)
	assert.Equal(t, 1, base.allocateCalls)

	require.NoError(t, pd.DeallocateRepo(context.Background(), "test-repo-01"))
	assert.Equal(t, "test-repo-01", base.deallocateName)

	require.NoError(t, pd.Finalize(context.Background()))
	assert.True(t, base.finalizeCalled)

	assert.Equal(t, 7, pd.Capacity())
}

func TestPlaybackDriver_AllocateRepo_PropagatesBaseError(t *testing.T) {
	base := &fakeBaseDriver{allocateErr: errors.New("pool exhausted")}
	pd := NewPlaybackDriver(base, "acme", forge.NewFakeClient(), t.Logf)

	_, err := pd.AllocateRepo(context.Background())
	assert.ErrorContains(t, err, "pool exhausted")
}

func TestPlaybackDriver_SetRepoHint_NoOp(t *testing.T) {
	base := &fakeBaseDriver{}
	pd := NewPlaybackDriver(base, "acme", forge.NewFakeClient(), t.Logf)

	// SetRepoHint is retained for the shared playback step definitions
	// but does not affect allocation — pool repos have stable names
	// assigned by the wrapped Driver.
	assert.NotPanics(t, func() { pd.SetRepoHint("Some Scenario Name") })
}

func TestPlaybackDriver_InstalledBotToken_Found(t *testing.T) {
	client := forge.NewFakeClient()
	client.VariableValues = map[string]string{"acme/test-repo-01/FULLSEND_FORGE_TOKEN": "tok-123"}
	pd := NewPlaybackDriver(&fakeBaseDriver{}, "acme", client, t.Logf)

	tok, err := pd.InstalledBotToken(context.Background(), "test-repo-01")
	require.NoError(t, err)
	assert.Equal(t, "tok-123", tok)
}

func TestPlaybackDriver_InstalledBotToken_NotFound(t *testing.T) {
	pd := NewPlaybackDriver(&fakeBaseDriver{}, "acme", forge.NewFakeClient(), t.Logf)

	_, err := pd.InstalledBotToken(context.Background(), "test-repo-01")
	assert.ErrorContains(t, err, "FULLSEND_FORGE_TOKEN not found")
}

func TestPlaybackInstallHooks_NonPlaybackRuntime_NoHooks(t *testing.T) {
	hooks := playbackInstallHooks(common.GitHubSetupOpts{}, t.Logf)
	assert.Nil(t, hooks.BeforeInstall)
	assert.Nil(t, hooks.AfterInstall)

	hooks = playbackInstallHooks(common.GitHubSetupOpts{Runtime: "dummy"}, t.Logf)
	assert.Nil(t, hooks.AfterInstall)
}

func TestPlaybackInstallHooks_AfterInstall_CreatesTrackingIssueAndComment(t *testing.T) {
	client := forge.NewFakeClient()
	hooks := playbackInstallHooks(common.GitHubSetupOpts{Runtime: "dummy-playback"}, t.Logf)
	require.NotNil(t, hooks.AfterInstall)

	err := hooks.AfterInstall(context.Background(), client, "acme", "test-repo-01", nil)
	require.NoError(t, err)

	content, err := client.GetFileContent(context.Background(), "acme", "test-repo-01", playbackCommentPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "acme/test-repo-01/issues/comments/")
}

func TestPlaybackInstallHooks_AfterInstall_CreateIssueError(t *testing.T) {
	client := forge.NewFakeClient()
	client.Errors = map[string]error{"CreateIssue": errors.New("boom")}
	hooks := playbackInstallHooks(common.GitHubSetupOpts{Runtime: "dummy-playback"}, t.Logf)

	err := hooks.AfterInstall(context.Background(), client, "acme", "test-repo-01", nil)
	assert.ErrorContains(t, err, "creating playback tracking issue")
}

func TestPlaybackSetupOpts_DefaultsToVendoredDummy(t *testing.T) {
	t.Setenv("PLAYBACK_RUNTIME", "")
	opts := playbackSetupOpts()
	assert.True(t, opts.Vendor)
	assert.Empty(t, opts.Runtime)
}

func TestPlaybackSetupOpts_HonoursEnvOverride(t *testing.T) {
	t.Setenv("PLAYBACK_RUNTIME", "dummy-playback")
	opts := playbackSetupOpts()
	assert.Equal(t, "dummy-playback", opts.Runtime)
}
