package install

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCFMintWorkerName(t *testing.T) {
	assert.Equal(t, "bt-mint", CFMintWorkerName("bt"))
	assert.Equal(t, "e2e-mint", CFMintWorkerName("e2e"))
}

func TestParseCFMintURLFromOutput(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{
			name:   "standard deploy output",
			output: "Deploying...\n✓ Worker deployed at https://bt-abc12345-bt-mint.fullsend-ai.workers.dev\nDone",
			want:   "https://bt-abc12345-bt-mint.fullsend-ai.workers.dev",
		},
		{
			name:   "durable deploy output",
			output: "✓ Worker deployed at https://bt-mint.fullsend-ai.workers.dev\n",
			want:   "https://bt-mint.fullsend-ai.workers.dev",
		},
		{
			name:   "no url in output",
			output: "Deploy completed without URL line",
			want:   "",
		},
		{
			name:   "trailing punctuation stripped",
			output: "✓ Worker deployed at https://bt-x-mint.sub.workers.dev.",
			want:   "https://bt-x-mint.sub.workers.dev",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ParseCFMintURLFromOutput(tt.output))
		})
	}
}

func TestGenerateCFMintPreviewAlias(t *testing.T) {
	alias, err := GenerateCFMintPreviewAlias()
	require.NoError(t, err)

	// Format: bt-<8-hex-chars>
	assert.True(t, strings.HasPrefix(alias, "bt-"), "alias should start with bt-")
	assert.Len(t, alias, 11, "bt- + 8 hex chars = 11 chars")

	// Must be unique across calls.
	alias2, err := GenerateCFMintPreviewAlias()
	require.NoError(t, err)
	assert.NotEqual(t, alias, alias2, "sequential aliases should differ")
}

func TestNewCFMintDriver_FailsEarly_NoPEMDir(t *testing.T) {
	_, err := newCFMintDriver(nil, "tok", "/bin/fullsend", "", t.Logf, cfmintConfig{
		suiteName: "bt",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PEMDir is required")
}

func TestNewCFMintDriver_FailsEarly_EmptyPEMDir(t *testing.T) {
	dir := t.TempDir()
	_, err := newCFMintDriver(nil, "tok", "/bin/fullsend", "", t.Logf, cfmintConfig{
		pemDir:    dir,
		suiteName: "bt",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no .pem files")
}

func TestNewCFMintDriver_FailsEarly_NoSuiteName(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "fullsend.pem"), []byte("pem"), 0600))

	_, err := newCFMintDriver(nil, "tok", "/bin/fullsend", "", t.Logf, cfmintConfig{
		pemDir: dir,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SuiteName is required")
}

func TestNewCFMintDriver_OK(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "fullsend.pem"), []byte("pem"), 0600))

	d, err := newCFMintDriver(nil, "tok", "/bin/fullsend", "", t.Logf, cfmintConfig{
		pemDir:            dir,
		suiteName:         "bt",
		allowedOrgs:       "",
		perRepoWIFRepos:   "my-org/test-repo-01,my-org/test-repo-02",
		workflowHostRepos: "my-org/test-repo-01,my-org/test-repo-02",
		appSet:            "fullsend-test",
	})
	require.NoError(t, err)
	require.NotNil(t, d)
}

func TestCFMintDeployArgs_WithAppSet(t *testing.T) {
	cfg := cfmintConfig{
		pemDir:            "/tmp/pems",
		suiteName:         "bt",
		allowedOrgs:       "",
		perRepoWIFRepos:   "my-org/test-repo-01",
		workflowHostRepos: "my-org/test-repo-01,my-org/test-repo-02",
		appSet:            "fullsend-test",
	}

	args := CFMintDeployArgs("bt-abc12345", "bt-mint", cfg)

	assert.Contains(t, args, "--app-set")
	for i, a := range args {
		if a == "--app-set" {
			require.Less(t, i+1, len(args), "--app-set must have a value")
			assert.Equal(t, "fullsend-test", args[i+1])
			break
		}
	}
	assert.Contains(t, args, "--pem-dir")
	assert.Contains(t, args, "--allowed-orgs")
	assert.Contains(t, args, "--per-repo-wif-repos")
	assert.Contains(t, args, "--workflow-host-repos")

	for i, a := range args {
		if a == "--allowed-orgs" {
			require.Less(t, i+1, len(args), "--allowed-orgs must have a value")
			assert.Equal(t, "", args[i+1], "--allowed-orgs should be explicit empty for per-repo mode")
			break
		}
	}

	for i, a := range args {
		if a == "--workflow-host-repos" {
			require.Less(t, i+1, len(args), "--workflow-host-repos must have a value")
			assert.Equal(t, "my-org/test-repo-01,my-org/test-repo-02", args[i+1])
			break
		}
	}
}

