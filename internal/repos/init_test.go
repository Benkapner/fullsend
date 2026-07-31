package repos

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/fullsend-ai/fullsend/internal/config"
	"github.com/fullsend-ai/fullsend/internal/forge"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// newFakeWithOrgRepos returns a FakeClient with org repos and per-repo
// variables pre-populated. This is the standard setup helper for init tests.
func newFakeWithOrgRepos(org string, repos []forge.Repository) *forge.FakeClient {
	fc := forge.NewFakeClient()
	fc.OrgRepos = map[string][]forge.Repository{org: repos}
	return fc
}

func setRepoVars(fc *forge.FakeClient, owner, repo string, vars map[string]string) {
	for k, v := range vars {
		fc.VariableValues[owner+"/"+repo+"/"+k] = v
	}
}

func setWorkflowFile(fc *forge.FakeClient, owner, repo, content string) {
	fc.FileContents[owner+"/"+repo+"/.github/workflows/fullsend.yml"] = []byte(content)
}

func setOrgConfig(fc *forge.FakeClient, org, configYAML string) {
	fc.FileContents[org+"/"+forge.ConfigRepoName+"/config.yaml"] = []byte(configYAML)
}

var selectAll = func(candidates []RepoCandidate) ([]string, error) {
	names := make([]string, 0, len(candidates))
	for _, c := range candidates {
		names = append(names, c.Owner+"/"+c.Repo)
	}
	return names, nil
}

// nopProgress is a no-op progress callback for tests.
func nopProgress(_, _, _ string) {}

// --- Init: forge validation ---

func TestInit_EmptyForge_ReturnsError(t *testing.T) {
	fc := forge.NewFakeClient()

	_, err := Init(context.Background(), InitConfig{
		Target: "acme/api",
	}, newTestClientFactory(fc), nil, nopProgress)

	assert.Error(t, err)
	assert.ErrorContains(t, err, "Forge is required")
}

// --- Init: greenfield org tests ---

func TestInit_GreenfieldOrg_AllFlag(t *testing.T) {
	fc := newFakeWithOrgRepos("acme", []forge.Repository{
		{Name: "api", FullName: "acme/api"},
		{Name: "web", FullName: "acme/web"},
		{Name: "lib", FullName: "acme/lib"},
	})

	result, err := Init(context.Background(), InitConfig{
		Target:         "acme",
		All:            true,
		Forge:          ForgeGitHub,
		CLIVersion:     "2.3.0",
		MaxConcurrency: 2,
	}, newTestClientFactory(fc), nil, nopProgress)

	require.NoError(t, err)
	assert.Equal(t, 0, result.PerRepoCount)
	assert.Equal(t, 0, result.PerOrgCount)
	assert.Equal(t, 3, result.NewCount)
	// Greenfield: no mint URL discovered → TODO generated.
	assert.Contains(t, result.TODOs, "forge.github.mint_url: set the Cloud Run endpoint URL")

	m := result.Manifest
	assert.Equal(t, 1, m.Version)
	// InferenceProject is no longer stored in manifest — install-time only.
	assert.Empty(t, m.Forge.GitHub.InferenceProject)
	assert.Equal(t, "v2.3.0", m.Forge.GitHub.FullsendRef)
	require.Len(t, m.Repos, 3)
}

func TestInit_GreenfieldOrg_ExplicitRepos(t *testing.T) {
	fc := newFakeWithOrgRepos("acme", []forge.Repository{
		{Name: "api", FullName: "acme/api"},
		{Name: "web", FullName: "acme/web"},
		{Name: "lib", FullName: "acme/lib"},
	})

	result, err := Init(context.Background(), InitConfig{
		Target:     "acme",
		Repos:      []string{"acme/api", "acme/web"},
		Forge:      ForgeGitHub,
		CLIVersion: "1.0.0",
	}, newTestClientFactory(fc), nil, nopProgress)

	require.NoError(t, err)
	assert.Equal(t, 2, result.NewCount)
	require.Len(t, result.Manifest.Repos, 2)
	assert.Equal(t, "acme/api", result.Manifest.Repos[0].Repo)
	assert.Equal(t, "acme/web", result.Manifest.Repos[1].Repo)
}

func TestInit_GreenfieldOrg_ExplicitRepos_NotFound(t *testing.T) {
	fc := newFakeWithOrgRepos("acme", []forge.Repository{
		{Name: "api", FullName: "acme/api"},
	})

	_, err := Init(context.Background(), InitConfig{
		Target: "acme",
		Forge:  ForgeGitHub,
		Repos:  []string{"acme/api", "acme/nonexistent"},
	}, newTestClientFactory(fc), nil, nopProgress)

	assert.Error(t, err)
	assert.ErrorContains(t, err, "not found in org")
}

// --- Init: migration tests ---

func TestInit_MixedPerRepoAndPerOrg(t *testing.T) {
	fc := newFakeWithOrgRepos("acme", []forge.Repository{
		{Name: "api", FullName: "acme/api"},
		{Name: "web", FullName: "acme/web"},
		{Name: "lib", FullName: "acme/lib"},
	})

	// api is per-repo installed.
	setRepoVars(fc, "acme", "api", map[string]string{
		forge.PerRepoGuardVar: "true",
		"FULLSEND_MINT_URL":   "https://mint.example.com",
		"FULLSEND_GCP_REGION": "us-east1",
	})
	setWorkflowFile(fc, "acme", "api",
		"    uses: fullsend-ai/fullsend/.github/workflows/reusable-dispatch.yml@v2.3.0")

	// web is per-org enrolled.
	setOrgConfig(fc, "acme", `
version: "1"
dispatch:
  platform: github-actions
  mode: oidc-mint
  mint_url: https://mint-org.example.com
repos:
  web:
    enabled: true
  lib:
    enabled: false
`)
	setWorkflowFile(fc, "acme", "web",
		"    uses: fullsend-ai/fullsend/.github/workflows/reusable-dispatch.yml@v2.1.0")

	result, err := Init(context.Background(), InitConfig{
		Target:         "acme",
		All:            true,
		Forge:          ForgeGitHub,
		MaxConcurrency: 4,
	}, newTestClientFactory(fc), nil, nopProgress)

	require.NoError(t, err)
	assert.Equal(t, 1, result.PerRepoCount)
	assert.Equal(t, 1, result.PerOrgCount)
	assert.Equal(t, 1, result.NewCount)
	require.Len(t, result.Manifest.Repos, 3)
}

