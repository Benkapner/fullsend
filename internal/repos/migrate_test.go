package repos

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/fullsend-ai/fullsend/internal/forge"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeProvisioner implements InferenceProvisioner for tests.
type fakeProvisioner struct {
	mu sync.Mutex
	// statusResults maps "owner/repo" to WIF provider name (empty = not provisioned).
	statusResults map[string]string
	// statusErrors maps "owner/repo" to an error returned by Status.
	statusErrors map[string]error
	// provisionResults maps "owner/repo" to WIF provider name.
	provisionResults map[string]string
	// provisionErrors maps "owner/repo" to an error returned by Provision.
	provisionErrors map[string]error
	// provisionCalls tracks which repos were provisioned.
	provisionCalls []string
}

func newFakeProvisioner() *fakeProvisioner {
	return &fakeProvisioner{
		statusResults:    make(map[string]string),
		statusErrors:     make(map[string]error),
		provisionResults: make(map[string]string),
		provisionErrors:  make(map[string]error),
	}
}

func (p *fakeProvisioner) Status(_ context.Context, owner, repo string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := owner + "/" + repo
	if err, ok := p.statusErrors[key]; ok {
		return "", err
	}
	return p.statusResults[key], nil
}

func (p *fakeProvisioner) Provision(_ context.Context, owner, repo string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := owner + "/" + repo
	p.provisionCalls = append(p.provisionCalls, key)
	if err, ok := p.provisionErrors[key]; ok {
		return "", err
	}
	return p.provisionResults[key], nil
}

func nopScaffoldCommit(_ context.Context, _, _ string, _ []forge.TreeFile, _ bool) error {
	return nil
}

// --- Migrate: basic validation ---

func TestMigrate_EmptyOrg_ReturnsError(t *testing.T) {
	_, err := Migrate(context.Background(), MigrateConfig{
		Project: "my-project",
	}, newTestClientFactory(forge.NewFakeClient()), newFakeProvisioner(), nopScaffoldCommit, nopProgress)

	assert.Error(t, err)
	assert.ErrorContains(t, err, "org is required")
}

func TestMigrate_EmptyProject_ReturnsError(t *testing.T) {
	_, err := Migrate(context.Background(), MigrateConfig{
		Org: "acme",
	}, newTestClientFactory(forge.NewFakeClient()), newFakeProvisioner(), nopScaffoldCommit, nopProgress)

	assert.Error(t, err)
	assert.ErrorContains(t, err, "project is required")
}

func TestMigrate_NoConfigRepo_ReturnsError(t *testing.T) {
	fc := forge.NewFakeClient()

	_, err := Migrate(context.Background(), MigrateConfig{
		Org:     "acme",
		Project: "my-project",
	}, newTestClientFactory(fc), newFakeProvisioner(), nopScaffoldCommit, nopProgress)

	assert.Error(t, err)
	assert.ErrorContains(t, err, "nothing to migrate")
}

func TestMigrate_NoEnabledRepos_ReturnsEmpty(t *testing.T) {
	fc := forge.NewFakeClient()
	setOrgConfig(fc, "acme", `
version: "1"
dispatch:
  platform: github-actions
repos:
  api:
    enabled: false
`)

	result, err := Migrate(context.Background(), MigrateConfig{
		Org:     "acme",
		Project: "my-project",
	}, newTestClientFactory(fc), newFakeProvisioner(), nopScaffoldCommit, nopProgress)

	require.NoError(t, err)
	assert.Empty(t, result.Migrated)
	assert.Empty(t, result.Failed)
}

// --- Migrate: full migration ---