func TestCFMintDeployArgs_WithoutAppSet(t *testing.T) {
	cfg := cfmintConfig{
		pemDir:            "/tmp/pems",
		suiteName:         "bt",
		allowedOrgs:       "",
		perRepoWIFRepos:   "my-org/test-repo-01",
		workflowHostRepos: "my-org/test-repo-01",
	}

	args := CFMintDeployArgs("bt-abc12345", "bt-mint", cfg)

	assert.NotContains(t, args, "--app-set")
	assert.Contains(t, args, "--pem-dir")
	assert.Contains(t, args, "--allowed-orgs")
	assert.Contains(t, args, "--workflow-host-repos")
}

func TestCFMintTeardownArgs(t *testing.T) {
	args := CFMintTeardownArgs("bt-abc12345", "bt-mint")

	assert.Contains(t, args, "--platform")
	assert.Contains(t, args, "--preview")
	assert.Contains(t, args, "--worker-name")
	assert.Contains(t, args, "--yolo")

	for i, a := range args {
		if a == "--worker-name" {
			require.Less(t, i+1, len(args))
			assert.Equal(t, "bt-mint", args[i+1])
			break
		}
	}

	for i, a := range args {
		if a == "--preview" {
			require.Less(t, i+1, len(args))
			assert.Equal(t, "bt-abc12345", args[i+1])
			break
		}
	}
}

func TestCFMintDriver_Implements_MintDriver(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "fullsend.pem"), []byte("pem"), 0600))

	d, err := newCFMintDriver(nil, "tok", "/bin/fullsend", "", t.Logf, cfmintConfig{
		pemDir:            dir,
		suiteName:         "bt",
		allowedOrgs:       "",
		perRepoWIFRepos:   "org/repo",
		workflowHostRepos: "org/repo",
	})
	require.NoError(t, err)

	var _ mintDriver = d
}

// newTestCFMintDriver creates a cfmintMintDriver with a mock CLI runner
// for unit testing.
func newTestCFMintDriver(cliRunner CLIRunnerFunc) *cfmintMintDriver {
	return &cfmintMintDriver{
		token:      "tok",
		binary:     "/bin/fullsend",
		logf:       func(string, ...any) {},
		cfg:        cfmintConfig{suiteName: "bt"},
		workerName: CFMintWorkerName("bt"),
		cliRunner:  cliRunner,
	}
}

func TestCFMintInstall_Success(t *testing.T) {
	const wantMintURL = "https://bt-abc12345-bt-mint.fullsend-ai.workers.dev"
	d := newTestCFMintDriver(func(_, _ string, _ ...string) (string, error) {
		return "✓ Worker deployed at " + wantMintURL, nil
	})

	mintURL, err := d.Install(context.Background(), "my-org")
	require.NoError(t, err)
	assert.Equal(t, wantMintURL, mintURL)

	// previewAlias should be set for teardown.
	assert.NotEmpty(t, d.previewAlias)
}

func TestCFMintInstall_DeployFailure(t *testing.T) {
	d := newTestCFMintDriver(func(_, _ string, _ ...string) (string, error) {
		return "", fmt.Errorf("deploy exploded")
	})

	mintURL, err := d.Install(context.Background(), "my-org")
	require.Error(t, err)
	assert.Empty(t, mintURL)
	assert.Contains(t, err.Error(), "deploying CF preview mint for BT")
}

func TestCFMintInstall_NoMintURLInOutput(t *testing.T) {
	d := newTestCFMintDriver(func(_, _ string, _ ...string) (string, error) {
		return "Deploying...\nDone", nil
	})

	mintURL, err := d.Install(context.Background(), "my-org")
	require.Error(t, err)
	assert.Empty(t, mintURL)
	assert.Contains(t, err.Error(), "could not parse mint URL")
}

func TestCFMintTeardown_WithPreview(t *testing.T) {
	var calledArgs []string
	d := newTestCFMintDriver(func(_, _ string, args ...string) (string, error) {
		calledArgs = args
		return "", nil
	})
	d.previewAlias = "bt-abc12345"

	err := d.Teardown(context.Background())
	require.NoError(t, err)
	assert.Contains(t, calledArgs, "--preview")
	assert.Contains(t, calledArgs, "bt-abc12345")
}

