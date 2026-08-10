package install

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fullsend-ai/fullsend/pkg/e2etest"
)

func TestPreviewMintURL(t *testing.T) {
	tests := []struct {
		name  string
		alias string
		want  string
	}{
		{
			name:  "standard alias",
			alias: "bt-abc12345",
			want:  "https://bt-abc12345-fullsend-mint.workers.dev",
		},
		{
			name:  "another alias",
			alias: "bt-deadbeef",
			want:  "https://bt-deadbeef-fullsend-mint.workers.dev",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, previewMintURL(tt.alias))
		})
	}
}

func TestNewCFMintConfig(t *testing.T) {
	e2eCfg := e2etest.EnvConfig{
		CFMintPEMDir: "/ci/pems",
	}

	cfg := newCFMintConfig(e2eCfg)
	assert.Equal(t, "/ci/pems", cfg.PEMDir)
}

func TestNewCFMintConfig_Empty(t *testing.T) {
	cfg := newCFMintConfig(e2etest.EnvConfig{})
	assert.Empty(t, cfg.PEMDir)
}

func TestGeneratePreviewAlias(t *testing.T) {
	alias, err := generatePreviewAlias()
	require.NoError(t, err)

	// Format: bt-<8-hex-chars>
	assert.True(t, strings.HasPrefix(alias, "bt-"), "alias should start with bt-")
	assert.Len(t, alias, 11, "bt- + 8 hex chars = 11 chars")

	// Must be unique across calls.
	alias2, err := generatePreviewAlias()
	require.NoError(t, err)
	assert.NotEqual(t, alias, alias2, "sequential aliases should differ")
}

func TestBuildPerRepoWIFRepos(t *testing.T) {
	got := buildPerRepoWIFRepos("halfsend-01")
	repos := strings.Split(got, ",")
	assert.Len(t, repos, btPoolSize)
	assert.Equal(t, "halfsend-01/test-repo-01", repos[0])
	assert.Equal(t, "halfsend-01/test-repo-12", repos[btPoolSize-1])
}

func TestDeployCFMint_Args(t *testing.T) {
	// Verify that deployCFMint passes the correct CLI arguments.
	var capturedArgs []string
	stubCLI := func(binary, token string, args ...string) (string, error) {
		capturedArgs = args
		return "", nil
	}

	cfg := cfMintConfig{PEMDir: "/ci/pems"}
	url, err := deployCFMint("/bin/fullsend", "tok", cfg, "bt-test1234", "my-org", stubCLI, t.Logf)
	require.NoError(t, err)
	assert.Equal(t, "https://bt-test1234-fullsend-mint.workers.dev", url)

	// Verify all arguments.
	assert.Contains(t, capturedArgs, "mint")
	assert.Contains(t, capturedArgs, "deploy")
	assert.Contains(t, capturedArgs, "--platform")
	assert.Contains(t, capturedArgs, "cloudflare")
	assert.Contains(t, capturedArgs, "--preview")
	assert.Contains(t, capturedArgs, "bt-test1234")
	assert.Contains(t, capturedArgs, "--pem-dir")
	assert.Contains(t, capturedArgs, "/ci/pems")
	assert.Contains(t, capturedArgs, "--allowed-orgs")
	assert.Contains(t, capturedArgs, "my-org")
	assert.Contains(t, capturedArgs, "--per-repo-wif-repos")
}

func TestDeployCFMint_AutoGeneratesPerRepoWIFRepos(t *testing.T) {
	var capturedArgs []string
	stubCLI := func(binary, token string, args ...string) (string, error) {
		capturedArgs = args
		return "", nil
	}

	cfg := cfMintConfig{PEMDir: "/ci/pems"}
	_, err := deployCFMint("/bin/fullsend", "tok", cfg, "bt-test0000", "halfsend-05", stubCLI, t.Logf)
	require.NoError(t, err)

	// Find the --per-repo-wif-repos value.
	var wifRepos string
	for i, arg := range capturedArgs {
		if arg == "--per-repo-wif-repos" && i+1 < len(capturedArgs) {
			wifRepos = capturedArgs[i+1]
			break
		}
	}
	require.NotEmpty(t, wifRepos, "--per-repo-wif-repos should be present")
	repos := strings.Split(wifRepos, ",")
	assert.Len(t, repos, btPoolSize)
	assert.Equal(t, "halfsend-05/test-repo-01", repos[0])
}

func TestDeployCFMint_AllowedOrgsIsAcquiredOrg(t *testing.T) {
	var capturedArgs []string
	stubCLI := func(binary, token string, args ...string) (string, error) {
		capturedArgs = args
		return "", nil
	}

	cfg := cfMintConfig{PEMDir: "/ci/pems"}
	_, err := deployCFMint("/bin/fullsend", "tok", cfg, "bt-test0000", "halfsend-03", stubCLI, t.Logf)
	require.NoError(t, err)

	// Find the --allowed-orgs value.
	var allowedOrgs string
	for i, arg := range capturedArgs {
		if arg == "--allowed-orgs" && i+1 < len(capturedArgs) {
			allowedOrgs = capturedArgs[i+1]
			break
		}
	}
	assert.Equal(t, "halfsend-03", allowedOrgs, "should use the acquired org")
}

func TestDeployCFMint_Error(t *testing.T) {
	stubCLI := func(binary, token string, args ...string) (string, error) {
		return "", fmt.Errorf("deploy failed")
	}

	cfg := cfMintConfig{PEMDir: "/ci/pems"}
	_, err := deployCFMint("/bin/fullsend", "tok", cfg, "bt-err00000", "org", stubCLI, t.Logf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mint deploy")
	assert.Contains(t, err.Error(), "deploy failed")
}

func TestTeardownCFMint_OK(t *testing.T) {
	var capturedArgs []string
	stubCLI := func(binary, token string, args ...string) (string, error) {
		capturedArgs = args
		return "", nil
	}

	err := teardownCFMint("/bin/fullsend", "tok", "bt-tear0000", stubCLI, t.Logf)
	require.NoError(t, err)

	assert.Contains(t, capturedArgs, "mint")
	assert.Contains(t, capturedArgs, "delete")
	assert.Contains(t, capturedArgs, "--platform")
	assert.Contains(t, capturedArgs, "cloudflare")
	assert.Contains(t, capturedArgs, "--preview")
	assert.Contains(t, capturedArgs, "bt-tear0000")
	assert.Contains(t, capturedArgs, "--yolo")
}

func TestTeardownCFMint_Error(t *testing.T) {
	stubCLI := func(binary, token string, args ...string) (string, error) {
		return "", fmt.Errorf("delete failed")
	}

	err := teardownCFMint("/bin/fullsend", "tok", "bt-err00001", stubCLI, t.Logf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mint delete")
	assert.Contains(t, err.Error(), "delete failed")
}

func TestPerRepoState_MintURL(t *testing.T) {
	st := &perRepoState{
		org:     "test-org",
		repo:    "test-repo",
		mintURL: "https://bt-test-fullsend-mint.workers.dev",
	}
	assert.Equal(t, "https://bt-test-fullsend-mint.workers.dev", st.MintURL())

	// Verify MintURLProvider interface.
	var provider MintURLProvider = st
	assert.Equal(t, st.MintURL(), provider.MintURL())
}
