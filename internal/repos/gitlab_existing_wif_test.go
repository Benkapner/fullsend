package repos

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fullsend-ai/fullsend/internal/forge"
	"github.com/fullsend-ai/fullsend/internal/scaffold"
)

// Tests for the GitLab vertex-wif options that point inference at an
// existing WIF provider the installer does not own (#8810).

const (
	testExistingWIF      = "projects/123456789/locations/global/workloadIdentityPools/shared-pool/providers/gitlab-instance"
	testExistingAudience = "//iam.googleapis.com/" + testExistingWIF
	testServiceAccount   = "agent-sa@example-project.iam.gserviceaccount.com"
)

func gitlabExistingWIFManifest(t *testing.T, managed string) *Manifest {
	t.Helper()
	return &Manifest{
		Version:  1,
		Defaults: testInferenceDefaults(),
		GitLab: &PlatformConfig{
			URL:         "https://gitlab.example.com",
			FullsendRef: "v2.5.0",
			Config:      mustManagedConfig(t, managed),
			Repos:       []RepoEntry{{Name: "group/project"}},
		},
	}
}

func TestValidateWIFAudience(t *testing.T) {
	for _, ok := range []string{testExistingAudience, "https:" + testExistingAudience, "fullsend", "custom-audience"} {
		assert.NoError(t, validateWIFAudience(ok), ok)
	}
	for bad, want := range map[string]string{
		"":                           "non-empty",
		" padded":                    "surrounding spaces",
		"line\nbreak":                "single line",
		"tab\there":                  "single line",
		"café":                       "printable ASCII",
		"//iam.googleapis.com/$POOL": "must not contain",
	} {
		err := validateWIFAudience(bad)
		require.Error(t, err, "%q", bad)
		assert.Contains(t, err.Error(), want, "%q", bad)
	}
}

func TestValidateGCPServiceAccountEmail(t *testing.T) {
	for _, ok := range []string{testServiceAccount, "my-agent@my-project-123.iam.gserviceaccount.com", "agent-sa@project.example.com.iam.gserviceaccount.com"} {
		assert.NoError(t, validateGCPServiceAccountEmail(ok), ok)
	}
	for _, bad := range []string{
		"agent-sa",
		"agent-sa@example-project",
		"agent-sa@example-project.gserviceaccount.com",
		"Agent-SA@example-project.iam.gserviceaccount.com",
		"ab@example-project.iam.gserviceaccount.com",
		`agent-sa@example-project.iam.gserviceaccount.com"`,
		"agent-sa@example-project.iam.gserviceaccount.com\n",
	} {
		err := validateGCPServiceAccountEmail(bad)
		require.Error(t, err, "%q", bad)
		assert.Contains(t, err.Error(), "full service account email")
	}
}

func TestApplyEffectiveInfrastructure_GitLabExistingWIFOptions(t *testing.T) {
	m := gitlabExistingWIFManifest(t, "inference:\n  project: example-project\n  wif_provider: "+testExistingWIF+
		"\n  wif_audience: \""+testExistingAudience+"\"\n  service_account: "+testServiceAccount+"\n")
	rc, err := resolvedFor(t, m, "group", "project", "")
	require.NoError(t, err)
	assert.Equal(t, testExistingAudience, rc.DeclaredWIFAudience)
	assert.Equal(t, testServiceAccount, rc.DeclaredServiceAccount)

	project, wif, err := vertexCredentialInputs(ConvergeConfig{}, rc)
	require.NoError(t, err)
	assert.Equal(t, "example-project", project)
	assert.Equal(t, testExistingWIF, wif)
}

func TestApplyEffectiveInfrastructure_ExistingWIFOptionsFromPreset(t *testing.T) {
	m := gitlabExistingWIFManifest(t, "inference:\n  region: us-east5\n")
	preset := "version: \"1\"\ninference:\n  project: example-project\n  wif_provider: " + testExistingWIF +
		"\n  wif_audience: custom-audience\n  service_account: " + testServiceAccount + "\n"
	rc, err := resolvedFor(t, m, "group", "project", preset)
	require.NoError(t, err)
	assert.Equal(t, "custom-audience", rc.DeclaredWIFAudience)
	assert.Equal(t, testServiceAccount, rc.DeclaredServiceAccount)

	// A preset value bypasses managed-layer validation, so the format is
	// checked during resolution too.
	bad := strings.Replace(preset, testServiceAccount, "agent-sa@example-project", 1)
	_, err = resolvedFor(t, m, "group", "project", bad)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "full service account email")
}