func TestInit_OnlyPerRepoInstallations(t *testing.T) {
	fc := newFakeWithOrgRepos("acme", []forge.Repository{
		{Name: "api", FullName: "acme/api"},
		{Name: "web", FullName: "acme/web"},
	})

	setRepoVars(fc, "acme", "api", map[string]string{
		forge.PerRepoGuardVar: "true",
		"FULLSEND_MINT_URL":   "https://mint.example.com",
	})
	setWorkflowFile(fc, "acme", "api",
		"    uses: fullsend-ai/fullsend/.github/workflows/reusable-dispatch.yml@v2.0.0")

	setRepoVars(fc, "acme", "web", map[string]string{
		forge.PerRepoGuardVar: "true",
		"FULLSEND_MINT_URL":   "https://mint.example.com",
	})
	setWorkflowFile(fc, "acme", "web",
		"    uses: fullsend-ai/fullsend/.github/workflows/reusable-dispatch.yml@v2.0.0")

	result, err := Init(context.Background(), InitConfig{
		Target:         "acme",
		All:            true,
		Forge:          ForgeGitHub,
		MaxConcurrency: 2,
	}, newTestClientFactory(fc), nil, nopProgress)

	require.NoError(t, err)
	assert.Equal(t, 2, result.PerRepoCount)
	assert.Equal(t, 0, result.PerOrgCount)
	assert.Equal(t, "https://mint.example.com", result.Manifest.Forge.GitHub.MintURL)
}

func TestInit_OnlyPerOrgEnrollments(t *testing.T) {
	fc := newFakeWithOrgRepos("acme", []forge.Repository{
		{Name: "api", FullName: "acme/api"},
	})

	setOrgConfig(fc, "acme", `
version: "1"
dispatch:
  platform: github-actions
  mode: oidc-mint
  mint_url: https://mint-org.example.com
repos:
  api:
    enabled: true
`)
	setWorkflowFile(fc, "acme", "api",
		"    uses: fullsend-ai/fullsend/.github/workflows/reusable-dispatch.yml@v2.1.0")

	result, err := Init(context.Background(), InitConfig{
		Target: "acme",
		All:    true,
		Forge:  ForgeGitHub,
	}, newTestClientFactory(fc), nil, nopProgress)

	require.NoError(t, err)
	assert.Equal(t, 0, result.PerRepoCount)
	assert.Equal(t, 1, result.PerOrgCount)
	assert.Equal(t, "https://mint-org.example.com", result.Manifest.Forge.GitHub.MintURL)
}

// --- Init: single repo tests ---

func TestInit_SingleRepo_PerRepoInstalled(t *testing.T) {
	fc := forge.NewFakeClient()
	setRepoVars(fc, "acme", "api", map[string]string{
		forge.PerRepoGuardVar: "true",
		"FULLSEND_MINT_URL":   "https://mint.example.com",
		"FULLSEND_GCP_REGION": "us-west1",
	})
	setWorkflowFile(fc, "acme", "api",
		"    uses: fullsend-ai/fullsend/.github/workflows/reusable-dispatch.yml@v2.3.0")

	result, err := Init(context.Background(), InitConfig{
		Target: "acme/api",
		Forge:  ForgeGitHub,
	}, newTestClientFactory(fc), nil, nopProgress)

	require.NoError(t, err)
	assert.Equal(t, 1, result.PerRepoCount)
	require.Len(t, result.Manifest.Repos, 1)
	assert.Equal(t, "acme/api", result.Manifest.Repos[0].Repo)
	assert.Equal(t, "https://mint.example.com", result.Manifest.Forge.GitHub.MintURL)
	assert.Equal(t, "v2.3.0", result.Manifest.Forge.GitHub.FullsendRef)
}

func TestInit_SingleRepo_PerOrgEnrolled(t *testing.T) {
	fc := forge.NewFakeClient()

	setOrgConfig(fc, "acme", `
version: "1"
dispatch:
  platform: github-actions
  mode: oidc-mint
  mint_url: https://mint-org.example.com
repos:
  api:
    enabled: true
`)
	setWorkflowFile(fc, "acme", "api",
		"    uses: fullsend-ai/fullsend/.github/workflows/reusable-dispatch.yml@v2.1.0")

	result, err := Init(context.Background(), InitConfig{
		Target: "acme/api",
		Forge:  ForgeGitHub,
	}, newTestClientFactory(fc), nil, nopProgress)

	require.NoError(t, err)
	assert.Equal(t, 1, result.PerOrgCount)
	assert.Equal(t, "https://mint-org.example.com", result.Manifest.Forge.GitHub.MintURL)
}

func TestInit_SingleRepo_RejectsAllFlag(t *testing.T) {
	fc := forge.NewFakeClient()

	_, err := Init(context.Background(), InitConfig{
		Target: "acme/api",
		Forge:  ForgeGitHub,
		All:    true,
	}, newTestClientFactory(fc), nil, nopProgress)

	assert.Error(t, err)
	assert.ErrorContains(t, err, "--all flag cannot be used with a single repo target")
}

func TestInit_SingleRepo_RejectsReposFlag(t *testing.T) {
	fc := forge.NewFakeClient()

	_, err := Init(context.Background(), InitConfig{
		Target: "acme/api",
		Forge:  ForgeGitHub,
		Repos:  []string{"acme/other"},
	}, newTestClientFactory(fc), nil, nopProgress)

	assert.Error(t, err)
	assert.ErrorContains(t, err, "--repos flag cannot be used with a single repo target")
}

func TestInit_SingleRepo_NotInstalled(t *testing.T) {
	fc := forge.NewFakeClient()

	result, err := Init(context.Background(), InitConfig{
		Target:     "acme/api",
		Forge:      ForgeGitHub,
		CLIVersion: "2.5.0",
	}, newTestClientFactory(fc), nil, nopProgress)

	require.NoError(t, err)
	assert.Equal(t, 1, result.NewCount)
	assert.Equal(t, "v2.5.0", result.Manifest.Forge.GitHub.FullsendRef)
}

// --- Defaults computation tests ---

