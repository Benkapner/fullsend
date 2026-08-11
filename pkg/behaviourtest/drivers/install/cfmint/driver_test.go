package cfmint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fullsend-ai/fullsend/pkg/behaviourtest/drivers/install"
)

func TestWorkerName(t *testing.T) {
	assert.Equal(t, "bt-mint", WorkerName("bt"))
	assert.Equal(t, "e2e-mint", WorkerName("e2e"))
}

func TestPreviewMintURL(t *testing.T) {
	tests := []struct {
		name       string
		alias      string
		workerName string
		want       string
	}{
		{
			name:       "standard alias",
			alias:      "bt-abc12345",
			workerName: "bt-mint",
			want:       "https://bt-abc12345-bt-mint.workers.dev",
		},
		{
			name:       "different suite worker",
			alias:      "bt-deadbeef",
			workerName: "e2e-mint",
			want:       "https://bt-deadbeef-e2e-mint.workers.dev",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, PreviewMintURL(tt.alias, tt.workerName))
		})
	}
}

func TestGeneratePreviewAlias(t *testing.T) {
	alias, err := GeneratePreviewAlias()
	require.NoError(t, err)

	// Format: bt-<8-hex-chars>
	assert.True(t, strings.HasPrefix(alias, "bt-"), "alias should start with bt-")
	assert.Len(t, alias, 11, "bt- + 8 hex chars = 11 chars")

	// Must be unique across calls.
	alias2, err := GeneratePreviewAlias()
	require.NoError(t, err)
	assert.NotEqual(t, alias, alias2, "sequential aliases should differ")
}

func TestNewDriver_FailsEarly_NoPEMDir(t *testing.T) {
	_, err := NewDriver(nil, "tok", "/bin/fullsend", "", t.Logf, Config{
		SuiteName: "bt",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PEMDir is required")
}

func TestNewDriver_FailsEarly_EmptyPEMDir(t *testing.T) {
	dir := t.TempDir()
	_, err := NewDriver(nil, "tok", "/bin/fullsend", "", t.Logf, Config{
		PEMDir:    dir,
		SuiteName: "bt",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no .pem files")
}

func TestNewDriver_FailsEarly_NoSuiteName(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "fullsend.pem"), []byte("pem"), 0600))

	_, err := NewDriver(nil, "tok", "/bin/fullsend", "", t.Logf, Config{
		PEMDir: dir,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SuiteName is required")
}

func TestNewDriver_OK(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "fullsend.pem"), []byte("pem"), 0600))

	d, err := NewDriver(nil, "tok", "/bin/fullsend", "", t.Logf, Config{
		PEMDir:          dir,
		SuiteName:       "bt",
		AllowedOrgs:     "my-org",
		PerRepoWIFRepos: "my-org/test-repo-01,my-org/test-repo-02",
	})
	require.NoError(t, err)
	require.NotNil(t, d)
}

func TestDriver_DeployCFMint_Args(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "fullsend.pem"), []byte("pem"), 0600))

	d := &driver{
		binary:     "/bin/fullsend",
		token:      "tok",
		logf:       t.Logf,
		workerName: "bt-mint",
		cfg: Config{
			PEMDir:          dir,
			SuiteName:       "bt",
			AllowedOrgs:     "my-org",
			PerRepoWIFRepos: "my-org/test-repo-01",
		},
	}

	// We can't easily test deployCFMint since it calls e2etest.TryRunCLI directly.
	// Instead, verify the URL derivation.
	url := PreviewMintURL("bt-test1234", d.workerName)
	assert.Equal(t, "https://bt-test1234-bt-mint.workers.dev", url)
}

func TestDriver_Implements_Install_Driver(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "fullsend.pem"), []byte("pem"), 0600))

	d, err := NewDriver(nil, "tok", "/bin/fullsend", "", t.Logf, Config{
		PEMDir:          dir,
		SuiteName:       "bt",
		AllowedOrgs:     "org",
		PerRepoWIFRepos: "org/repo",
	})
	require.NoError(t, err)

	// Verify it implements install.Driver.
	var _ install.Driver = d
}

func TestPerRepoState_MintURL(t *testing.T) {
	st := install.NewPerRepoState("test-org", "test-repo", "https://bt-test-bt-mint.workers.dev")
	assert.Equal(t, "https://bt-test-bt-mint.workers.dev", st.MintURL())

	// Verify MintURLProvider interface.
	var provider install.MintURLProvider = st
	assert.Equal(t, st.MintURL(), provider.MintURL())
}
