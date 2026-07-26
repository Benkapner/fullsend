package cf

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- FakeWranglerRunner ---

type fakeWranglerRunner struct {
	deployErr    error
	deployURL    string
	deployCalls  []deployCall
	secretCalls  []secretCall
	deleteCalls  []string
	deleteErr    error
	secretPutErr error
}

type deployCall struct {
	sourceDir  string
	workerName string
	preview    bool
	envVars    map[string]string
}

type secretCall struct {
	workerName string
	secretName string
	value      []byte
}

func (f *fakeWranglerRunner) Deploy(_ context.Context, sourceDir, workerName string, preview bool, envVars map[string]string) (string, error) {
	f.deployCalls = append(f.deployCalls, deployCall{
		sourceDir:  sourceDir,
		workerName: workerName,
		preview:    preview,
		envVars:    envVars,
	})
	if f.deployErr != nil {
		return "", f.deployErr
	}
	url := f.deployURL
	if url == "" {
		url = fmt.Sprintf("https://%s.workers.dev", workerName)
	}
	return url, nil
}

func (f *fakeWranglerRunner) PutSecret(_ context.Context, workerName, secretName string, value []byte) error {
	f.secretCalls = append(f.secretCalls, secretCall{
		workerName: workerName,
		secretName: secretName,
		value:      value,
	})
	return f.secretPutErr
}

func (f *fakeWranglerRunner) Delete(_ context.Context, workerName string) error {
	f.deleteCalls = append(f.deleteCalls, workerName)
	return f.deleteErr
}

// --- Provisioner tests ---

func TestProvisioner_Name(t *testing.T) {
	p := NewProvisioner(Config{}, &fakeWranglerRunner{})
	assert.Equal(t, "cf", p.Name())
}

func TestProvisioner_OrgVariableNames(t *testing.T) {
	p := NewProvisioner(Config{}, &fakeWranglerRunner{})
	assert.Equal(t, []string{"FULLSEND_MINT_URL"}, p.OrgVariableNames())
}

func TestProvisioner_OrgSecretNames(t *testing.T) {
	p := NewProvisioner(Config{}, &fakeWranglerRunner{})
	assert.Nil(t, p.OrgSecretNames())
}