func TestApplyEffectiveInfrastructure_ExistingWIFOptionsInertOutsideGitLabVertexWIF(t *testing.T) {
	const options = "inference:\n  wif_audience: custom-audience\n  service_account: " + testServiceAccount + "\n"
	t.Run("GitHub inherits from defaults.config", func(t *testing.T) {
		m := newConvergeManifest("acme/api")
		m.Defaults.Config = mustManagedConfig(t, options)
		require.NoError(t, m.Validate())
		rc, err := resolvedFor(t, m, "acme", "api", "")
		require.NoError(t, err)
		assert.Empty(t, rc.DeclaredWIFAudience)
		assert.Empty(t, rc.DeclaredServiceAccount)
		_, _, err = vertexCredentialInputs(ConvergeConfig{}, rc)
		assert.NoError(t, err, "no explicit-provider requirement where the options are inert")
	})
	for _, auth := range []string{InferenceAuthOpenAIAPIKey, InferenceAuthOpenAIWIF} {
		t.Run(auth, func(t *testing.T) {
			m := gitlabExistingWIFManifest(t, options)
			m.Defaults.Inference.Auth = auth
			rc, err := resolvedFor(t, m, "group", "project", "")
			require.NoError(t, err)
			assert.Empty(t, rc.DeclaredWIFAudience)
			assert.Empty(t, rc.DeclaredServiceAccount)
		})
	}
	t.Run("format is still checked", func(t *testing.T) {
		m := newConvergeManifest("acme/api")
		m.Defaults.Config = mustManagedConfig(t, "inference:\n  service_account: agent-sa\n")
		require.ErrorContains(t, m.Validate(), "full service account email")
		_, err := resolvedFor(t, m, "acme", "api", "")
		require.ErrorContains(t, err, "full service account email")
	})
}

func TestManifestValidate_ExistingWIFOptionsRejectedInGitHubLayers(t *testing.T) {
	for _, key := range []string{"wif_audience: custom-audience", "service_account: " + testServiceAccount} {
		raw := "inference:\n  " + key + "\n"
		name := strings.SplitN(key, ":", 2)[0]

		m := newConvergeManifest("acme/api")
		m.GitHub.Config = mustManagedConfig(t, raw)
		err := m.Validate()
		require.Error(t, err, key)
		assert.Contains(t, err.Error(), "github.config.inference."+name+" applies only to GitLab repositories")
		assert.Contains(t, err.Error(), "gitlab.config")

		m = newConvergeManifest("acme/api")
		m.GitHub.Repos[0].Config = mustManagedConfig(t, raw)
		err = m.Validate()
		require.Error(t, err, key)
		assert.Contains(t, err.Error(), "github.repos[acme/api].config.inference."+name)
	}

	// The same keys are accepted on the GitLab side.
	m := gitlabExistingWIFManifest(t, "inference:\n  project: example-project\n  wif_provider: "+testExistingWIF+
		"\n  wif_audience: custom-audience\n  service_account: "+testServiceAccount+"\n")
	m.GitLab.Repos[0].Config = mustManagedConfig(t, "inference:\n  service_account: "+testServiceAccount+"\n")
	require.NoError(t, m.Validate())
}

// A GitLab repository whose inference.auth is not vertex-wif installs
// without the variable or a rendered audience even when the options are
// inherited.
func TestConverge_GitLabExistingWIFOptionsInertForOpenAI(t *testing.T) {
	fc := newFakeClientForBatch("group/project")
	m := gitlabExistingWIFManifest(t, "inference:\n  wif_audience: custom-audience\n  service_account: "+testServiceAccount+"\n")
	m.Defaults.Inference.Auth = InferenceAuthOpenAIAPIKey
	spy := &spyScaffoldCommit{}
	result, err := Converge(context.Background(), ConvergeConfig{Manifest: m, MaxConcurrency: 4, Direct: true, OpenAIAPIKey: "sk-test-key-0123456789"},
		newTestClientFactory(fc), spy.fn(), noopProgress)
	require.NoError(t, err)
	require.Empty(t, result.Failed(), "results: %+v", result.Results)
	_, written := fc.VariableValues["group/project/"+forge.VarGCPServiceAccount]
	assert.False(t, written)
	assert.Contains(t, gitlabAgentTemplate(t, spy.files), "\n      aud: \"fullsend\"\n")
}

