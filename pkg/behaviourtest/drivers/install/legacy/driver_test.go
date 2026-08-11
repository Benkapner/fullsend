package legacy

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fullsend-ai/fullsend/pkg/behaviourtest/drivers/install"
)

func TestNewDriver_FailsEarly_NoMintURL(t *testing.T) {
	_, err := NewDriver(nil, "tok", "/bin/fullsend", "", "", "test-repo-01", t.Logf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mintURL is required")
}

func TestNewDriver_FailsEarly_NoRepo(t *testing.T) {
	_, err := NewDriver(nil, "tok", "/bin/fullsend", "https://mint.test", "", "", t.Logf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "repo is required")
}

func TestNewDriver_OK(t *testing.T) {
	d, err := NewDriver(nil, "tok", "/bin/fullsend", "https://mint.test", "", "test-repo-01", t.Logf)
	require.NoError(t, err)
	require.NotNil(t, d)

	// Verify it implements install.Driver.
	var _ install.Driver = d
}