func TestMigrate_FullMigration(t *testing.T) {
	fc := forge.NewFakeClient()
	setOrgConfig(fc, "acme", `
version: "1"
dispatch:
  platform: github-actions
  mode: oidc-mint
  mint_url: https://mint.example.com
repos:
  api:
    enabled: true
  web:
    enabled: true
  lib:
    enabled: true
`)
	setWorkflowFile(fc, "acme", "api",
		"    uses: fullsend-ai/fullsend/.github/workflows/reusable-dispatch.yml@v2.1.0")
	setWorkflowFile(fc, "acme", "web",
		"    uses: fullsend-ai/fullsend/.github/workflows/reusable-dispatch.yml@v2.1.0")
	setWorkflowFile(fc, "acme", "lib",
		"    uses: fullsend-ai/fullsend/.github/workflows/reusable-dispatch.yml@v2.1.0")

	prov := newFakeProvisioner()
	for _, repo := range []string{"acme/api", "acme/web", "acme/lib"} {
		prov.provisionResults[repo] = "projects/123456789/locations/global/workloadIdentityPools/fullsend-inference/providers/prov-" + repo[len("acme/"):]
	}

	result, err := Migrate(context.Background(), MigrateConfig{
		Org:            "acme",
		Project:        "my-project",
		MaxConcurrency: 2,
		CLIVersion:     "2.0.0",
	}, newTestClientFactory(fc), prov, nopScaffoldCommit, nopProgress)

	require.NoError(t, err)
	assert.Len(t, result.Migrated, 3)
	assert.Empty(t, result.Failed)
	assert.Equal(t, 3, result.Unenrolled)
	assert.NotNil(t, result.Manifest)
}

// --- Migrate: idempotent re-run ---

func TestMigrate_IdempotentRerun_SkipsAlreadyInstalled(t *testing.T) {
	fc := forge.NewFakeClient()
	setOrgConfig(fc, "acme", `
version: "1"
dispatch:
  platform: github-actions
  mode: oidc-mint
  mint_url: https://mint.example.com
repos:
  api:
    enabled: true
  web:
    enabled: true
`)
	// api is already per-repo installed.
	setRepoVars(fc, "acme", "api", map[string]string{
		forge.PerRepoGuardVar: "true",
		"FULLSEND_MINT_URL":   "https://mint.example.com",
		"FULLSEND_GCP_REGION": "us-central1",
	})
	setWorkflowFile(fc, "acme", "api",
		"    uses: fullsend-ai/fullsend/.github/workflows/reusable-dispatch.yml@v2.1.0")
	// web is per-org only.
	setWorkflowFile(fc, "acme", "web",
		"    uses: fullsend-ai/fullsend/.github/workflows/reusable-dispatch.yml@v2.1.0")

	prov := newFakeProvisioner()
	prov.provisionResults["acme/web"] = "projects/123/locations/global/workloadIdentityPools/inference/providers/prov"

	result, err := Migrate(context.Background(), MigrateConfig{
		Org:     "acme",
		Project: "my-project",
	}, newTestClientFactory(fc), prov, nopScaffoldCommit, nopProgress)

	require.NoError(t, err)
	assert.Len(t, result.Skipped, 1, "api should be skipped")
	assert.Equal(t, "api", result.Skipped[0].Repo)
	assert.Len(t, result.Migrated, 1, "web should be migrated")
	assert.Equal(t, "web", result.Migrated[0].Repo)
}

// --- Migrate: pre-provisioned inference ---

func TestMigrate_PreProvisioned_ReusesExistingWIF(t *testing.T) {
	fc := forge.NewFakeClient()
	setOrgConfig(fc, "acme", `
version: "1"
dispatch:
  platform: github-actions
  mode: oidc-mint
  mint_url: https://mint.example.com
repos:
  api:
    enabled: true
`)
	setWorkflowFile(fc, "acme", "api",
		"    uses: fullsend-ai/fullsend/.github/workflows/reusable-dispatch.yml@v2.1.0")

	prov := newFakeProvisioner()
	prov.statusResults["acme/api"] = "projects/123/locations/global/workloadIdentityPools/inference/providers/existing"

	result, err := Migrate(context.Background(), MigrateConfig{
		Org:     "acme",
		Project: "my-project",
	}, newTestClientFactory(fc), prov, nopScaffoldCommit, nopProgress)

	require.NoError(t, err)
	assert.Len(t, result.Migrated, 1)
	assert.Empty(t, prov.provisionCalls, "should not provision when WIF already exists")
}

// --- Migrate: partial failure ---