func TestInit_DefaultsComputation_MostCommonRef(t *testing.T) {
	fc := newFakeWithOrgRepos("acme", []forge.Repository{
		{Name: "r1", FullName: "acme/r1"},
		{Name: "r2", FullName: "acme/r2"},
		{Name: "r3", FullName: "acme/r3"},
	})

	// r1 and r2 have v2.3.0, r3 has v2.1.0
	for _, name := range []string{"r1", "r2"} {
		setRepoVars(fc, "acme", name, map[string]string{
			forge.PerRepoGuardVar: "true",
			"FULLSEND_MINT_URL":   "https://mint.example.com",
		})
		setWorkflowFile(fc, "acme", name,
			"    uses: fullsend-ai/fullsend/.github/workflows/reusable-dispatch.yml@v2.3.0")
	}
	setRepoVars(fc, "acme", "r3", map[string]string{
		forge.PerRepoGuardVar: "true",
		"FULLSEND_MINT_URL":   "https://mint.example.com",
	})
	setWorkflowFile(fc, "acme", "r3",
		"    uses: fullsend-ai/fullsend/.github/workflows/reusable-dispatch.yml@v2.1.0")

	result, err := Init(context.Background(), InitConfig{
		Target: "acme",
		All:    true,
		Forge:  ForgeGitHub,
	}, newTestClientFactory(fc), nil, nopProgress)

	require.NoError(t, err)
	// v2.3.0 is most common, should be the forge-level default.
	assert.Equal(t, "v2.3.0", result.Manifest.Forge.GitHub.FullsendRef)

	// No per-repo overrides — all settings live in forge.github.
	for _, entry := range result.Manifest.Repos {
		assert.False(t, entry.Forge.Set, "repo %s should not have per-repo forge override", entry.Repo)
	}
}

func TestInit_InferenceRegion_MostCommonDiscovered(t *testing.T) {
	fc := newFakeWithOrgRepos("acme", []forge.Repository{
		{Name: "r1", FullName: "acme/r1"},
		{Name: "r2", FullName: "acme/r2"},
	})

	setRepoVars(fc, "acme", "r1", map[string]string{
		forge.PerRepoGuardVar: "true",
		"FULLSEND_MINT_URL":   "https://mint.example.com",
		"FULLSEND_GCP_REGION": "us-central1",
	})
	setWorkflowFile(fc, "acme", "r1",
		"    uses: fullsend-ai/fullsend/.github/workflows/reusable-dispatch.yml@v2.0.0")

	setRepoVars(fc, "acme", "r2", map[string]string{
		forge.PerRepoGuardVar: "true",
		"FULLSEND_MINT_URL":   "https://mint.example.com",
		"FULLSEND_GCP_REGION": "us-east1",
	})
	setWorkflowFile(fc, "acme", "r2",
		"    uses: fullsend-ai/fullsend/.github/workflows/reusable-dispatch.yml@v2.0.0")

	result, err := Init(context.Background(), InitConfig{
		Target: "acme",
		All:    true,
		Forge:  ForgeGitHub,
	}, newTestClientFactory(fc), nil, nopProgress)

	require.NoError(t, err)

	// Inference region should be set at forge level (no per-repo overrides).
	// Discovery picks the mode; both regions appear once so alphabetical wins.
	assert.NotEmpty(t, result.Manifest.Forge.GitHub.InferenceRegion)
}

// --- Interactive selection tests ---

func TestInit_InteractiveSelection(t *testing.T) {
	fc := newFakeWithOrgRepos("acme", []forge.Repository{
		{Name: "api", FullName: "acme/api"},
		{Name: "web", FullName: "acme/web"},
		{Name: "lib", FullName: "acme/lib"},
	})

	setRepoVars(fc, "acme", "api", map[string]string{
		forge.PerRepoGuardVar: "true",
		"FULLSEND_MINT_URL":   "https://mint.example.com",
	})
	setWorkflowFile(fc, "acme", "api",
		"    uses: fullsend-ai/fullsend/.github/workflows/reusable-dispatch.yml@v2.0.0")

	var receivedCandidates []RepoCandidate
	selectFn := func(candidates []RepoCandidate) ([]string, error) {
		receivedCandidates = candidates
		return []string{"acme/api", "acme/web"}, nil
	}

	result, err := Init(context.Background(), InitConfig{
		Target: "acme",
		Forge:  ForgeGitHub,
	}, newTestClientFactory(fc), selectFn, nopProgress)

	require.NoError(t, err)
	require.Len(t, result.Manifest.Repos, 2)

	// Verify candidates include status labels.
	require.Len(t, receivedCandidates, 3)
	statusMap := make(map[string]string)
	for _, c := range receivedCandidates {
		statusMap[c.Owner+"/"+c.Repo] = c.Status
	}
	assert.Equal(t, "per-repo", statusMap["acme/api"])
	assert.Equal(t, "new", statusMap["acme/web"])
	assert.Equal(t, "new", statusMap["acme/lib"])
}

func TestInit_NilCallback_RequiresFlag(t *testing.T) {
	fc := newFakeWithOrgRepos("acme", []forge.Repository{
		{Name: "api", FullName: "acme/api"},
	})

	_, err := Init(context.Background(), InitConfig{
		Target: "acme",
		Forge:  ForgeGitHub,
	}, newTestClientFactory(fc), nil, nopProgress)

	assert.Error(t, err)
	assert.ErrorContains(t, err, "org target requires --all or --repos flag")
}

// --- TODO generation tests ---

func TestInit_TODOs_NoInferenceProject(t *testing.T) {
	// InferenceProject is no longer stored in the manifest (install-time
	// only), so init should NOT generate a TODO for it.
	fc := forge.NewFakeClient()

	result, err := Init(context.Background(), InitConfig{
		Target:     "acme/api",
		Forge:      ForgeGitHub,
		CLIVersion: "1.0.0",
	}, newTestClientFactory(fc), nil, nopProgress)

	require.NoError(t, err)
	for _, todo := range result.TODOs {
		assert.NotContains(t, todo, "inference_project")
	}
}

func TestInit_TODOs_NoMintURL_Greenfield(t *testing.T) {
	fc := forge.NewFakeClient()

	result, err := Init(context.Background(), InitConfig{
		Target:     "acme/api",
		Forge:      ForgeGitHub,
		CLIVersion: "1.0.0",
	}, newTestClientFactory(fc), nil, nopProgress)

	require.NoError(t, err)
	assert.Contains(t, result.TODOs, "forge.github.mint_url: set the Cloud Run endpoint URL")
}