func TestVertexCredentialInputs_ExistingWIFOptionsRequireExplicitProvider(t *testing.T) {
	base := ResolvedConfig{
		Owner: "group", Repo: "project", Forge: ForgeGitLab, InferenceAuth: InferenceAuthVertexWIF,
		DeclaredInferenceProject: "example-project",
	}
	for name, rc := range map[string]ResolvedConfig{
		"audience":        func() ResolvedConfig { r := base; r.DeclaredWIFAudience = testExistingAudience; return r }(),
		"service account": func() ResolvedConfig { r := base; r.DeclaredServiceAccount = testServiceAccount; return r }(),
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := vertexCredentialInputs(ConvergeConfig{}, rc)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "require an explicitly supplied WIF provider")
			assert.Contains(t, err.Error(), "gitlab-oidc")

			// --vertex-wif-provider satisfies the requirement.
			_, wif, err := vertexCredentialInputs(ConvergeConfig{WIFProvider: testExistingWIF}, rc)
			require.NoError(t, err)
			assert.Equal(t, testExistingWIF, wif)
		})
	}

	// Without the options the derived provider stays allowed.
	_, wif, err := vertexCredentialInputs(ConvergeConfig{}, base)
	require.NoError(t, err)
	assert.Empty(t, wif)
}

func TestConverge_GitLabExistingWIFOptionsWithDerivedProviderFails(t *testing.T) {
	fc := newFakeClientForBatch("group/project")
	m := gitlabExistingWIFManifest(t, "inference:\n  project: example-project\n  service_account: "+testServiceAccount+"\n")
	cfg := ConvergeConfig{
		Manifest:             m,
		MaxConcurrency:       4,
		Direct:               true,
		ResolveProjectNumber: func(context.Context, string) (string, error) { return "777", nil },
	}
	result, err := Converge(context.Background(), cfg, newTestClientFactory(fc), (&fakeScaffoldCommit{}).fn(), noopProgress)
	require.NoError(t, err)
	require.Len(t, result.Failed(), 1)
	assert.Contains(t, result.Failed()[0].Error.Error(), "derived per-project gitlab-oidc provider")
	assert.Empty(t, fc.CreatedSecrets, "nothing is written")
	assert.Empty(t, fc.Variables, "nothing is written")
}

func TestValidateManagedMintAndWIF_ExistingWIFOptionFormats(t *testing.T) {
	for raw, want := range map[string]string{
		"inference:\n  service_account: agent-sa\n":         "full service account email",
		"inference:\n  wif_audience: \"aud-$CI_PROJECT\"\n": "must not contain",
	} {
		err := validateManagedBlock("gitlab.config", mustManagedConfig(t, raw))
		require.Error(t, err, raw)
		assert.Contains(t, err.Error(), want)
	}
	require.NoError(t, validateManagedBlock("gitlab.config", mustManagedConfig(t,
		"inference:\n  wif_audience: custom-audience\n  service_account: "+testServiceAccount+"\n")))
}

func gitlabAgentTemplate(t *testing.T, files []forge.TreeFile) string {
	t.Helper()
	for _, f := range files {
		if f.Path == fullsendAgentTemplatePath {
			return string(f.Content)
		}
	}
	t.Fatalf("%s not among the files", fullsendAgentTemplatePath)
	return ""
}