func TestMigrate_PartialFailure_DoesNotAbortBatch(t *testing.T) {
	fc := forge.NewFakeClient()
	setOrgConfig(fc, "acme", `
version: "1"
dispatch:
  platform: github-actions
  mode: oidc-mint
  mint_url: https://mint.example.com
repos:
  api:
    enabled: true
  web:
    enabled: true
`)
	setWorkflowFile(fc, "acme", "api",
		"    uses: fullsend-ai/fullsend/.github/workflows/reusable-dispatch.yml@v2.1.0")
	setWorkflowFile(fc, "acme", "web",
		"    uses: fullsend-ai/fullsend/.github/workflows/reusable-dispatch.yml@v2.1.0")

	prov := newFakeProvisioner()
	prov.provisionErrors["acme/api"] = fmt.Errorf("GCP permission denied")
	prov.provisionResults["acme/web"] = "projects/123/locations/global/workloadIdentityPools/inference/providers/prov"

	result, err := Migrate(context.Background(), MigrateConfig{
		Org:            "acme",
		Project:        "my-project",
		MaxConcurrency: 1,
	}, newTestClientFactory(fc), prov, nopScaffoldCommit, nopProgress)

	require.NoError(t, err)
	assert.Len(t, result.Failed, 1, "api should have failed")
	assert.Len(t, result.Migrated, 1, "web should have succeeded")
	assert.Equal(t, 1, result.Unenrolled, "only web should be unenrolled")
}

// --- Migrate: dry-run ---

func TestMigrate_DryRun_NoSideEffects(t *testing.T) {
	fc := forge.NewFakeClient()
	setOrgConfig(fc, "acme", `
version: "1"
dispatch:
  platform: github-actions
  mode: oidc-mint
  mint_url: https://mint.example.com
repos:
  api:
    enabled: true
`)
	setWorkflowFile(fc, "acme", "api",
		"    uses: fullsend-ai/fullsend/.github/workflows/reusable-dispatch.yml@v2.1.0")

	prov := newFakeProvisioner()

	result, err := Migrate(context.Background(), MigrateConfig{
		Org:     "acme",
		Project: "my-project",
		DryRun:  true,
	}, newTestClientFactory(fc), prov, nopScaffoldCommit, nopProgress)

	require.NoError(t, err)
	assert.Len(t, result.Migrated, 1)
	assert.Empty(t, prov.provisionCalls, "dry-run should not provision")
	assert.Equal(t, 0, result.Unenrolled, "dry-run should not unenroll")
	assert.NotNil(t, result.Manifest)
}

// --- Migrate: subset --repo filter ---

func TestMigrate_RepoFilter_OnlyMigratesSpecified(t *testing.T) {
	fc := forge.NewFakeClient()
	setOrgConfig(fc, "acme", `
version: "1"
dispatch:
  platform: github-actions
  mode: oidc-mint
  mint_url: https://mint.example.com
repos:
  api:
    enabled: true
  web:
    enabled: true
  lib:
    enabled: true
`)
	for _, name := range []string{"api", "web", "lib"} {
		setWorkflowFile(fc, "acme", name,
			"    uses: fullsend-ai/fullsend/.github/workflows/reusable-dispatch.yml@v2.1.0")
	}

	prov := newFakeProvisioner()
	prov.provisionResults["acme/api"] = "projects/123/locations/global/workloadIdentityPools/inference/providers/prov-api"

	result, err := Migrate(context.Background(), MigrateConfig{
		Org:        "acme",
		Project:    "my-project",
		RepoFilter: []string{"api"},
	}, newTestClientFactory(fc), prov, nopScaffoldCommit, nopProgress)

	require.NoError(t, err)
	assert.Len(t, result.Migrated, 1)
	assert.Equal(t, "api", result.Migrated[0].Repo)
}

func TestMigrate_RepoFilter_WithOrgPrefix(t *testing.T) {
	fc := forge.NewFakeClient()
	setOrgConfig(fc, "acme", `
version: "1"
dispatch:
  platform: github-actions
  mode: oidc-mint
  mint_url: https://mint.example.com
repos:
  api:
    enabled: true
  web:
    enabled: true
`)
	for _, name := range []string{"api", "web"} {
		setWorkflowFile(fc, "acme", name,
			"    uses: fullsend-ai/fullsend/.github/workflows/reusable-dispatch.yml@v2.1.0")
	}

	prov := newFakeProvisioner()
	prov.provisionResults["acme/web"] = "projects/123/locations/global/workloadIdentityPools/inference/providers/prov"

	result, err := Migrate(context.Background(), MigrateConfig{
		Org:        "acme",
		Project:    "my-project",
		RepoFilter: []string{"acme/web"},
	}, newTestClientFactory(fc), prov, nopScaffoldCommit, nopProgress)

	require.NoError(t, err)
	assert.Len(t, result.Migrated, 1)
	assert.Equal(t, "web", result.Migrated[0].Repo)
}