func TestCFMintTeardown_NoPreview(t *testing.T) {
	called := false
	d := newTestCFMintDriver(func(_, _ string, _ ...string) (string, error) {
		called = true
		return "", nil
	})

	err := d.Teardown(context.Background())
	require.NoError(t, err)
	assert.False(t, called, "CLI should not be called when no preview was deployed")
}

// --- NewRepoPoolCFMintPreviews / buildCFMintDriver tests ---

// testCFMintMintDriver is a fake mintDriver for testing buildCFMintDriver
// without shelling out.
type testCFMintMintDriver struct {
	installMintURL string
	installErr     error
	teardownErr    error
}

func (m *testCFMintMintDriver) Install(_ context.Context, _ string) (string, error) {
	return m.installMintURL, m.installErr
}

func (m *testCFMintMintDriver) Teardown(_ context.Context) error {
	return m.teardownErr
}

func TestBuildCFMintDriver_HappyPath(t *testing.T) {
	mint := &testCFMintMintDriver{
		installMintURL: "https://mint.test",
	}

	d, err := buildCFMintDriver("org", mint, nil, "tok", "/bin/fullsend", "proj", 3, t.Logf)
	require.NoError(t, err)
	require.NotNil(t, d)
	assert.Equal(t, 3, d.Capacity())
}

func TestBuildCFMintDriver_InstallFails(t *testing.T) {
	mint := &testCFMintMintDriver{
		installErr: fmt.Errorf("deploy boom"),
	}

	_, err := buildCFMintDriver("org", mint, nil, "tok", "/bin/fullsend", "proj", 3, t.Logf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cfmint factory: deploying mint")
	assert.Contains(t, err.Error(), "deploy boom")
}

func TestBuildCFMintDriver_EmptyMintURL(t *testing.T) {
	// Install returns empty mint URL — driver should still construct.
	mint := &testCFMintMintDriver{
		installMintURL: "",
	}

	d, err := buildCFMintDriver("org", mint, nil, "tok", "/bin/fullsend", "proj", 2, t.Logf)
	require.NoError(t, err)
	require.NotNil(t, d)
	assert.Equal(t, 2, d.Capacity())
}

func TestBuildCFMintDriver_InvalidPoolSize(t *testing.T) {
	mint := &testCFMintMintDriver{
		installMintURL: "https://mint.test",
	}

	_, err := buildCFMintDriver("org", mint, nil, "tok", "/bin/fullsend", "proj", 0, t.Logf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "capacity must be positive")
}

func TestCFMintTeardown_CLIFailure_ReturnsError(t *testing.T) {
	d := newTestCFMintDriver(func(_, _ string, _ ...string) (string, error) {
		return "", fmt.Errorf("teardown boom")
	})
	d.previewAlias = "bt-abc12345"

	err := d.Teardown(context.Background())
	require.Error(t, err, "teardown failures must be returned so Finalize can join them")
	assert.Contains(t, err.Error(), "preview mint teardown")
	assert.Contains(t, err.Error(), "teardown boom")
}

func TestBuildRepoList(t *testing.T) {
	list := buildRepoList("my-org", 3)
	assert.Equal(t, "my-org/test-repo-01,my-org/test-repo-02,my-org/test-repo-03", list)
}

func TestSetupCFMintPEMDir_NoPEMVars(t *testing.T) {
	// When no TEST_*_PEM env vars are set, returns ("", nil).
	dir, err := setupCFMintPEMDir()
	require.NoError(t, err)
	assert.Empty(t, dir)
}

func TestSetupCFMintPEMDir_MaterializesPEMs(t *testing.T) {
	t.Setenv("TEST_FULLSEND_PEM", "fake-pem-data")
	defer os.Unsetenv("TEST_FULLSEND_PEM")

	dir, err := setupCFMintPEMDir()
	require.NoError(t, err)
	require.NotEmpty(t, dir)
	defer os.RemoveAll(dir)

	data, readErr := os.ReadFile(filepath.Join(dir, "fullsend.pem"))
	require.NoError(t, readErr)
	assert.Equal(t, "fake-pem-data", string(data))
}

func TestEnvPoolSize_Default(t *testing.T) {
	// Without BEHAVIOUR_POOL_SIZE set, should return DefaultPoolSize.
	assert.Equal(t, DefaultPoolSize, envPoolSize())
}