func TestBuildScaffoldFiles_GitLabAgentAudience(t *testing.T) {
	cfg := InstallConfig{Owner: "group", Repo: "project", Forge: ForgeGitLab, Roles: []string{"triage"}}
	defaultFiles, err := BuildScaffoldFiles(cfg)
	require.NoError(t, err)
	shipped, err := scaffold.GitLabPerRepoFile(fullsendAgentTemplatePath)
	require.NoError(t, err)
	wantDefault := strings.ReplaceAll(string(shipped), "__AGENT_RUNNER_TAGS__", scaffold.FormatRunnerTags(nil))
	assert.Equal(t, wantDefault, gitlabAgentTemplate(t, defaultFiles), "an unset audience keeps the shipped template byte for byte")

	cfg.InferenceWIFAudience = testExistingAudience
	customFiles, err := BuildScaffoldFiles(cfg)
	require.NoError(t, err)
	custom := gitlabAgentTemplate(t, customFiles)
	assert.Contains(t, custom, "aud: \""+testExistingAudience+"\"")
	assert.NotContains(t, custom, `aud: "fullsend"`)

	// Pinned (prebuilt) templates are rendered too.
	cfg.PrebuiltScaffoldFiles = scaffold.InstallFiles{{Path: fullsendAgentTemplatePath, Content: []byte(wantDefault), Mode: "100644"}}
	prebuilt, err := BuildScaffoldFiles(cfg)
	require.NoError(t, err)
	assert.Equal(t, custom, gitlabAgentTemplate(t, prebuilt))

	// A template without the shipped audience line cannot be rendered.
	cfg.PrebuiltScaffoldFiles = scaffold.InstallFiles{{Path: fullsendAgentTemplatePath, Content: []byte("job: {}\n"), Mode: "100644"}}
	_, err = BuildScaffoldFiles(cfg)
	require.ErrorContains(t, err, "rendering GitLab agent token audience")
}

func TestManagedVarsForForge_GitLabServiceAccount(t *testing.T) {
	vars, err := managedVarsForForge(InstallConfig{Forge: ForgeGitLab, InferenceServiceAccount: testServiceAccount}, "")
	require.NoError(t, err)
	assert.Contains(t, vars, ManagedVar{Name: forge.VarGCPServiceAccount, Value: testServiceAccount})

	vars, err = managedVarsForForge(InstallConfig{Forge: ForgeGitLab}, "")
	require.NoError(t, err)
	for _, v := range vars {
		assert.NotEqual(t, forge.VarGCPServiceAccount, v.Name, "no variable without a configured service account")
	}
}

func TestInstall_RejectsInvalidExistingWIFOptions(t *testing.T) {
	_, err := Install(context.Background(), InstallConfig{
		Owner: "group", Repo: "project", Forge: ForgeGitLab, InferenceServiceAccount: "agent-sa",
	}, forge.NewFakeClient(), (&fakeScaffoldCommit{}).fn(), nil)
	require.ErrorContains(t, err, "full service account email")
}

func TestConverge_GitLabExistingWIFProviderInstall(t *testing.T) {
	fc := newFakeClientForBatch("group/project")
	m := gitlabExistingWIFManifest(t, "inference:\n  project: example-project\n  wif_provider: "+testExistingWIF+
		"\n  wif_audience: \""+testExistingAudience+"\"\n  service_account: "+testServiceAccount+"\n")
	cfg := ConvergeConfig{Manifest: m, MaxConcurrency: 4, Direct: true}
	spy := &spyScaffoldCommit{}

	result, err := Converge(context.Background(), cfg, newTestClientFactory(fc), spy.fn(), noopProgress)
	require.NoError(t, err)
	require.Empty(t, result.Failed())

	wif, ok := createdSecretValue(fc, "group/project", forge.SecretGCPWIFProvider)
	require.True(t, ok)
	assert.Equal(t, testExistingWIF, wif, "the explicit provider is used verbatim")
	assert.Equal(t, testServiceAccount, fc.VariableValues["group/project/"+forge.VarGCPServiceAccount])
	agent := gitlabAgentTemplate(t, spy.files)
	assert.Contains(t, agent, "aud: \""+testExistingAudience+"\"")
	assert.NotContains(t, agent, `aud: "fullsend"`)
}

func TestConverge_GitLabDefaultInstallKeepsDefaultAudience(t *testing.T) {
	fc := newFakeClientForBatch("group/project")
	m := gitlabExistingWIFManifest(t, "inference:\n  project: example-project\n  wif_provider: "+testExistingWIF+"\n")
	spy := &spyScaffoldCommit{}

	result, err := Converge(context.Background(), ConvergeConfig{Manifest: m, MaxConcurrency: 4, Direct: true},
		newTestClientFactory(fc), spy.fn(), noopProgress)
	require.NoError(t, err)
	require.Empty(t, result.Failed())
	assert.Contains(t, gitlabAgentTemplate(t, spy.files), "\n      aud: \"fullsend\"\n")
	_, written := fc.VariableValues["group/project/"+forge.VarGCPServiceAccount]
	assert.False(t, written, "no service account variable without the option")
}