func TestMigrate_RepoFilter_GlobPattern(t *testing.T) {
	fc := forge.NewFakeClient()
	setOrgConfig(fc, "acme", `
version: "1"
dispatch:
  platform: github-actions
  mode: oidc-mint
  mint_url: https://mint.example.com
repos:
  api-v1:
    enabled: true
  api-v2:
    enabled: true
  web:
    enabled: true
`)
	for _, name := range []string{"api-v1", "api-v2", "web"} {
		setWorkflowFile(fc, "acme", name,
			"    uses: fullsend-ai/fullsend/.github/workflows/reusable-dispatch.yml@v2.1.0")
	}

	prov := newFakeProvisioner()
	for _, repo := range []string{"acme/api-v1", "acme/api-v2"} {
		prov.provisionResults[repo] = "projects/123/locations/global/workloadIdentityPools/inference/providers/prov"
	}

	result, err := Migrate(context.Background(), MigrateConfig{
		Org:        "acme",
		Project:    "my-project",
		RepoFilter: []string{"api-*"},
	}, newTestClientFactory(fc), prov, nopScaffoldCommit, nopProgress)

	require.NoError(t, err)
	assert.Len(t, result.Migrated, 2)
}

// --- Migrate: manifest generation ---

func TestMigrate_GeneratesManifest(t *testing.T) {
	fc := forge.NewFakeClient()
	setOrgConfig(fc, "acme", `
version: "1"
dispatch:
  platform: github-actions
  mode: oidc-mint
  mint_url: https://mint.example.com
repos:
  api:
    enabled: true
`)
	setWorkflowFile(fc, "acme", "api",
		"    uses: fullsend-ai/fullsend/.github/workflows/reusable-dispatch.yml@v2.1.0")

	prov := newFakeProvisioner()
	prov.provisionResults["acme/api"] = "projects/123/locations/global/workloadIdentityPools/inference/providers/prov"

	result, err := Migrate(context.Background(), MigrateConfig{
		Org:        "acme",
		Project:    "my-project",
		CLIVersion: "2.0.0",
	}, newTestClientFactory(fc), prov, nopScaffoldCommit, nopProgress)

	require.NoError(t, err)
	require.NotNil(t, result.Manifest)
	assert.Equal(t, 1, result.Manifest.Version)
	assert.Equal(t, "https://mint.example.com", result.Manifest.Forge.GitHub.MintURL)
	require.Len(t, result.Manifest.Repos, 1)
	assert.Equal(t, "acme/api", result.Manifest.Repos[0].Repo)
}

// --- filterEnrolledRepos tests ---

func TestFilterEnrolledRepos_ExactMatch(t *testing.T) {
	result := filterEnrolledRepos(
		[]string{"api", "web", "lib"},
		"acme",
		[]string{"api", "web"},
	)
	assert.Equal(t, []string{"api", "web"}, result)
}

func TestFilterEnrolledRepos_WithOrgPrefix(t *testing.T) {
	result := filterEnrolledRepos(
		[]string{"api", "web", "lib"},
		"acme",
		[]string{"acme/api"},
	)
	assert.Equal(t, []string{"api"}, result)
}

func TestFilterEnrolledRepos_GlobPattern(t *testing.T) {
	result := filterEnrolledRepos(
		[]string{"api-v1", "api-v2", "web"},
		"acme",
		[]string{"api-*"},
	)
	assert.Equal(t, []string{"api-v1", "api-v2"}, result)
}

func TestFilterEnrolledRepos_NoMatch(t *testing.T) {
	result := filterEnrolledRepos(
		[]string{"api", "web"},
		"acme",
		[]string{"nonexistent"},
	)
	assert.Empty(t, result)
}

// --- matchGlob tests ---

