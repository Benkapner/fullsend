package install

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fullsend-ai/fullsend/pkg/e2etest"
)

func TestCFMintConfig_Enabled(t *testing.T) {
	tests := []struct {
		name    string
		pemDir  string
		enabled bool
	}{
		{"empty PEM dir", "", false},
		{"non-empty PEM dir", "/tmp/pems", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := cfMintConfig{PEMDir: tt.pemDir}
			assert.Equal(t, tt.enabled, cfg.enabled())
		})
	}
}

func TestCFMintConfig_EffectiveWorkerName(t *testing.T) {
	tests := []struct {
		name       string
		workerName string
		want       string
	}{
		{"default", "", "fullsend-mint"},
		{"custom", "my-worker", "my-worker"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := cfMintConfig{WorkerName: tt.workerName}
			assert.Equal(t, tt.want, cfg.effectiveWorkerName())
		})
	}
}

func TestCFMintConfig_PreviewMintURL(t *testing.T) {
	tests := []struct {
		name       string
		workerName string
		alias      string
		want       string
	}{
		{
			name:  "default worker",
			alias: "bt-abc12345",
			want:  "https://bt-abc12345-fullsend-mint.workers.dev",
		},
		{
			name:       "custom worker",
			workerName: "my-mint",
			alias:      "bt-deadbeef",
			want:       "https://bt-deadbeef-my-mint.workers.dev",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := cfMintConfig{WorkerName: tt.workerName}
			assert.Equal(t, tt.want, cfg.previewMintURL(tt.alias))
		})
	}
}

func TestNewCFMintConfig(t *testing.T) {
	e2eCfg := e2etest.EnvConfig{
		CFMintPEMDir:               "/ci/pems",
		CFMintAllowedOrgs:          "test-org",
		CFMintPerRepoWIFRepos:      "test-org/repo-a,test-org/repo-b",
		CFMintWorkerName:           "custom-mint",
		CFMintAppSet:               "test-apps",
		CFMintRoles:                "triage,coder",
		CFMintWorkflowHostRepos:    "org/repo",
		CFMintAllowedWorkflowFiles: "dispatch.yml",
	}

	cfg := newCFMintConfig(e2eCfg)
	assert.Equal(t, "/ci/pems", cfg.PEMDir)
	assert.Equal(t, "test-org", cfg.AllowedOrgs)
	assert.Equal(t, "test-org/repo-a,test-org/repo-b", cfg.PerRepoWIFRepos)
	assert.Equal(t, "custom-mint", cfg.WorkerName)
	assert.Equal(t, "test-apps", cfg.AppSet)
	assert.Equal(t, "triage,coder", cfg.Roles)
	assert.Equal(t, "org/repo", cfg.WorkflowHostRepos)
	assert.Equal(t, "dispatch.yml", cfg.AllowedWorkflowFiles)
	assert.True(t, cfg.enabled())
}

