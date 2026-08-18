package install

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExternalMintDriver_FailsEarly_NoMintURL(t *testing.T) {
	f := NewExternalMintFactory("", 2)
	_, err := f("org", nil, "tok", "/bin/fullsend", "", t.Logf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mintURL is required")
}

func TestExternalMintDriver_OK(t *testing.T) {
	f := NewExternalMintFactory("https://mint.test", 2)
	d, err := f("org", nil, "tok", "/bin/fullsend", "", t.Logf)
	require.NoError(t, err)
	require.NotNil(t, d)
	assert.Equal(t, 2, d.Capacity())
}

func TestExternalMintInstall_ReturnsStateWithMintURL(t *testing.T) {
	d := &externalMintDriver{mintURL: "https://mint.test"}

	state, err := d.Install(context.Background(), "my-org")
	require.NoError(t, err)
	require.NotNil(t, state)

	assert.Equal(t, "per-repo", state.Mode())
	assert.Equal(t, "my-org", state.ConfigOwner())
	// ConfigRepo is empty — the driver only manages the mint, not a
	// specific repo. Per-repo state is created by the ensurer.
	assert.Equal(t, "", state.ConfigRepo())

	ps, ok := state.(*PerRepoState)
	require.True(t, ok)
	assert.Equal(t, "https://mint.test", ps.MintURL())
}

func TestExternalMintTeardown_IsNoOp(t *testing.T) {
	d := &externalMintDriver{mintURL: "https://mint.test"}

	// Teardown should succeed and be a no-op.
	err := d.Teardown(context.Background(), "my-org", nil)
	require.NoError(t, err)
}
