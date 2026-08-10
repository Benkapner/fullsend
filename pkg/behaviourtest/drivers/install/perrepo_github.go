package install

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/fullsend-ai/fullsend/internal/config"
	"github.com/fullsend-ai/fullsend/internal/forge"
	"github.com/fullsend-ai/fullsend/internal/layers"
	"github.com/fullsend-ai/fullsend/internal/scaffold"
	"github.com/fullsend-ai/fullsend/pkg/e2etest"
)

const (
	perRepoTestRepo = "test-repo"
	// Vendored per-repo: issue_comment triggers the fullsend.yaml shim, which
	// workflow_calls reusable-dispatch → reusable-triage synchronously.
	perRepoTriageWorkflow = "fullsend.yaml"
	perRepoAgentWorkflow  = "reusable-triage.yml"
	perRepoAgentArtifact  = "fullsend-triage"
)

// perRepoDriver installs fullsend in per-repo mode via fullsend github setup.
// When cfMint is enabled (PEMDir non-empty), Install deploys a temporary
// Cloudflare Worker preview mint and uses the derived preview URL as the
// mint endpoint for github setup. Teardown abandons the preview alias.
type perRepoDriver struct {
	e2eCfg       e2etest.EnvConfig
	client       forge.Client
	token        string
	binary       string
	logf         func(string, ...any)
	cfMint       cfMintConfig
	previewAlias string // set during Install when cfMint is enabled
}

type perRepoState struct {
	org     string
	repo    string
	mintURL string // effective mint URL (CF preview or configured MintURL)
}

func (s *perRepoState) Mode() string               { return "per-repo" }
func (s *perRepoState) TestRepo() string           { return s.repo }
func (s *perRepoState) ConfigOwner() string        { return s.org }
func (s *perRepoState) ConfigRepo() string         { return s.repo }
func (s *perRepoState) ConfigPathPrefix() string   { return ".fullsend" }
func (s *perRepoState) TriageWorkflowRepo() string { return s.repo }
func (s *perRepoState) TriageWorkflowFile() string { return perRepoTriageWorkflow }
func (s *perRepoState) AgentWorkflowFile() string  { return perRepoAgentWorkflow }
func (s *perRepoState) AgentArtifactName() string  { return perRepoAgentArtifact }

// MintURL returns the effective mint URL used during install. When a CF
// preview mint was deployed, this is the derived preview URL. Otherwise
// it is the configured MintURL from EnvConfig.
func (s *perRepoState) MintURL() string { return s.mintURL }

// Compile-time check that perRepoState implements MintURLProvider.
var _ MintURLProvider = (*perRepoState)(nil)

func newPerRepoDriver(e2eCfg e2etest.EnvConfig, client forge.Client, token, binary string, logf func(string, ...any)) Driver {
	return &perRepoDriver{
		e2eCfg: e2eCfg,
		client: client,
		token:  token,
		binary: binary,
		logf:   logf,
		cfMint: newCFMintConfig(e2eCfg),
	}
}

func (d *perRepoDriver) Install(ctx context.Context, org string) (State, error) {
	repo := perRepoTestRepo
	target := org + "/" + repo

	// Determine the effective mint URL. When CF mint is configured,
	// deploy a temporary preview mint and use its URL.
	mintURL := d.e2eCfg.MintURL
	if d.cfMint.enabled() {
		alias, err := generatePreviewAlias()
		if err != nil {
			return nil, err
		}
		d.previewAlias = alias

		url, err := deployCFMint(d.binary, d.token, d.cfMint, alias, org, e2etest.TryRunCLI, d.logf)
		if err != nil {
			return nil, fmt.Errorf("deploying CF preview mint for BT: %w", err)
		}
		mintURL = url
	}

	args := []string{
		"github", "setup", target,
		"--vendor", "--direct",
		"--skip-app-setup",
		"--mint-url", mintURL,
		"--runtime", "dummy",
	}
	if project := strings.TrimSpace(d.e2eCfg.GCPProjectID); project != "" {
		wifProvider, err := d.provisionPerRepoInference(target, project)
		if err != nil {
			return nil, err
		}
		args = append(args, "--inference-project", project, "--inference-wif-provider", wifProvider)
	}

	d.logf("[install] running fullsend %s", strings.Join(args, " "))
	if _, err := e2etest.TryRunCLI(d.binary, d.token, args...); err != nil {
		return nil, fmt.Errorf("github setup %s: %w", target, err)
	}

	st := &perRepoState{org: org, repo: repo, mintURL: mintURL}
	if err := validatePerRepoPostInstall(ctx, d.client, org, repo); err != nil {
		return nil, err
	}
	return st, nil
}