func TestNewCFMintConfig_Disabled(t *testing.T) {
	cfg := newCFMintConfig(e2etest.EnvConfig{})
	assert.False(t, cfg.enabled())
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

func TestDeployCFMint_FullArgs(t *testing.T) {
	// Verify that deployCFMint passes the correct CLI arguments.
	var capturedArgs []string
	stubCLI := func(binary, token string, args ...string) (string, error) {
		capturedArgs = args
		return "", nil
	}

	cfg := cfMintConfig{
		PEMDir:               "/ci/pems",
		AllowedOrgs:          "my-org",
		PerRepoWIFRepos:      "my-org/repo-a",
		WorkerName:           "custom-mint",
		AppSet:               "test-apps",
		Roles:                "triage,coder",
		WorkflowHostRepos:    "fullsend-ai/fullsend",
		AllowedWorkflowFiles: "dispatch.yml",
	}

	url, err := deployCFMint("/bin/fullsend", "tok", cfg, "bt-test1234", "my-org", stubCLI, t.Logf)
	require.NoError(t, err)
	assert.Equal(t, "https://bt-test1234-custom-mint.workers.dev", url)

	// Verify key arguments.
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
	assert.Contains(t, capturedArgs, "my-org/repo-a")
	assert.Contains(t, capturedArgs, "--worker-name")
	assert.Contains(t, capturedArgs, "custom-mint")
	assert.Contains(t, capturedArgs, "--app-set")
	assert.Contains(t, capturedArgs, "test-apps")
	assert.Contains(t, capturedArgs, "--roles")
	assert.Contains(t, capturedArgs, "triage,coder")
	assert.Contains(t, capturedArgs, "--workflow-host-repos")
	assert.Contains(t, capturedArgs, "fullsend-ai/fullsend")
	assert.Contains(t, capturedArgs, "--allowed-workflow-files")
	assert.Contains(t, capturedArgs, "dispatch.yml")
}

func TestDeployCFMint_MinimalArgs(t *testing.T) {
	// When optional fields are empty, only required flags are passed.
	var capturedArgs []string
	stubCLI := func(binary, token string, args ...string) (string, error) {
		capturedArgs = args
		return "", nil
	}

	cfg := cfMintConfig{PEMDir: "/ci/pems"}
	url, err := deployCFMint("/bin/fullsend", "tok", cfg, "bt-abcd0123", "pool-org", stubCLI, t.Logf)
	require.NoError(t, err)
	assert.Equal(t, "https://bt-abcd0123-fullsend-mint.workers.dev", url)

	// Required flags present.
	assert.Contains(t, capturedArgs, "--pem-dir")
	assert.Contains(t, capturedArgs, "--allowed-orgs")
	assert.Contains(t, capturedArgs, "pool-org")
	assert.Contains(t, capturedArgs, "--per-repo-wif-repos")

	// Optional flags absent.
	for _, flag := range []string{"--worker-name", "--app-set", "--roles", "--workflow-host-repos", "--allowed-workflow-files"} {
		assert.NotContains(t, capturedArgs, flag,
			"optional flag %s should not be present when empty", flag)
	}
}

func TestDeployCFMint_AutoGeneratesPerRepoWIFRepos(t *testing.T) {
	// When PerRepoWIFRepos is empty, deployCFMint auto-generates from org.
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

func TestDeployCFMint_DefaultsAllowedOrgsToOrg(t *testing.T) {
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
	assert.Equal(t, "halfsend-03", allowedOrgs, "should default to the acquired org")
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

	cfg := cfMintConfig{WorkerName: "custom-mint"}
	err := teardownCFMint("/bin/fullsend", "tok", cfg, "bt-tear0000", stubCLI, t.Logf)
	require.NoError(t, err)

	assert.Contains(t, capturedArgs, "mint")
	assert.Contains(t, capturedArgs, "delete")
	assert.Contains(t, capturedArgs, "--platform")
	assert.Contains(t, capturedArgs, "cloudflare")
	assert.Contains(t, capturedArgs, "--preview")
	assert.Contains(t, capturedArgs, "bt-tear0000")
	assert.Contains(t, capturedArgs, "--yolo")
	assert.Contains(t, capturedArgs, "--worker-name")
	assert.Contains(t, capturedArgs, "custom-mint")
}

func TestTeardownCFMint_DefaultWorkerName(t *testing.T) {
	var capturedArgs []string
	stubCLI := func(binary, token string, args ...string) (string, error) {
		capturedArgs = args
		return "", nil
	}

	cfg := cfMintConfig{}
	err := teardownCFMint("/bin/fullsend", "tok", cfg, "bt-tear0001", stubCLI, t.Logf)
	require.NoError(t, err)

	assert.NotContains(t, capturedArgs, "--worker-name",
		"should not pass --worker-name when empty (CLI uses default)")
}

func TestTeardownCFMint_Error(t *testing.T) {
	stubCLI := func(binary, token string, args ...string) (string, error) {
		return "", fmt.Errorf("delete failed")
	}

	cfg := cfMintConfig{}
	err := teardownCFMint("/bin/fullsend", "tok", cfg, "bt-err00001", stubCLI, t.Logf)
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

func TestPerRepoState_MintURL_Legacy(t *testing.T) {
	// When no CF mint is used, MintURL holds the configured value.
	st := &perRepoState{
		org:     "org",
		repo:    "repo",
		mintURL: "https://hosted-mint.example.com",
	}
	assert.Equal(t, "https://hosted-mint.example.com", st.MintURL())
}