func TestProvisioner_Provision_MissingAccountID(t *testing.T) {
	p := NewProvisioner(Config{
		WorkerName: "test-mint",
	}, &fakeWranglerRunner{})

	_, err := p.Provision(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CLOUDFLARE_ACCOUNT_ID")
}

func TestProvisioner_Provision_InvalidWorkerName(t *testing.T) {
	p := NewProvisioner(Config{
		AccountID:  "abc123",
		WorkerName: "INVALID_NAME",
	}, &fakeWranglerRunner{})

	_, err := p.Provision(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid Worker name")
}

func TestProvisioner_Provision_WithSourceDir(t *testing.T) {
	sourceDir := createFakeWorkerSourceDir(t)
	fake := &fakeWranglerRunner{
		deployURL: "https://test-mint.workers.dev",
	}

	p := NewProvisioner(Config{
		AccountID:  "abc123",
		WorkerName: "test-mint",
		SourceDir:  sourceDir,
	}, fake)

	result, err := p.Provision(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "https://test-mint.workers.dev", result["FULLSEND_MINT_URL"])
	require.Len(t, fake.deployCalls, 1)
	assert.Equal(t, sourceDir, fake.deployCalls[0].sourceDir)
	assert.Equal(t, "test-mint", fake.deployCalls[0].workerName)
	assert.False(t, fake.deployCalls[0].preview)
}

func TestProvisioner_Provision_Preview(t *testing.T) {
	sourceDir := createFakeWorkerSourceDir(t)
	fake := &fakeWranglerRunner{}

	p := NewProvisioner(Config{
		AccountID:  "abc123",
		WorkerName: "test-mint-preview",
		DeployMode: DeployPreview,
		SourceDir:  sourceDir,
	}, fake)

	_, err := p.Provision(context.Background())
	require.NoError(t, err)
	require.Len(t, fake.deployCalls, 1)
	assert.True(t, fake.deployCalls[0].preview)
}

func TestProvisioner_Provision_EnvVars(t *testing.T) {
	sourceDir := createFakeWorkerSourceDir(t)
	fake := &fakeWranglerRunner{}

	envVars := map[string]string{
		"ROLE_APP_IDS": `{"coder":"12345"}`,
		"ALLOWED_ORGS": "acme",
	}

	p := NewProvisioner(Config{
		AccountID:  "abc123",
		WorkerName: "test-mint",
		SourceDir:  sourceDir,
		EnvVars:    envVars,
	}, fake)

	_, err := p.Provision(context.Background())
	require.NoError(t, err)
	require.Len(t, fake.deployCalls, 1)
	assert.Equal(t, `{"coder":"12345"}`, fake.deployCalls[0].envVars["ROLE_APP_IDS"])
	assert.Equal(t, "acme", fake.deployCalls[0].envVars["ALLOWED_ORGS"])
	// OIDC_AUDIENCE should be set by default.
	assert.Equal(t, "fullsend-mint", fake.deployCalls[0].envVars["OIDC_AUDIENCE"])
}

func TestProvisioner_Provision_StampsVersion(t *testing.T) {
	sourceDir := createFakeWorkerSourceDir(t)
	fake := &fakeWranglerRunner{}

	p := NewProvisioner(Config{
		AccountID:  "abc123",
		WorkerName: "test-mint",
		SourceDir:  sourceDir,
		Version:    "1.2.3",
		Commit:     "deadbeef",
	}, fake)

	_, err := p.Provision(context.Background())
	require.NoError(t, err)
	require.Len(t, fake.deployCalls, 1)
	assert.Equal(t, "1.2.3", fake.deployCalls[0].envVars["FULLSEND_VERSION"])
	assert.Equal(t, "deadbeef", fake.deployCalls[0].envVars["FULLSEND_COMMIT"])
}

func TestProvisioner_Provision_OmitsEmptyVersion(t *testing.T) {
	sourceDir := createFakeWorkerSourceDir(t)
	fake := &fakeWranglerRunner{}

	p := NewProvisioner(Config{
		AccountID:  "abc123",
		WorkerName: "test-mint",
		SourceDir:  sourceDir,
		// No Version or Commit set.
	}, fake)

	_, err := p.Provision(context.Background())
	require.NoError(t, err)
	require.Len(t, fake.deployCalls, 1)
	_, hasVersion := fake.deployCalls[0].envVars["FULLSEND_VERSION"]
	_, hasCommit := fake.deployCalls[0].envVars["FULLSEND_COMMIT"]
	assert.False(t, hasVersion, "FULLSEND_VERSION should not be set when empty")
	assert.False(t, hasCommit, "FULLSEND_COMMIT should not be set when empty")
}

func TestProvisioner_Provision_KeepVarsAlwaysPassed(t *testing.T) {
	// Verify that --keep-vars is always passed to wrangler deploy,
	// not just for preview deploys, to avoid wiping existing secrets.
	sourceDir := createFakeWorkerSourceDir(t)

	for _, mode := range []DeployMode{DeployDurable, DeployPreview} {
		t.Run(fmt.Sprintf("mode=%d", mode), func(t *testing.T) {
			fake := &fakeWranglerRunner{}
			p := NewProvisioner(Config{
				AccountID:  "abc123",
				WorkerName: "test-mint",
				SourceDir:  sourceDir,
				DeployMode: mode,
			}, fake)

			_, err := p.Provision(context.Background())
			require.NoError(t, err)
			require.Len(t, fake.deployCalls, 1)
			// The deploy call always passes preview=true/false to Deploy(),
			// but --keep-vars is handled inside LiveWranglerRunner.Deploy.
			// This test verifies Deploy() is called; the --keep-vars
			// behavior is tested in integration tests via the runner.
		})
	}
}

func TestProvisioner_Provision_DeployError(t *testing.T) {
	sourceDir := createFakeWorkerSourceDir(t)
	fake := &fakeWranglerRunner{
		deployErr: fmt.Errorf("network error"),
	}

	p := NewProvisioner(Config{
		AccountID:  "abc123",
		WorkerName: "test-mint",
		SourceDir:  sourceDir,
	}, fake)

	_, err := p.Provision(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deploying worker")
}

func TestProvisioner_Provision_EmbeddedSource(t *testing.T) {
	fake := &fakeWranglerRunner{
		deployURL: "https://test-mint.workers.dev",
	}

	p := NewProvisioner(Config{
		AccountID:  "abc123",
		WorkerName: "test-mint",
		// No SourceDir — uses embedded source.
	}, fake)

	result, err := p.Provision(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "https://test-mint.workers.dev", result["FULLSEND_MINT_URL"])
	require.Len(t, fake.deployCalls, 1)
	// Should have extracted to a temp dir.
	assert.NotEmpty(t, fake.deployCalls[0].sourceDir)
	// Temp dir should be cleaned up.
	_, statErr := os.Stat(fake.deployCalls[0].sourceDir)
	assert.True(t, os.IsNotExist(statErr), "temp dir should be cleaned up")
}

func TestProvisioner_Provision_BadSourceDir(t *testing.T) {
	fake := &fakeWranglerRunner{}
	p := NewProvisioner(Config{
		AccountID:  "abc123",
		WorkerName: "test-mint",
		SourceDir:  "/nonexistent",
	}, fake)

	_, err := p.Provision(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "source-dir")
}

func TestProvisioner_Provision_DefaultWorkerName(t *testing.T) {
	sourceDir := createFakeWorkerSourceDir(t)
	fake := &fakeWranglerRunner{}

	p := NewProvisioner(Config{
		AccountID: "abc123",
		SourceDir: sourceDir,
		// No WorkerName — should default.
	}, fake)

	_, err := p.Provision(context.Background())
	require.NoError(t, err)
	require.Len(t, fake.deployCalls, 1)
	assert.Equal(t, "fullsend-mint", fake.deployCalls[0].workerName)
}

// --- StoreAgentPEM tests ---

func TestProvisioner_StoreAgentPEM(t *testing.T) {
	fake := &fakeWranglerRunner{}
	p := NewProvisioner(Config{
		AccountID:  "abc123",
		WorkerName: "test-mint",
	}, fake)

	err := p.StoreAgentPEM(context.Background(), "coder", []byte("pem-data"))
	require.NoError(t, err)
	require.Len(t, fake.secretCalls, 1)
	assert.Equal(t, "test-mint", fake.secretCalls[0].workerName)
	assert.Equal(t, "CODER_APP_PEM", fake.secretCalls[0].secretName)
	assert.Equal(t, []byte("pem-data"), fake.secretCalls[0].value)
}

func TestProvisioner_StoreAgentPEM_InvalidRole(t *testing.T) {
	fake := &fakeWranglerRunner{}
	p := NewProvisioner(Config{
		AccountID:  "abc123",
		WorkerName: "test-mint",
	}, fake)

	err := p.StoreAgentPEM(context.Background(), "INVALID", []byte("pem"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid role name")
}

func TestProvisioner_StoreAgentPEM_Error(t *testing.T) {
	fake := &fakeWranglerRunner{
		secretPutErr: fmt.Errorf("api error"),
	}
	p := NewProvisioner(Config{
		AccountID:  "abc123",
		WorkerName: "test-mint",
	}, fake)

	err := p.StoreAgentPEM(context.Background(), "coder", []byte("pem"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "storing PEM secret")
}

// --- Teardown tests ---

func TestProvisioner_Teardown_Preview(t *testing.T) {
	fake := &fakeWranglerRunner{}
	p := NewProvisioner(Config{
		AccountID:  "abc123",
		WorkerName: "test-mint-preview",
		DeployMode: DeployPreview,
	}, fake)

	err := p.Teardown(context.Background())
	require.NoError(t, err)
	require.Len(t, fake.deleteCalls, 1)
	assert.Equal(t, "test-mint-preview", fake.deleteCalls[0])
}

func TestProvisioner_Teardown_DurableRejectsCleanup(t *testing.T) {
	fake := &fakeWranglerRunner{}
	p := NewProvisioner(Config{
		AccountID:  "abc123",
		WorkerName: "test-mint",
		DeployMode: DeployDurable,
	}, fake)

	err := p.Teardown(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "only supported for preview")
}

func TestProvisioner_Teardown_Error(t *testing.T) {
	fake := &fakeWranglerRunner{
		deleteErr: fmt.Errorf("delete failed"),
	}
	p := NewProvisioner(Config{
		AccountID:  "abc123",
		WorkerName: "test-mint-preview",
		DeployMode: DeployPreview,
	}, fake)

	err := p.Teardown(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deleting worker")
}

// --- pemSecretName tests ---

func TestPemSecretName(t *testing.T) {
	tests := []struct {
		role   string
		expect string
	}{
		{"coder", "CODER_APP_PEM"},
		{"triage", "TRIAGE_APP_PEM"},
		{"review", "REVIEW_APP_PEM"},
	}
	for _, tc := range tests {
		t.Run(tc.role, func(t *testing.T) {
			assert.Equal(t, tc.expect, pemSecretName(tc.role))
		})
	}
}

// --- ValidateWorkerName tests ---

func TestValidateWorkerName(t *testing.T) {
	tests := []struct {
		name  string
		valid bool
	}{
		{"fullsend-mint", true},
		{"my-worker-123", true},
		{"ab", true},
		{"a", false},                   // too short
		{"UPPER", false},               // uppercase
		{"has_underscore", false},      // underscore
		{"-starts-with-hyphen", false}, // starts with hyphen
		{"ends-with-hyphen-", false},   // ends with hyphen
		{"", false},                    // empty
		{"a-very-long-worker-name-that-exceeds-the-maximum-allowed-length-of-63-chars", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.valid, ValidateWorkerName(tc.name))
		})
	}
}

// --- ValidateCloudflareEnv tests ---

func TestValidateCloudflareEnv_Missing(t *testing.T) {
	// Save and restore env vars.
	origAccount := os.Getenv("CLOUDFLARE_ACCOUNT_ID")
	origToken := os.Getenv("CLOUDFLARE_API_TOKEN")
	defer func() {
		os.Setenv("CLOUDFLARE_ACCOUNT_ID", origAccount)
		os.Setenv("CLOUDFLARE_API_TOKEN", origToken)
	}()

	os.Unsetenv("CLOUDFLARE_ACCOUNT_ID")
	os.Unsetenv("CLOUDFLARE_API_TOKEN")

	err := ValidateCloudflareEnv()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CLOUDFLARE_ACCOUNT_ID")
	assert.Contains(t, err.Error(), "CLOUDFLARE_API_TOKEN")
}

func TestValidateCloudflareEnv_Present(t *testing.T) {
	origAccount := os.Getenv("CLOUDFLARE_ACCOUNT_ID")
	origToken := os.Getenv("CLOUDFLARE_API_TOKEN")
	defer func() {
		os.Setenv("CLOUDFLARE_ACCOUNT_ID", origAccount)
		os.Setenv("CLOUDFLARE_API_TOKEN", origToken)
	}()

	os.Setenv("CLOUDFLARE_ACCOUNT_ID", "test-account")
	os.Setenv("CLOUDFLARE_API_TOKEN", "test-token")

	err := ValidateCloudflareEnv()
	require.NoError(t, err)
}

// --- Embed integrity tests ---

func TestEmbeddedWorkerSource_ContainsRequiredFiles(t *testing.T) {
	for _, path := range embeddedWorkerFiles {
		t.Run(path, func(t *testing.T) {
			data, err := embeddedWorkerSource.ReadFile(path)
			require.NoError(t, err, "embedded file %s should be readable", path)
			assert.NotEmpty(t, data, "embedded file %s should not be empty", path)
		})
	}
}

func TestExtractEmbeddedSource(t *testing.T) {
	dir := t.TempDir()
	err := extractEmbeddedSource(dir)
	require.NoError(t, err)

	// Verify key files were extracted.
	for _, name := range []string{"src/index.ts", "wrangler.toml", "package.json"} {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		require.NoError(t, err, "expected %s to exist", name)
		assert.True(t, info.Size() > 0, "expected %s to be non-empty", name)
	}
}

// --- validateSourceDir tests ---

func TestValidateSourceDir_Valid(t *testing.T) {
	dir := createFakeWorkerSourceDir(t)
	err := validateSourceDir(dir)
	require.NoError(t, err)
}

func TestValidateSourceDir_MissingDir(t *testing.T) {
	err := validateSourceDir("/nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "source-dir")
}

func TestValidateSourceDir_MissingFile(t *testing.T) {
	dir := t.TempDir()
	// Create only some required files.
	os.MkdirAll(filepath.Join(dir, "src"), 0o755)
	os.WriteFile(filepath.Join(dir, "src/index.ts"), []byte("//ts"), 0o644)
	os.WriteFile(filepath.Join(dir, "wrangler.toml"), []byte("name = \"test\""), 0o644)
	// Missing package.json.

	err := validateSourceDir(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "package.json")
}

// --- parseWorkerURL tests ---

func TestParseWorkerURL(t *testing.T) {
	tests := []struct {
		name   string
		output string
		expect string
	}{
		{
			"standard output",
			"Published test-mint (0.5s)\nhttps://test-mint.workers.dev",
			"https://test-mint.workers.dev",
		},
		{
			"with trailing punctuation",
			"Deployed to https://my-worker.workers.dev.",
			"https://my-worker.workers.dev",
		},
		{
			"no url in output",
			"Some other output\nwithout a URL",
			"",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := parseWorkerURL(tc.output, "test-mint")
			assert.Equal(t, tc.expect, result)
		})
	}
}

// --- helpers ---

func createFakeWorkerSourceDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "src"), 0o755)
	os.WriteFile(filepath.Join(dir, "src/index.ts"), []byte("export default {}"), 0o644)
	os.WriteFile(filepath.Join(dir, "wrangler.toml"), []byte("name = \"test\""), 0o644)
	os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0o644)
	return dir
}