// provisionPerRepoInference creates repo-scoped inference WIF for target and
// returns the provider resource name for github setup. Idempotent.
func (d *perRepoDriver) provisionPerRepoInference(target, project string) (string, error) {
	provisionArgs := []string{"inference", "provision", target, "--project", project}
	d.logf("[install] running fullsend %s", strings.Join(provisionArgs, " "))
	if _, err := e2etest.TryRunCLI(d.binary, d.token, provisionArgs...); err != nil {
		return "", fmt.Errorf("inference provision %s: %w", target, err)
	}

	statusArgs := []string{"inference", "status", target, "--project", project, "--format", "json"}
	d.logf("[install] running fullsend %s", strings.Join(statusArgs, " "))
	out, err := e2etest.TryRunCLI(d.binary, d.token, statusArgs...)
	if err != nil {
		return "", fmt.Errorf("inference status %s: %w", target, err)
	}

	wifProvider, err := parseInferenceStatusWIFProvider(out)
	if err != nil {
		return "", fmt.Errorf("inference status %s: %w", target, err)
	}
	d.logf("[install] repo-scoped inference WIF provider: %s", wifProvider)
	return wifProvider, nil
}

func parseInferenceStatusWIFProvider(output string) (string, error) {
	statusKey := `"status":`
	keyIdx := strings.Index(output, statusKey)
	if keyIdx < 0 {
		return "", fmt.Errorf("no JSON status object in output")
	}
	start := strings.LastIndex(output[:keyIdx], "{")
	if start < 0 {
		return "", fmt.Errorf("no JSON status object in output")
	}
	var status struct {
		Status      string `json:"status"`
		WIFProvider string `json:"FULLSEND_GCP_WIF_PROVIDER"`
	}
	if err := json.NewDecoder(strings.NewReader(output[start:])).Decode(&status); err != nil {
		return "", fmt.Errorf("parse JSON: %w", err)
	}
	if status.WIFProvider == "" {
		return "", fmt.Errorf("missing FULLSEND_GCP_WIF_PROVIDER (status=%q)", status.Status)
	}
	if status.Status != "healthy" {
		return "", fmt.Errorf("expected healthy status, got %q", status.Status)
	}
	return status.WIFProvider, nil
}

func (d *perRepoDriver) Teardown(ctx context.Context, org string, state State) error {
	repo := state.TestRepo()
	d.logf("[install] tearing down per-repo install on %s/%s", org, repo)
	e2etest.TeardownPerRepoInstall(ctx, d.client, d.token, org, repo, d.logf)

	// Tear down the CF preview mint if one was deployed during Install.
	if d.cfMint.enabled() && d.previewAlias != "" {
		if err := teardownCFMint(d.binary, d.token, d.cfMint, d.previewAlias, e2etest.TryRunCLI, d.logf); err != nil {
			// Log but don't fail — the preview is ephemeral and will
			// expire. A teardown failure should not mask test results.
			d.logf("[install] CF preview mint teardown failed: %v", err)
		}
	}

	return nil
}

func validatePerRepoPostInstall(ctx context.Context, client forge.Client, org, repo string) error {
	shimPath := ".github/workflows/fullsend.yaml"
	if _, err := client.GetFileContent(ctx, org, repo, shimPath); err != nil {
		return fmt.Errorf("post-install: missing %s on %s/%s: %w", shimPath, org, repo, err)
	}

	cfgPath := filepath.Join(".fullsend", "config.yaml")
	cfgData, err := client.GetFileContent(ctx, org, repo, cfgPath)
	if err != nil {
		return fmt.Errorf("post-install: reading %s: %w", cfgPath, err)
	}
	cfgW, err := config.ParsePerRepoConfigWriter(cfgData)
	if err != nil {
		return fmt.Errorf("post-install: parsing %s: %w", cfgPath, err)
	}
	if err := cfgW.Validate(); err != nil {
		return fmt.Errorf("post-install: invalid %s: %w", cfgPath, err)
	}
	cfg := cfgW.(config.PerRepoConfigReader)
	if cfg.ConfigRuntime() != "dummy" {
		return fmt.Errorf("post-install: %s runtime is %q, want dummy", cfgPath, cfg.ConfigRuntime())
	}

	markerPath := scaffold.VendoredMarkerPath()
	if _, err := client.GetFileContent(ctx, org, repo, markerPath); err != nil {
		return fmt.Errorf("post-install: missing vendored marker %s: %w", markerPath, err)
	}
	if _, err := client.GetFileContent(ctx, org, repo, layers.VendoredBinaryPathPerRepo); err != nil {
		return fmt.Errorf("post-install: missing vendored binary at %s: %w", layers.VendoredBinaryPathPerRepo, err)
	}
	return nil
}