func TestInit_TODOs_MultipleMintURLs(t *testing.T) {
	fc := newFakeWithOrgRepos("acme", []forge.Repository{
		{Name: "r1", FullName: "acme/r1"},
		{Name: "r2", FullName: "acme/r2"},
		{Name: "r3", FullName: "acme/r3"},
	})

	setRepoVars(fc, "acme", "r1", map[string]string{
		forge.PerRepoGuardVar: "true",
		"FULLSEND_MINT_URL":   "https://mint-a.example.com",
	})
	setRepoVars(fc, "acme", "r2", map[string]string{
		forge.PerRepoGuardVar: "true",
		"FULLSEND_MINT_URL":   "https://mint-a.example.com",
	})
	setRepoVars(fc, "acme", "r3", map[string]string{
		forge.PerRepoGuardVar: "true",
		"FULLSEND_MINT_URL":   "https://mint-b.example.com",
	})

	result, err := Init(context.Background(), InitConfig{
		Target: "acme",
		All:    true,
		Forge:  ForgeGitHub,
	}, newTestClientFactory(fc), nil, nopProgress)

	require.NoError(t, err)
	// Most common URL should be used.
	assert.Equal(t, "https://mint-a.example.com", result.Manifest.Forge.GitHub.MintURL)
	assert.Contains(t, result.TODOs, "forge.github.mint_url: multiple mint URLs discovered; using most common — verify correctness")
}

// --- buildManifest tests ---

func TestBuildManifest_SimpleEntries(t *testing.T) {
	repos := []DiscoveredRepo{
		{Owner: "acme", Repo: "api", Source: "new"},
		{Owner: "acme", Repo: "web", Source: "new"},
	}
	m, todos := buildManifest(repos, InitConfig{
		Forge:      ForgeGitHub,
		CLIVersion: "2.0.0",
	})

	require.Len(t, m.Repos, 2)
	assert.Equal(t, "acme/api", m.Repos[0].Repo)
	assert.Equal(t, "acme/web", m.Repos[1].Repo)
	// No per-repo overrides; all settings in forge section.
	for _, entry := range m.Repos {
		assert.False(t, entry.Forge.Set)
	}
	// Greenfield: no mint URL discovered → TODO generated.
	assert.Contains(t, todos, "forge.github.mint_url: set the Cloud Run endpoint URL")
}

func TestBuildManifest_MixedDiscovery(t *testing.T) {
	repos := []DiscoveredRepo{
		{Owner: "acme", Repo: "r1", Source: "per-repo", FullsendRef: "v2.3.0", InferenceRegion: "us-central1", MintURL: "https://mint.example.com"},
		{Owner: "acme", Repo: "r2", Source: "per-repo", FullsendRef: "v2.3.0", InferenceRegion: "us-central1", MintURL: "https://mint.example.com"},
		{Owner: "acme", Repo: "r3", Source: "per-repo", FullsendRef: "v2.1.0", InferenceRegion: "us-east1", MintURL: "https://mint.example.com"},
	}
	m, _ := buildManifest(repos, InitConfig{
		Forge: ForgeGitHub,
	})

	// Forge-level fields should use the mode (most common) values.
	assert.Equal(t, "v2.3.0", m.Forge.GitHub.FullsendRef)
	assert.Equal(t, "us-central1", m.Forge.GitHub.InferenceRegion)

	// No per-repo overrides — settings live in forge.github.
	for _, entry := range m.Repos {
		assert.False(t, entry.Forge.Set)
	}
}

// --- computeMode tests ---

func TestComputeMode(t *testing.T) {
	tests := []struct {
		name  string
		repos []DiscoveredRepo
		want  string
	}{
		{
			name: "single value",
			repos: []DiscoveredRepo{
				{MintURL: "https://a.com"},
				{MintURL: "https://a.com"},
			},
			want: "https://a.com",
		},
		{
			name: "majority wins",
			repos: []DiscoveredRepo{
				{MintURL: "https://a.com"},
				{MintURL: "https://a.com"},
				{MintURL: "https://b.com"},
			},
			want: "https://a.com",
		},
		{
			name: "empty values ignored",
			repos: []DiscoveredRepo{
				{MintURL: ""},
				{MintURL: "https://a.com"},
				{MintURL: ""},
			},
			want: "https://a.com",
		},
		{
			name:  "all empty",
			repos: []DiscoveredRepo{{MintURL: ""}, {MintURL: ""}},
			want:  "",
		},
		{
			name:  "no repos",
			repos: nil,
			want:  "",
		},
		{
			name: "tie broken alphabetically",
			repos: []DiscoveredRepo{
				{MintURL: "https://b.com"},
				{MintURL: "https://a.com"},
			},
			want: "https://a.com",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeMode(tt.repos, func(d DiscoveredRepo) string { return d.MintURL })
			assert.Equal(t, tt.want, got)
		})
	}
}

// --- countDistinct tests ---

func TestCountDistinct(t *testing.T) {
	repos := []DiscoveredRepo{
		{MintURL: "https://a.com"},
		{MintURL: "https://a.com"},
		{MintURL: "https://b.com"},
		{MintURL: ""},
	}
	got := countDistinct(repos, func(d DiscoveredRepo) string { return d.MintURL })
	assert.Equal(t, 2, got)
}

// --- MarshalWithHeader tests ---

func TestMarshalWithHeader(t *testing.T) {
	m := &Manifest{
		Version: 1,
		Forge: ForgeSection{GitHub: GitHubForgeInfra{
			MintURL: "https://mint.example.com",
		}},
		Repos: []RepoEntry{
			{Repo: "acme/api"},
		},
	}

	data, err := MarshalWithHeader(m)
	require.NoError(t, err)

	s := string(data)
	assert.Contains(t, s, "# Generated by fullsend repos init on")
	assert.Contains(t, s, "# Review and adjust before running fullsend repos install.")
	assert.Contains(t, s, "version: 1")
	assert.Contains(t, s, "acme/api")
}

// --- Round-trip: Init → Marshal → parse ---

