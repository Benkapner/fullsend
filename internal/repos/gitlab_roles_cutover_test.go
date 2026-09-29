package repos

import (
	"context"
	"fmt"
	"testing"

	"github.com/fullsend-ai/fullsend/internal/forge"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCutoverGitLabRoleCredentialsRetiresSharedCredential(t *testing.T) {
	fc := provisionClient(t)
	seedCutoverState(t, fc)
	fc.Secrets["group/project/"+forge.SecretForgeToken] = true
	tokens := cutoverTokenInventory()

	result, err := CutoverGitLabRoleCredentials(context.Background(), GitLabRoleCutoverConfig{
		Owner: "group", Repo: "project", Client: fc, TokenInventory: tokens, DrainConfirmed: true,
	})
	require.NoError(t, err)
	assert.True(t, result.SharedRetired)
	assert.False(t, fc.Secrets["group/project/"+forge.SecretForgeToken])
	assert.Contains(t, tokens.revoked, 4)
}

func TestCutoverGitLabRoleCredentialsDefersWhenRoleMissing(t *testing.T) {
	fc := provisionClient(t)
	fc.Secrets["group/project/"+forge.SecretForgeToken] = true
	result, err := CutoverGitLabRoleCredentials(context.Background(), GitLabRoleCutoverConfig{
		Owner: "group", Repo: "project", Client: fc, TokenInventory: cutoverTokenInventory(), DrainConfirmed: true,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrGitLabRoleCutoverNotReady)
	assert.False(t, result.SharedRetired)
	assert.True(t, fc.Secrets["group/project/"+forge.SecretForgeToken])
}

func TestCutoverGitLabRoleCredentialsRequiresInventory(t *testing.T) {
	fc := provisionClient(t)
	_, err := CutoverGitLabRoleCredentials(context.Background(), GitLabRoleCutoverConfig{
		Owner: "group", Repo: "project", Client: fc, DrainConfirmed: true,
	})
	require.Error(t, err)
}

func TestCutoverGitLabRoleCredentialsRequiresDrainConfirmed(t *testing.T) {
	fc := provisionClient(t)
	seedCutoverState(t, fc)
	fc.Secrets["group/project/"+forge.SecretForgeToken] = true
	_, err := CutoverGitLabRoleCredentials(context.Background(), GitLabRoleCutoverConfig{
		Owner: "group", Repo: "project", Client: fc, TokenInventory: cutoverTokenInventory(),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "drained")
	assert.True(t, fc.Secrets["group/project/"+forge.SecretForgeToken])
}

func TestCutoverGitLabRoleCredentialsDryRunDoesNotRetire(t *testing.T) {
	fc := provisionClient(t)
	seedCutoverState(t, fc)
	fc.Secrets["group/project/"+forge.SecretForgeToken] = true
	tokens := cutoverTokenInventory()

	result, err := CutoverGitLabRoleCredentials(context.Background(), GitLabRoleCutoverConfig{
		Owner: "group", Repo: "project", Client: fc, TokenInventory: tokens, DrainConfirmed: true, DryRun: true,
	})
	require.NoError(t, err)
	assert.True(t, result.DryRun)
	assert.True(t, result.SharedRetired, "dry-run reports what would be retired without acting")
	assert.True(t, fc.Secrets["group/project/"+forge.SecretForgeToken], "dry-run must not delete the shared secret")
	assert.Empty(t, tokens.revoked, "dry-run must not revoke any token")
}

// TestCutoverGitLabRoleCredentialsRetryAfterRevokeFailureStillRevokes is a
// regression test for the retry hole in maybeRetireGitLabSharedCredential
// (internal/cli/repos_gitlab.go): if secret deletion succeeds but revoking
// the shared fullsend-bot project access token fails, a retry must not
// treat "secret already gone" as "nothing left to do" — the leftover
// Developer-scope PAT must still get revoked.
func TestCutoverGitLabRoleCredentialsRetryAfterRevokeFailureStillRevokes(t *testing.T) {
	fc := provisionClient(t)
	seedCutoverState(t, fc)
	fc.Secrets["group/project/"+forge.SecretForgeToken] = true
	tokens := cutoverTokenInventory()
	tokens.failRevoke = fmt.Errorf("revoke transiently failed")

	_, err := CutoverGitLabRoleCredentials(context.Background(), GitLabRoleCutoverConfig{
		Owner: "group", Repo: "project", Client: fc, TokenInventory: tokens, DrainConfirmed: true,
	})
	require.Error(t, err, "revoke failure must surface as an error, not a silent partial success")
	assert.False(t, fc.Secrets["group/project/"+forge.SecretForgeToken], "the secret is already deleted before revoke runs")
	assert.Empty(t, tokens.revoked, "the shared token is still active after the failed revoke")

	// Retry: the secret is gone, but the shared token is still active in
	// the inventory. CutoverGitLabRoleCredentials must still be called (the
	// caller must not skip it merely because the secret is absent) so the
	// leftover token actually gets revoked this time.
	tokens.failRevoke = nil
	result, err := CutoverGitLabRoleCredentials(context.Background(), GitLabRoleCutoverConfig{
		Owner: "group", Repo: "project", Client: fc, TokenInventory: tokens, DrainConfirmed: true,
	})
	require.NoError(t, err)
	assert.True(t, result.SharedRetired)
	assert.Contains(t, tokens.revoked, 4, "the leftover shared PAT must be revoked on retry")
}