func TestProbeComponents_GitLabServiceAccountDrift(t *testing.T) {
	expected := map[string]string{forge.VarGCPServiceAccount: testServiceAccount}
	component := func(fc *forge.FakeClient) ComponentStatus {
		t.Helper()
		components, err := ProbeComponents(context.Background(), fc, "group", "project", ForgeGitLab, GitLabForgeConfig(), expected)
		require.NoError(t, err)
		for _, c := range components {
			if c.Name == "var:"+forge.VarGCPServiceAccount {
				return c
			}
		}
		t.Fatalf("no %s component", forge.VarGCPServiceAccount)
		return ComponentStatus{}
	}

	missing := component(forge.NewFakeClient())
	assert.False(t, missing.Present)
	assert.False(t, missing.Match, "a configured but missing variable is drift")
	assert.Equal(t, testServiceAccount, missing.Expected)

	fc := forge.NewFakeClient()
	fc.VariableValues["group/project/"+forge.VarGCPServiceAccount] = "other-sa@example-project.iam.gserviceaccount.com"
	drifted := component(fc)
	assert.True(t, drifted.Present)
	assert.False(t, drifted.Match)

	fc.VariableValues["group/project/"+forge.VarGCPServiceAccount] = testServiceAccount
	assert.True(t, component(fc).Match)
}

func TestCheckOrphanVars_GitLabServiceAccountNotFlagged(t *testing.T) {
	fc := forge.NewFakeClient()
	fc.VariableValues["group/project/"+forge.VarGCPServiceAccount] = testServiceAccount
	orphans, err := CheckOrphanVars(context.Background(), fc, "group", "project", InstallConfig{Forge: ForgeGitLab}, "")
	require.NoError(t, err)
	for _, o := range orphans {
		assert.NotEqual(t, forge.VarGCPServiceAccount, o.Name)
	}
}

func TestUninstall_GitLabRemovesServiceAccountVariable(t *testing.T) {
	assert.True(t, slices.Contains(gitlabUninstallVars, forge.VarGCPServiceAccount))
	client := newInstalledFakeGitLabClient("group/project")
	results, err := Uninstall(context.Background(), UninstallConfig{
		Manifest:       testGitLabManifest("group/project"),
		Repos:          []string{"group/project"},
		Direct:         true,
		MaxConcurrency: 4,
	}, newTestClientFactory(client), uninstallCommitFn(client), nil)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.True(t, results[0].Success, "error: %v", results[0].Error)
	deleted := false
	for _, v := range client.DeletedVariables {
		if v.Name == forge.VarGCPServiceAccount {
			deleted = true
		}
	}
	assert.True(t, deleted, "uninstall deletes %s", forge.VarGCPServiceAccount)
}

func TestCheckGitLabTriggerDispatchGate_AcceptsRenderedAgentAudience(t *testing.T) {
	e := readyTriggerDispatchEnv(t)
	key := trigOwner + "/" + trigRepo + "/" + fullsendAgentTemplatePath
	shipped := e.state.FileContents[key]

	rendered, err := scaffold.RenderGitLabAgentAudience(shipped, testExistingAudience)
	require.NoError(t, err)
	e.state.FileContents[key] = rendered
	gate := e.dispatchGate()
	assert.True(t, gate.Ready, "deferred: %v", gate.Deferred)

	// An audience that would fail inference.wif_audience validation, such as
	// a GitLab variable expansion, is not accepted.
	e.state.FileContents[key] = []byte(strings.Replace(string(shipped), `aud: "fullsend"`, `aud: "$CI_PROJECT_URL"`, 1))
	gate = e.dispatchGate()
	assert.False(t, gate.Ready)
}