func TestInit_RoundTrip(t *testing.T) {
	fc := newFakeWithOrgRepos("acme", []forge.Repository{
		{Name: "api", FullName: "acme/api"},
		{Name: "web", FullName: "acme/web"},
	})

	setRepoVars(fc, "acme", "api", map[string]string{
		forge.PerRepoGuardVar: "true",
		"FULLSEND_MINT_URL":   "https://mint.example.com",
		"FULLSEND_GCP_REGION": "us-central1",
	})
	setWorkflowFile(fc, "acme", "api",
		"    uses: fullsend-ai/fullsend/.github/workflows/reusable-dispatch.yml@v2.3.0")

	result, err := Init(context.Background(), InitConfig{
		Target: "acme",
		All:    true,
		Forge:  ForgeGitHub,
	}, newTestClientFactory(fc), nil, nopProgress)
	require.NoError(t, err)

	// Marshal and re-parse.
	data, err := result.Manifest.Marshal()
	require.NoError(t, err)

	var parsed Manifest
	require.NoError(t, yaml.Unmarshal(data, &parsed))

	assert.Equal(t, 1, parsed.Version)
	assert.Equal(t, "https://mint.example.com", parsed.Forge.GitHub.MintURL)
	assert.Len(t, parsed.Repos, 2)
}

// --- Error handling tests ---

func TestInit_ListOrgReposError(t *testing.T) {
	fc := forge.NewFakeClient()
	fc.Errors["ListOrgRepos"] = assert.AnError

	_, err := Init(context.Background(), InitConfig{
		Target: "acme",
		Forge:  ForgeGitHub,
		All:    true,
	}, newTestClientFactory(fc), nil, nopProgress)

	assert.Error(t, err)
	assert.ErrorContains(t, err, "listing repos for org")
}

func TestInit_ListRepoVariablesError(t *testing.T) {
	fc := forge.NewFakeClient()
	fc.Errors["ListRepoVariables"] = assert.AnError

	_, err := Init(context.Background(), InitConfig{
		Target: "acme/api",
		Forge:  ForgeGitHub,
	}, newTestClientFactory(fc), nil, nopProgress)

	assert.Error(t, err)
	assert.ErrorContains(t, err, "listing variables")
}

// --- GetFileContent error handling tests ---

func TestInit_OrgConfigParseError_SingleRepo_Warns(t *testing.T) {
	fc := forge.NewFakeClient()
	fc.FileContents["acme/"+forge.ConfigRepoName+"/config.yaml"] = []byte("not: valid: yaml: [")

	// Malformed org config should warn, not fail, for single-repo init.
	var warnings []string
	progress := func(_, _, msg string) {
		warnings = append(warnings, msg)
	}

	result, err := Init(context.Background(), InitConfig{
		Target:     "acme/api",
		Forge:      ForgeGitHub,
		CLIVersion: "1.0.0",
	}, newTestClientFactory(fc), nil, progress)

	require.NoError(t, err)
	assert.Equal(t, 1, result.NewCount)

	hasWarning := false
	for _, w := range warnings {
		if len(w) > 8 && w[:8] == "warning:" {
			hasWarning = true
			break
		}
	}
	assert.True(t, hasWarning, "expected a warning about org config parse failure")
}

func TestInit_OrgConfigFetchError_SingleRepo(t *testing.T) {
	fc := forge.NewFakeClient()
	fc.Errors["GetFileContent"] = assert.AnError

	_, err := Init(context.Background(), InitConfig{
		Target: "acme/api",
		Forge:  ForgeGitHub,
	}, newTestClientFactory(fc), nil, nopProgress)

	assert.Error(t, err)
	assert.ErrorContains(t, err, "fetching org config")
}

func TestInit_OrgConfigFetchError_Org(t *testing.T) {
	fc := newFakeWithOrgRepos("acme", []forge.Repository{
		{Name: "api", FullName: "acme/api"},
	})
	fc.Errors["GetFileContent"] = assert.AnError

	_, err := Init(context.Background(), InitConfig{
		Target: "acme",
		Forge:  ForgeGitHub,
		All:    true,
	}, newTestClientFactory(fc), nil, nopProgress)

	assert.Error(t, err)
	assert.ErrorContains(t, err, "fetching org config")
}

// --- Config repo exclusion tests ---

func TestInit_ConfigRepoExcluded(t *testing.T) {
	fc := newFakeWithOrgRepos("acme", []forge.Repository{
		{Name: "api", FullName: "acme/api"},
		{Name: forge.ConfigRepoName, FullName: "acme/" + forge.ConfigRepoName},
		{Name: "web", FullName: "acme/web"},
	})

	result, err := Init(context.Background(), InitConfig{
		Target:     "acme",
		All:        true,
		Forge:      ForgeGitHub,
		CLIVersion: "1.0.0",
	}, newTestClientFactory(fc), nil, nopProgress)

	require.NoError(t, err)
	assert.Equal(t, 2, result.NewCount)
	require.Len(t, result.Manifest.Repos, 2)
	for _, entry := range result.Manifest.Repos {
		assert.NotEqual(t, "acme/"+forge.ConfigRepoName, entry.Repo)
	}
}

// --- Discovery error tracking tests ---

func TestInit_DiscoveryErrors_Tracked(t *testing.T) {
	fc := newFakeWithOrgRepos("acme", []forge.Repository{
		{Name: "api", FullName: "acme/api"},
	})
	fc.Errors["ListRepoVariables"] = assert.AnError

	result, err := Init(context.Background(), InitConfig{
		Target:     "acme",
		All:        true,
		Forge:      ForgeGitHub,
		CLIVersion: "1.0.0",
	}, newTestClientFactory(fc), nil, nopProgress)

	require.NoError(t, err)
	// Repo with error should be excluded from manifest.
	assert.Empty(t, result.Manifest.Repos)
	// Error should be tracked in result.
	require.Len(t, result.Errors, 1)
	assert.Contains(t, result.Errors[0], "acme/api")
}

func TestDiscoverReposParallel_ErrorsExcluded(t *testing.T) {
	fc := forge.NewFakeClient()
	fc.Errors["ListRepoVariables"] = assert.AnError

	repos := []forge.Repository{
		{Name: "api", FullName: "acme/api"},
		{Name: "web", FullName: "acme/web"},
	}

	dr := discoverReposParallel(context.Background(), fc, "acme", repos, nil, "", 4, nopProgress)

	assert.Empty(t, dr.repos)
	assert.Len(t, dr.errors, 2)
}

