package install

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExternalMintInstall_ReturnsMintURL(t *testing.T) {
	d := &externalMintDriver{mintURL: "https://mint.test"}

	mintURL, err := d.Install(context.Background(), "my-org")
	require.NoError(t, err)
	assert.Equal(t, "https://mint.test", mintURL)
}

func TestExternalMintTeardown_IsNoOp(t *testing.T) {
	d := &externalMintDriver{mintURL: "https://mint.test"}

	// Teardown should succeed and be a no-op.
	err := d.Teardown(context.Background())
	require.NoError(t, err)
}
