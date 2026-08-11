// Package cfmint implements an install.Driver that deploys a temporary
// Cloudflare Worker preview mint for behaviour tests. The preview mint is
// self-contained: all configuration (PEMs, allowlists, provenance) is
// passed at deploy time. Teardown abandons the preview alias.
package cfmint

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"strings"

	"github.com/fullsend-ai/fullsend/internal/forge"
	"github.com/fullsend-ai/fullsend/pkg/behaviourtest/drivers/install"
	"github.com/fullsend-ai/fullsend/pkg/behaviourtest/drivers/install/common"
	"github.com/fullsend-ai/fullsend/pkg/e2etest"
)

// Config holds parameters for the CF mint driver. The caller provides
// these from test-infra data; the driver does not hardcode org/repo/pool
// assumptions.
type Config struct {
	// PEMDir is the directory containing {role}.pem files materialized
	// from TEST_*_PEM env vars. Required; the driver fails early if
	// empty or if the directory contains no .pem files.
	PEMDir string

	// SuiteName is used to derive the worker name. For example,
	// suite "bt" produces worker "bt-mint". Different suites get
	// different workers to avoid collisions.
	SuiteName string

	// AllowedOrgs is a comma-separated list of allowed GitHub orgs.
	// Passed to --allowed-orgs on deploy.
	AllowedOrgs string

	// PerRepoWIFRepos is a comma-separated list of repos for per-repo
	// WIF. Passed to --per-repo-wif-repos on deploy.
	PerRepoWIFRepos string
}

// driver deploys a CF Worker preview mint and uses the derived preview
// URL as the mint endpoint for fullsend github setup.
type driver struct {
	client       forge.Client
	token        string
	binary       string
	gcpProjectID string
	logf         func(string, ...any)
	cfg          Config
	workerName   string
	previewAlias string // set during Install
}

// NewDriver creates a CF mint install driver. Returns an error if the
// configuration is invalid (missing PEMs, empty suite name).
func NewDriver(
	client forge.Client,
	token, binary, gcpProjectID string,
	logf func(string, ...any),
	cfg Config,
) (install.Driver, error) {
	if cfg.PEMDir == "" {
		return nil, fmt.Errorf("cfmint: PEMDir is required (no PEMs materialized)")
	}
	entries, err := os.ReadDir(cfg.PEMDir)
	if err != nil {
		return nil, fmt.Errorf("cfmint: reading PEM dir: %w", err)
	}
	hasPEM := false
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".pem") {
			hasPEM = true
			break
		}
	}
	if !hasPEM {
		return nil, fmt.Errorf("cfmint: PEM dir %s contains no .pem files", cfg.PEMDir)
	}
	if cfg.SuiteName == "" {
		return nil, fmt.Errorf("cfmint: SuiteName is required")
	}

	return &driver{
		client:       client,
		token:        token,
		binary:       binary,
		gcpProjectID: gcpProjectID,
		logf:         logf,
		cfg:          cfg,
		workerName:   WorkerName(cfg.SuiteName),
	}, nil
}

// WorkerName derives the CF Worker script name from a suite name.
// The name clearly indicates it is the e2e/BT worker for that suite.
func WorkerName(suiteName string) string {
	return suiteName + "-mint"
}

// PreviewMintURL returns the deterministic preview mint URL for the given
// preview alias and worker name. Format: https://<alias>-<worker>.workers.dev
func PreviewMintURL(alias, workerName string) string {
	return fmt.Sprintf("https://%s-%s.workers.dev", alias, workerName)
}

// GeneratePreviewAlias creates a unique preview alias for a BT run.
// Format: bt-<8-hex-chars> (e.g., bt-a1b2c3d4). The alias satisfies
// the CF preview alias validation: 2-63 lowercase alphanumeric or hyphens.
func GeneratePreviewAlias() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating preview alias: %w", err)
	}
	return fmt.Sprintf("bt-%x", b), nil
}

func (d *driver) Install(ctx context.Context, org string) (install.State, error) {
	repo := install.PerRepoTestRepo
	target := org + "/" + repo

	alias, err := GeneratePreviewAlias()
	if err != nil {
		return nil, err
	}
	d.previewAlias = alias

	mintURL, err := d.deployCFMint(alias, org)
	if err != nil {
		return nil, fmt.Errorf("deploying CF preview mint for BT: %w", err)
	}

	// Run github setup with the preview mint URL.
	if err := common.RunGitHubSetup(d.binary, d.token, target, mintURL, d.gcpProjectID, e2etest.TryRunCLI, d.logf); err != nil {
		d.teardownPreview()
		return nil, err
	}

	if err := install.ValidatePerRepoPostInstall(ctx, d.client, org, repo); err != nil {
		d.teardownPreview()
		return nil, err
	}

	return install.NewPerRepoState(org, repo, mintURL), nil
}

func (d *driver) Teardown(ctx context.Context, org string, state install.State) error {
	repo := state.TestRepo()
	d.logf("[install] tearing down per-repo install on %s/%s", org, repo)
	e2etest.TeardownPerRepoInstall(ctx, d.client, d.token, org, repo, d.logf)

	d.teardownPreview()
	return nil
}

// deployCFMint deploys a Cloudflare Worker preview mint and returns the
// derived preview URL.
func (d *driver) deployCFMint(alias, org string) (string, error) {
	args := []string{
		"mint", "deploy",
		"--platform", "cloudflare",
		"--preview", alias,
		"--worker-name", d.workerName,
		"--pem-dir", d.cfg.PEMDir,
		"--allowed-orgs", d.cfg.AllowedOrgs,
		"--per-repo-wif-repos", d.cfg.PerRepoWIFRepos,
	}

	d.logf("[cfmint] deploying preview mint: fullsend %s", strings.Join(args, " "))
	if _, err := e2etest.TryRunCLI(d.binary, d.token, args...); err != nil {
		return "", fmt.Errorf("mint deploy --platform=cloudflare --preview=%s: %w", alias, err)
	}

	mintURL := PreviewMintURL(alias, d.workerName)
	d.logf("[cfmint] preview mint deployed at %s", mintURL)
	return mintURL, nil
}

// teardownPreview tears down the CF preview mint if one was deployed.
func (d *driver) teardownPreview() {
	if d.previewAlias == "" {
		return
	}
	args := []string{
		"mint", "delete",
		"--platform", "cloudflare",
		"--preview", d.previewAlias,
		"--yolo",
	}

	d.logf("[cfmint] tearing down preview mint: fullsend %s", strings.Join(args, " "))
	if _, err := e2etest.TryRunCLI(d.binary, d.token, args...); err != nil {
		// Log but don't fail — the preview is ephemeral and will
		// expire. A teardown failure should not mask test results.
		d.logf("[cfmint] preview mint teardown failed: %v", err)
	} else {
		d.logf("[cfmint] preview mint torn down (alias=%s)", d.previewAlias)
	}
}