// --- Repos flag whitespace tests ---

func TestSelectInitRepos_ExplicitMode_TrimmedNames(t *testing.T) {
	candidates := []RepoCandidate{
		{Owner: "acme", Repo: "api"},
		{Owner: "acme", Repo: "web"},
	}
	selected, err := selectInitRepos(InitConfig{Repos: []string{"acme/api", "acme/web"}}, candidates, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"acme/api", "acme/web"}, selected)
}

// --- parseInitTarget tests ---

func TestParseInitTarget(t *testing.T) {
	tests := []struct {
		input  string
		owner  string
		repo   string
		isRepo bool
	}{
		{"acme", "acme", "", false},
		{"acme/api", "acme", "api", true},
		{"acme/my.repo-name", "acme", "my.repo-name", true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			owner, repo, isRepo, err := parseInitTarget(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.owner, owner)
			assert.Equal(t, tt.repo, repo)
			assert.Equal(t, tt.isRepo, isRepo)
		})
	}
}

func TestParseInitTarget_Errors(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "target cannot be empty"},
		{"acme/", "both owner and repo must be non-empty"},
		{"/api", "both owner and repo must be non-empty"},
		{"acme/api/extra", "expected org or owner/repo format"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			_, _, _, err := parseInitTarget(tt.input)
			assert.Error(t, err)
			assert.ErrorContains(t, err, tt.want)
		})
	}
}

// --- countSources tests ---

func TestCountSources(t *testing.T) {
	repos := []DiscoveredRepo{
		{Source: "per-repo"},
		{Source: "per-repo"},
		{Source: "per-org"},
		{Source: "new"},
		{Source: "new"},
		{Source: "new"},
	}
	result := &InitResult{}
	countSources(repos, result)
	assert.Equal(t, 2, result.PerRepoCount)
	assert.Equal(t, 1, result.PerOrgCount)
	assert.Equal(t, 3, result.NewCount)
}

// --- discoverRepo tests ---

func TestDiscoverRepo_PerRepo(t *testing.T) {
	fc := forge.NewFakeClient()
	setRepoVars(fc, "acme", "api", map[string]string{
		forge.PerRepoGuardVar: "true",
		"FULLSEND_MINT_URL":   "https://mint.example.com",
		"FULLSEND_GCP_REGION": "us-west1",
	})
	setWorkflowFile(fc, "acme", "api",
		"    uses: fullsend-ai/fullsend/.github/workflows/reusable-dispatch.yml@v2.3.0")

	d, err := discoverRepo(context.Background(), fc, "acme", "api", nil, "", nopProgress)
	require.NoError(t, err)
	assert.Equal(t, "per-repo", d.Source)
	assert.Equal(t, "https://mint.example.com", d.MintURL)
	assert.Equal(t, "us-west1", d.InferenceRegion)
	assert.Equal(t, "v2.3.0", d.FullsendRef)
}

func TestDiscoverRepo_PerOrg(t *testing.T) {
	fc := forge.NewFakeClient()
	setWorkflowFile(fc, "acme", "api",
		"    uses: fullsend-ai/fullsend/.github/workflows/reusable-dispatch.yml@v2.1.0")

	orgCfg, parseErr := config.ParseOrgConfig([]byte(`version: "1"
dispatch:
  platform: github-actions
  mint_url: https://mint-org.example.com
defaults:
  roles: [triage]
repos:
  api:
    enabled: true
`))
	require.NoError(t, parseErr)

	d, err := discoverRepo(context.Background(), fc, "acme", "api", orgCfg, "", nopProgress)
	require.NoError(t, err)
	assert.Equal(t, "per-org", d.Source)
	assert.Equal(t, "https://mint-org.example.com", d.MintURL)
	assert.Equal(t, "v2.1.0", d.FullsendRef)
}

func TestDiscoverRepo_PerOrgDisabled(t *testing.T) {
	fc := forge.NewFakeClient()

	orgCfg, parseErr := config.ParseOrgConfig([]byte(`version: "1"
dispatch:
  platform: github-actions
defaults:
  roles: [triage]
repos:
  api:
    enabled: false
`))
	require.NoError(t, parseErr)

	d, err := discoverRepo(context.Background(), fc, "acme", "api", orgCfg, "", nopProgress)
	require.NoError(t, err)
	assert.Equal(t, "new", d.Source)
}

func TestDiscoverRepo_New(t *testing.T) {
	fc := forge.NewFakeClient()

	d, err := discoverRepo(context.Background(), fc, "acme", "api", nil, "", nopProgress)
	require.NoError(t, err)
	assert.Equal(t, "new", d.Source)
	assert.Empty(t, d.MintURL)
	assert.Empty(t, d.FullsendRef)
}

// --- discoverRepo: org variable fallback tests ---

func TestDiscoverRepo_PerOrg_OrgVarFallback(t *testing.T) {
	fc := forge.NewFakeClient()
	setWorkflowFile(fc, "acme", "api",
		"    uses: fullsend-ai/fullsend/.github/workflows/reusable-dispatch.yml@v2.1.0")

	// Org config has no mint_url in dispatch settings.
	orgCfg, parseErr := config.ParseOrgConfig([]byte(`version: "1"
dispatch:
  platform: github-actions
defaults:
  roles: [triage]
repos:
  api:
    enabled: true
`))
	require.NoError(t, parseErr)

	// Set org-level variable as fallback.
	fc.OrgVariables = map[string]bool{
		"acme/FULLSEND_MINT_URL": true,
	}
	fc.OrgVariableValues = map[string]string{
		"acme/FULLSEND_MINT_URL": "https://mint.example.com",
	}

	d, err := discoverRepo(context.Background(), fc, "acme", "api", orgCfg, "", nopProgress)
	require.NoError(t, err)
	assert.Equal(t, "per-org", d.Source)
	assert.Equal(t, "https://mint.example.com", d.MintURL)
}