func TestMatchGlob(t *testing.T) {
	tests := []struct {
		pattern string
		name    string
		want    bool
	}{
		{"api*", "api-v1", true},
		{"api*", "api", true},
		{"api*", "web", false},
		{"*api", "my-api", true},
		{"*api", "api", true},
		{"*api", "apix", false},
		{"a?i", "api", true},
		{"a?i", "axi", true},
		{"a?i", "ai", false},
		{"api-v?", "api-v1", true},
		{"api-v?", "api-v10", false},
		{"a*b*c", "abc", true},
		{"a*b*c", "aXbYc", true},
		{"a*b*c", "aXbY", false},
		{"*", "anything", true},
		{"*", "", true},
		{"", "", true},
		{"", "x", false},
		{"a?c", "ac", false},
		{"[api]", "[api]", true},
	}
	for _, tt := range tests {
		t.Run(tt.pattern+"_"+tt.name, func(t *testing.T) {
			got, err := matchGlob(tt.pattern, tt.name)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// --- Migrate: unenroll error propagation ---

func TestMigrate_UnenrollWriteError_SetsUnenrollError(t *testing.T) {
	fc := forge.NewFakeClient()
	setOrgConfig(fc, "acme", `
version: "1"
dispatch:
  platform: github-actions
  mode: oidc-mint
  mint_url: https://mint.example.com
repos:
  api:
    enabled: true
`)
	setWorkflowFile(fc, "acme", "api",
		"    uses: fullsend-ai/fullsend/.github/workflows/reusable-dispatch.yml@v2.1.0")

	prov := newFakeProvisioner()
	prov.provisionResults["acme/api"] = "projects/123/locations/global/workloadIdentityPools/inference/providers/prov"

	fc.Errors["CreateOrUpdateFile"] = fmt.Errorf("simulated write failure")

	result, err := Migrate(context.Background(), MigrateConfig{
		Org:     "acme",
		Project: "my-project",
	}, newTestClientFactory(fc), prov, nopScaffoldCommit, nopProgress)

	require.NoError(t, err)
	assert.Len(t, result.Migrated, 1)
	assert.NotNil(t, result.UnenrollError, "should surface unenroll write error")
	assert.Contains(t, result.UnenrollError.Error(), "writing org config")
	assert.Equal(t, 0, result.Unenrolled)
}

// --- Migrate: status error propagation ---

func TestMigrate_StatusError_SetsStatusError(t *testing.T) {
	fc := forge.NewFakeClient()
	setOrgConfig(fc, "acme", `
version: "1"
dispatch:
  platform: github-actions
  mode: oidc-mint
  mint_url: https://mint.example.com
repos:
  api:
    enabled: true
`)
	setWorkflowFile(fc, "acme", "api",
		"    uses: fullsend-ai/fullsend/.github/workflows/reusable-dispatch.yml@v2.1.0")

	prov := newFakeProvisioner()
	prov.statusErrors["acme/api"] = fmt.Errorf("GCP status check failed")
	prov.provisionResults["acme/api"] = "projects/123/locations/global/workloadIdentityPools/inference/providers/prov"

	result, err := Migrate(context.Background(), MigrateConfig{
		Org:     "acme",
		Project: "my-project",
	}, newTestClientFactory(fc), prov, nopScaffoldCommit, nopProgress)

	require.NoError(t, err)
	assert.Len(t, result.Migrated, 1)
	assert.NotNil(t, result.Migrated[0].StatusError)
	assert.Contains(t, result.Migrated[0].StatusError.Error(), "GCP status check failed")
}

// --- filterEnrolledRepos: cross-org matching ---

func TestFilterEnrolledRepos_CrossOrgDoesNotMatch(t *testing.T) {
	result := filterEnrolledRepos(
		[]string{"api", "web"},
		"acme",
		[]string{"wrong-org/api"},
	)
	assert.Empty(t, result, "wrong-org/api should not match acme's repos")
}

func TestFilterEnrolledRepos_CorrectOrgDoesMatch(t *testing.T) {
	result := filterEnrolledRepos(
		[]string{"api", "web"},
		"acme",
		[]string{"acme/api"},
	)
	assert.Equal(t, []string{"api"}, result)
}

func TestFilterEnrolledRepos_GlobWithOrgPrefix(t *testing.T) {
	result := filterEnrolledRepos(
		[]string{"api-v1", "api-v2", "web"},
		"acme",
		[]string{"acme/api-*"},
	)
	assert.Equal(t, []string{"api-v1", "api-v2"}, result)
}

func TestFilterEnrolledRepos_BracketTreatedLiterally(t *testing.T) {
	result := filterEnrolledRepos(
		[]string{"api-1", "api-2", "api-[12]"},
		"acme",
		[]string{"api-[12]"},
	)
	assert.Equal(t, []string{"api-[12]"}, result, "bracket should be treated as literal, not glob")
}

// --- Migrate: concurrency clamping ---

func TestMigrate_MaxConcurrencyClamped(t *testing.T) {
	fc := forge.NewFakeClient()
	setOrgConfig(fc, "acme", `
version: "1"
dispatch:
  platform: github-actions
  mode: oidc-mint
  mint_url: https://mint.example.com
repos:
  api:
    enabled: true
`)
	setWorkflowFile(fc, "acme", "api",
		"    uses: fullsend-ai/fullsend/.github/workflows/reusable-dispatch.yml@v2.1.0")

	prov := newFakeProvisioner()
	prov.provisionResults["acme/api"] = "projects/123/locations/global/workloadIdentityPools/inference/providers/prov"

	result, err := Migrate(context.Background(), MigrateConfig{
		Org:            "acme",
		Project:        "my-project",
		MaxConcurrency: 100,
	}, newTestClientFactory(fc), prov, nopScaffoldCommit, nopProgress)

	require.NoError(t, err)
	assert.Len(t, result.Migrated, 1)
}

func TestMigrate_NilProgress(t *testing.T) {
	fc := forge.NewFakeClient()
	setOrgConfig(fc, "acme", `
version: "1"
dispatch:
  platform: github-actions
repos:
  api:
    enabled: false
`)

	result, err := Migrate(context.Background(), MigrateConfig{
		Org:     "acme",
		Project: "my-project",
	}, newTestClientFactory(fc), newFakeProvisioner(), nopScaffoldCommit, nil)

	require.NoError(t, err)
	assert.Empty(t, result.Migrated)
}

func TestMigrate_RepoFilterNoMatch_ReturnsEmpty(t *testing.T) {
	fc := forge.NewFakeClient()
	setOrgConfig(fc, "acme", `
version: "1"
dispatch:
  platform: github-actions
repos:
  api:
    enabled: true
`)

	result, err := Migrate(context.Background(), MigrateConfig{
		Org:        "acme",
		Project:    "my-project",
		RepoFilter: []string{"nonexistent"},
	}, newTestClientFactory(fc), newFakeProvisioner(), nopScaffoldCommit, nopProgress)

	require.NoError(t, err)
	assert.Empty(t, result.Migrated)
}

func TestMigrate_AllAlreadyInstalled_GeneratesManifest(t *testing.T) {
	fc := forge.NewFakeClient()
	setOrgConfig(fc, "acme", `
version: "1"
dispatch:
  platform: github-actions
  mode: oidc-mint
  mint_url: https://mint.example.com
repos:
  api:
    enabled: true
`)
	setRepoVars(fc, "acme", "api", map[string]string{
		forge.PerRepoGuardVar: "true",
		"FULLSEND_MINT_URL":   "https://mint.example.com",
		"FULLSEND_GCP_REGION": "us-central1",
	})
	setWorkflowFile(fc, "acme", "api",
		"    uses: fullsend-ai/fullsend/.github/workflows/reusable-dispatch.yml@v2.1.0")

	result, err := Migrate(context.Background(), MigrateConfig{
		Org:     "acme",
		Project: "my-project",
	}, newTestClientFactory(fc), newFakeProvisioner(), nopScaffoldCommit, nopProgress)

	require.NoError(t, err)
	assert.Len(t, result.Skipped, 1)
	assert.Empty(t, result.Migrated)
	assert.NotNil(t, result.Manifest, "should generate manifest even when nothing to migrate")
	assert.Equal(t, 1, result.Unenrolled, "should unenroll skipped repos still enabled in org config")
}