func TestGitLabCommittedAgentAudience(t *testing.T) {
	shipped, err := scaffold.GitLabPerRepoFile(fullsendAgentTemplatePath)
	require.NoError(t, err)
	aud, ok := gitlabCommittedAgentAudience(shipped, fullsendAgentJobName)
	require.True(t, ok)
	assert.Equal(t, "fullsend", aud)

	_, ok = gitlabCommittedAgentAudience([]byte("---\nother: {}\n"), fullsendAgentJobName)
	assert.False(t, ok, "missing job")
	_, ok = gitlabCommittedAgentAudience([]byte("---\n\"fullsend $[[ inputs.stage | expand_vars ]] agent\":\n  script: [true]\n"), fullsendAgentJobName)
	assert.False(t, ok, "missing id_tokens")
}

// installedGitLabExistingWIF is an installed GitLab repository whose
// committed scaffold matches the default install, with its Vertex secrets
// present, under a manifest that declares the existing-provider options.
func installedGitLabExistingWIF(t *testing.T) (*forge.FakeClient, *Manifest) {
	t.Helper()
	fc := newFakeClientForBatch("group/project")
	populateGitLabInstalled(fc, "group", "project")
	populateGitLabTypedWrapper(t, fc, "group", "project")
	fc.Secrets["group/project/"+forge.SecretGCPProjectID] = true
	fc.Secrets["group/project/"+forge.SecretGCPWIFProvider] = true
	m := gitlabExistingWIFManifest(t, "inference:\n  project: example-project\n  wif_provider: "+testExistingWIF+
		"\n  wif_audience: \""+testExistingAudience+"\"\n  service_account: "+testServiceAccount+"\n")
	m.GitLab.Repos[0].Name = "group/project"
	return fc, m
}

func TestStatus_GitLabExistingWIFDrift(t *testing.T) {
	fc, m := installedGitLabExistingWIF(t)

	status, err := Status(context.Background(), m, newTestClientFactory(fc), 4, nil)
	require.NoError(t, err)
	require.Len(t, status.Repos, 1)
	require.Empty(t, status.Repos[0].Error)
	fields := driftFields(status.Repos[0].Drifts)
	assert.Contains(t, fields, forge.VarGCPServiceAccount, "a configured but missing variable is drift")
	assert.Contains(t, fields, fullsendAgentTemplatePath, "an agent job still requesting aud fullsend is drift")

	// A different service account is value drift.
	fc.VariableValues["group/project/"+forge.VarGCPServiceAccount] = "other-sa@example-project.iam.gserviceaccount.com"
	status, err = Status(context.Background(), m, newTestClientFactory(fc), 4, nil)
	require.NoError(t, err)
	assert.Contains(t, driftFields(status.Repos[0].Drifts), forge.VarGCPServiceAccount)

	// The configured variable and rendered audience clear both drifts.
	fc.VariableValues["group/project/"+forge.VarGCPServiceAccount] = testServiceAccount
	key := "group/project/" + fullsendAgentTemplatePath
	rendered, err := scaffold.RenderGitLabAgentAudience(fc.FileContents[key], testExistingAudience)
	require.NoError(t, err)
	fc.FileContents[key] = rendered
	status, err = Status(context.Background(), m, newTestClientFactory(fc), 4, nil)
	require.NoError(t, err)
	fields = driftFields(status.Repos[0].Drifts)
	assert.NotContains(t, fields, forge.VarGCPServiceAccount)
	assert.NotContains(t, fields, fullsendAgentTemplatePath)
}

// An already-installed repository converges to the configured options: the
// service account variable is added and the agent template is rendered.
func TestConverge_GitLabExistingWIFOptionsOnInstalledRepo(t *testing.T) {
	fc, m := installedGitLabExistingWIF(t)
	spy := &spyScaffoldCommit{}

	result, err := Converge(context.Background(), ConvergeConfig{Manifest: m, MaxConcurrency: 4, Direct: true},
		newTestClientFactory(fc), spy.fn(), noopProgress)
	require.NoError(t, err)
	require.Empty(t, result.Failed(), "results: %+v", result.Results)
	assert.Equal(t, testServiceAccount, fc.VariableValues["group/project/"+forge.VarGCPServiceAccount])
	assert.Contains(t, gitlabAgentTemplate(t, spy.files), "aud: \""+testExistingAudience+"\"")
}