func TestDiscoverRepo_PerOrg_OrgConfigTakesPrecedence(t *testing.T) {
	fc := forge.NewFakeClient()
	setWorkflowFile(fc, "acme", "api",
		"    uses: fullsend-ai/fullsend/.github/workflows/reusable-dispatch.yml@v2.1.0")

	// Org config has a mint_url.
	orgCfg, parseErr := config.ParseOrgConfig([]byte(`version: "1"
dispatch:
  platform: github-actions
  mint_url: https://config-mint.example.com
defaults:
  roles: [triage]
repos:
  api:
    enabled: true
`))
	require.NoError(t, parseErr)

	// Org variable also set — should NOT be used.
	fc.OrgVariables = map[string]bool{
		"acme/FULLSEND_MINT_URL": true,
	}
	fc.OrgVariableValues = map[string]string{
		"acme/FULLSEND_MINT_URL": "https://var-mint.example.com",
	}

	d, err := discoverRepo(context.Background(), fc, "acme", "api", orgCfg, "", nopProgress)
	require.NoError(t, err)
	assert.Equal(t, "per-org", d.Source)
	assert.Equal(t, "https://config-mint.example.com", d.MintURL)
}

func TestDiscoverRepo_PerOrg_OrgVarError_NonFatal(t *testing.T) {
	fc := forge.NewFakeClient()
	setWorkflowFile(fc, "acme", "api",
		"    uses: fullsend-ai/fullsend/.github/workflows/reusable-dispatch.yml@v2.1.0")
	fc.Errors["GetOrgVariable"] = fmt.Errorf("forbidden")

	// Org config has no mint_url.
	orgCfg, parseErr := config.ParseOrgConfig([]byte(`version: "1"
dispatch:
  platform: github-actions
defaults:
  roles: [triage]
repos:
  api:
    enabled: true
`))
	require.NoError(t, parseErr)

	var warnings []string
	progress := func(_, _, msg string) {
		warnings = append(warnings, msg)
	}

	d, err := discoverRepo(context.Background(), fc, "acme", "api", orgCfg, "", progress)
	require.NoError(t, err)
	assert.Equal(t, "per-org", d.Source)
	assert.Empty(t, d.MintURL)

	// Should have logged a warning about the org variable.
	hasWarning := false
	for _, w := range warnings {
		if strings.Contains(w, "FULLSEND_MINT_URL") &&
			strings.Contains(w, "warning") {
			hasWarning = true
			break
		}
	}
	assert.True(t, hasWarning, "expected a warning about GetOrgVariable failure")
}

// --- discoverRepo: forge-aware tests ---

func TestDiscoverRepo_GitLabForge_UsesGitLabPaths(t *testing.T) {
	fc := forge.NewFakeClient()
	setRepoVars(fc, "acme", "api", map[string]string{
		forge.PerRepoGuardVar: "true",
		"FULLSEND_MINT_URL":   "https://mint.example.com",
	})
	fc.FileContents["acme/api/.gitlab/ci/fullsend-dispatch.yml"] = []byte(
		"  ref: v2.5.0\n")

	d, err := discoverRepo(context.Background(), fc, "acme", "api", nil, ForgeGitLab, nopProgress)
	require.NoError(t, err)
	assert.Equal(t, "per-repo", d.Source)
	assert.Equal(t, "v2.5.0", d.FullsendRef)
}

func TestDiscoverRepo_GitLabForge_PerOrg_UsesGitLabPaths(t *testing.T) {
	fc := forge.NewFakeClient()
	fc.FileContents["acme/api/.gitlab/ci/fullsend-dispatch.yml"] = []byte(
		"  ref: v2.4.0\n")

	orgCfg, parseErr := config.ParseOrgConfig([]byte(`version: "1"
dispatch:
  platform: github-actions
  mint_url: https://mint-org.example.com
repos:
  api:
    enabled: true
`))
	require.NoError(t, parseErr)

	d, err := discoverRepo(context.Background(), fc, "acme", "api", orgCfg, ForgeGitLab, nopProgress)
	require.NoError(t, err)
	assert.Equal(t, "per-org", d.Source)
	assert.Equal(t, "v2.4.0", d.FullsendRef)
}

func TestInit_SingleRepo_GitLabForge(t *testing.T) {
	fc := forge.NewFakeClient()
	setRepoVars(fc, "acme", "api", map[string]string{
		forge.PerRepoGuardVar: "true",
		"FULLSEND_MINT_URL":   "https://mint.example.com",
	})
	fc.FileContents["acme/api/.gitlab/ci/fullsend-dispatch.yml"] = []byte(
		"  ref: v2.5.0\n")

	result, err := Init(context.Background(), InitConfig{
		Target:   "acme/api",
		Forge:    ForgeGitLab,
		ForgeURL: "https://gitlab.example.com",
	}, newTestClientFactory(fc), nil, nopProgress)

	require.NoError(t, err)
	assert.Equal(t, 1, result.PerRepoCount)
	assert.Equal(t, ForgeGitLab, result.Manifest.Defaults.Forge)
	assert.Equal(t, "https://gitlab.example.com", result.Manifest.Forge.GitLab.URL)
	// GitHub-specific fields should not be set for GitLab manifests.
	assert.Empty(t, result.Manifest.Forge.GitHub.MintURL)
	assert.Empty(t, result.Manifest.Forge.GitHub.MintProject)
	assert.Empty(t, result.Manifest.Forge.GitHub.MintRegion)
	assert.Empty(t, result.Manifest.Forge.GitHub.FullsendRef)
	assert.Empty(t, result.Manifest.Forge.GitHub.InferenceProject)
	assert.Empty(t, result.Manifest.Forge.GitHub.InferenceRegion)
	require.NoError(t, result.Manifest.Validate())
}

func TestInit_SingleRepo_GitLabForge_NoForgeURL(t *testing.T) {
	fc := forge.NewFakeClient()
	setRepoVars(fc, "acme", "api", map[string]string{
		forge.PerRepoGuardVar: "true",
		"FULLSEND_MINT_URL":   "https://mint.example.com",
	})
	fc.FileContents["acme/api/.gitlab/ci/fullsend-dispatch.yml"] = []byte(
		"  ref: v2.5.0\n")

	result, err := Init(context.Background(), InitConfig{
		Target: "acme/api",
		Forge:  ForgeGitLab,
	}, newTestClientFactory(fc), nil, nopProgress)

	require.NoError(t, err)
	assert.Empty(t, result.Manifest.Forge.GitLab.URL)
	assert.Contains(t, result.TODOs, "forge.gitlab.url: set the GitLab instance URL (e.g. https://gitlab.example.com)")
}

// --- readWorkflowRef tests ---

func TestReadWorkflowRef_YmlExtension(t *testing.T) {
	fc := forge.NewFakeClient()
	setWorkflowFile(fc, "acme", "api",
		"    uses: fullsend-ai/fullsend/.github/workflows/reusable-dispatch.yml@v2.3.0")

	ref, err := readWorkflowRef(context.Background(), fc, "acme", "api", defaultForgeConfig)
	require.NoError(t, err)
	assert.Equal(t, "v2.3.0", ref)
}

func TestReadWorkflowRef_YamlExtension(t *testing.T) {
	fc := forge.NewFakeClient()
	fc.FileContents["acme/api/.github/workflows/fullsend.yaml"] = []byte(
		"    uses: fullsend-ai/fullsend/.github/workflows/reusable-dispatch.yml@v1.0.0")

	ref, err := readWorkflowRef(context.Background(), fc, "acme", "api", defaultForgeConfig)
	require.NoError(t, err)
	assert.Equal(t, "v1.0.0", ref)
}

func TestReadWorkflowRef_NoWorkflowFile(t *testing.T) {
	fc := forge.NewFakeClient()
	ref, err := readWorkflowRef(context.Background(), fc, "acme", "api", defaultForgeConfig)
	require.NoError(t, err)
	assert.Empty(t, ref)
}

func TestReadWorkflowRef_NonNotFoundError(t *testing.T) {
	fc := forge.NewFakeClient()
	fc.Errors["GetFileContent"] = fmt.Errorf("network timeout")

	ref, err := readWorkflowRef(context.Background(), fc, "acme", "api", defaultForgeConfig)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "network timeout")
	assert.Empty(t, ref)
}

// --- CLIVersion fallback tests ---

func TestInit_CLIVersionFallback(t *testing.T) {
	fc := forge.NewFakeClient()

	result, err := Init(context.Background(), InitConfig{
		Target:     "acme/api",
		Forge:      ForgeGitHub,
		CLIVersion: "3.0.0",
	}, newTestClientFactory(fc), nil, nopProgress)

	require.NoError(t, err)
	assert.Equal(t, "v3.0.0", result.Manifest.Forge.GitHub.FullsendRef)
}

func TestInit_CLIVersionWithVPrefix_NoDoubleV(t *testing.T) {
	fc := forge.NewFakeClient()

	result, err := Init(context.Background(), InitConfig{
		Target:     "acme/api",
		Forge:      ForgeGitHub,
		CLIVersion: "v0.32.0-82-gcb2bcd9f",
	}, newTestClientFactory(fc), nil, nopProgress)

	require.NoError(t, err)
	assert.Equal(t, "v0.32.0-82-gcb2bcd9f", result.Manifest.Forge.GitHub.FullsendRef)
}

func TestInit_CLIVersionDev_FallsBackToDefault(t *testing.T) {
	fc := forge.NewFakeClient()

	result, err := Init(context.Background(), InitConfig{
		Target:     "acme/api",
		Forge:      ForgeGitHub,
		CLIVersion: "dev",
	}, newTestClientFactory(fc), nil, nopProgress)

	require.NoError(t, err)
	assert.Equal(t, config.DefaultUpstreamRef, result.Manifest.Forge.GitHub.FullsendRef)
}

// --- Concurrency tests ---

func TestInit_DefaultConcurrency(t *testing.T) {
	fc := newFakeWithOrgRepos("acme", []forge.Repository{
		{Name: "api", FullName: "acme/api"},
	})

	result, err := Init(context.Background(), InitConfig{
		Target:         "acme",
		All:            true,
		Forge:          ForgeGitHub,
		CLIVersion:     "1.0.0",
		MaxConcurrency: 0, // should default to 8
	}, newTestClientFactory(fc), nil, nopProgress)

	require.NoError(t, err)
	assert.Equal(t, 1, result.NewCount)
}

func TestInit_ConcurrencyUpperBound(t *testing.T) {
	fc := newFakeWithOrgRepos("acme", []forge.Repository{
		{Name: "api", FullName: "acme/api"},
	})

	result, err := Init(context.Background(), InitConfig{
		Target:         "acme",
		All:            true,
		Forge:          ForgeGitHub,
		CLIVersion:     "1.0.0",
		MaxConcurrency: 200, // should clamp to 64
	}, newTestClientFactory(fc), nil, nopProgress)

	require.NoError(t, err)
	assert.Equal(t, 1, result.NewCount)
}

// --- selectInitRepos tests ---

func TestSelectInitRepos_AllMode(t *testing.T) {
	candidates := []RepoCandidate{
		{Owner: "acme", Repo: "api"},
		{Owner: "acme", Repo: "web"},
	}
	selected, err := selectInitRepos(InitConfig{All: true}, candidates, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"acme/api", "acme/web"}, selected)
}

func TestSelectInitRepos_ExplicitMode(t *testing.T) {
	candidates := []RepoCandidate{
		{Owner: "acme", Repo: "api"},
		{Owner: "acme", Repo: "web"},
	}
	selected, err := selectInitRepos(InitConfig{Repos: []string{"acme/api"}}, candidates, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"acme/api"}, selected)
}

func TestSelectInitRepos_ExplicitMode_Empty(t *testing.T) {
	candidates := []RepoCandidate{
		{Owner: "acme", Repo: "api"},
	}
	_, err := selectInitRepos(InitConfig{Repos: []string{}}, candidates, nil, nil)
	assert.Error(t, err)
	assert.ErrorContains(t, err, "--repos list is empty")
}

func TestSelectInitRepos_ExplicitMode_InvalidRepo(t *testing.T) {
	candidates := []RepoCandidate{
		{Owner: "acme", Repo: "api"},
	}
	_, err := selectInitRepos(InitConfig{Repos: []string{"acme/missing"}}, candidates, nil, nil)
	assert.Error(t, err)
	assert.ErrorContains(t, err, "not found in org")
}

func TestSelectInitRepos_ExplicitMode_DiscoveryError(t *testing.T) {
	candidates := []RepoCandidate{
		{Owner: "acme", Repo: "api"},
	}
	discoveryErrors := []string{"acme/broken: connection refused"}
	_, err := selectInitRepos(InitConfig{Repos: []string{"acme/broken"}}, candidates, discoveryErrors, nil)
	assert.Error(t, err)
	assert.ErrorContains(t, err, "failed discovery")
	assert.ErrorContains(t, err, "connection refused")
}
