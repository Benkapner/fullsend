// cfmint.go implements the CF mint preview driver. It deploys a
// temporary Cloudflare Worker preview mint for behaviour tests and
// constructs a unified Driver that owns allocation, deallocation,
// ensure, and teardown.
//
// The preview mint is self-contained: all configuration (PEMs,
// allowlists, provenance) is passed at deploy time. Teardown
// abandons the preview alias.
package install

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"strings"

	"github.com/fullsend-ai/fullsend/internal/forge"
	"github.com/fullsend-ai/fullsend/pkg/e2etest"
)

// CFMintConfig holds parameters for the CF mint driver. The caller
// provides these from test-infra data; the driver does not hardcode
// org/repo/pool assumptions.
type CFMintConfig struct {
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

	// WorkflowHostRepos is a comma-separated list of repos whose
	// vendored workflows are allowed to mint tokens. Passed to
	// --workflow-host-repos on deploy. The caller builds the list
	// from pool naming conventions; the driver does not hardcode
	// org/repo assumptions.
	WorkflowHostRepos string

	// AppSet is the app set name for PEM bootstrap. Passed to --app-set
	// on deploy so the CLI verifies PEMs against the correct GitHub Apps.
	// For example, test PEMs use "fullsend-test" while production uses
	// "fullsend-ai". When empty, the CLI uses its own default.
	AppSet string
}

// NewCFMintFactory returns a Factory that closes over the CFMintConfig
// and poolSize. When called, the factory deploys a CF preview mint and
// returns a unified Driver that owns allocation, deallocation, ensure,
// and teardown.
//
// The caller provides the poolSize (number of test-repo-NN repos in
// the pool org). The factory handles mint deploy, ensurer creation,
// and driver construction internally — the suite does not need to
// compose these pieces by hand.
func NewCFMintFactory(cfg CFMintConfig, poolSize int) Factory {
	return func(
		org string,
		client forge.Client,
		token, binary, gcpProjectID string,
		logf func(string, ...any),
	) (Driver, error) {
		md, err := newCFMintDriver(client, token, binary, gcpProjectID, logf, cfg)
		if err != nil {
			return nil, fmt.Errorf("cfmint factory: creating mint driver: %w", err)
		}

		return buildCFMintFromMint(org, md, client, token, binary, gcpProjectID, poolSize, logf)
	}
}

// buildCFMintFromMint deploys the mint and constructs the composed driver.
// Extracted from NewCFMintFactory so the deploy -> compose path can be
// tested with a fake mintDriver (NewCFMintFactory hard-codes newCFMintDriver,
// which requires real PEM files and an external binary).
func buildCFMintFromMint(
	org string,
	md mintDriver,
	client forge.Client,
	token, binary, gcpProjectID string,
	poolSize int,
	logf func(string, ...any),
) (Driver, error) {
	ctx := context.Background()
	mintState, err := md.Install(ctx, org)
	if err != nil {
		return nil, fmt.Errorf("cfmint factory: deploying mint: %w", err)
	}

	// Extract the mint URL directly from the concrete state type.
	// Both cfmint and externalmint produce PerRepoState, so the type
	// assertion is safe here.
	var mintURL string
	if ps, ok := mintState.(*PerRepoState); ok {
		mintURL = ps.mintURL
	}

	// Create the ensurer with the deployed mint URL.
	e2eCfg := e2etest.EnvConfig{
		MintURL:      mintURL,
		GCPProjectID: gcpProjectID,
	}
	ens := newRepoEnsurer(e2eCfg, client, token, binary, logf)

	// Construct and return the composed driver.
	return newComposedDriver(org, md, mintState, ens, poolSize, logf)
}

// cfmintMintDriver deploys a CF Worker preview mint and uses the derived
// preview URL as the mint endpoint for fullsend github setup.
type cfmintMintDriver struct {
	client       forge.Client
	token        string
	binary       string
	gcpProjectID string
	logf         func(string, ...any)
	cfg          CFMintConfig
	workerName   string
	previewAlias string // set during Install
	cliRunner    CLIRunnerFunc
}

// Compile-time check that cfmintMintDriver implements mintDriver.
var _ mintDriver = (*cfmintMintDriver)(nil)

// newCFMintDriver creates a CF mint driver. Returns an error if the
// configuration is invalid (missing PEMs, empty suite name).
func newCFMintDriver(
	client forge.Client,
	token, binary, gcpProjectID string,
	logf func(string, ...any),
	cfg CFMintConfig,
) (mintDriver, error) {
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

	return &cfmintMintDriver{
		client:       client,
		token:        token,
		binary:       binary,
		gcpProjectID: gcpProjectID,
		logf:         logf,
		cfg:          cfg,
		workerName:   CFMintWorkerName(cfg.SuiteName),
		cliRunner:    e2etest.TryRunCLI,
	}, nil
}

// CFMintWorkerName derives the CF Worker script name from a suite name.
// The name clearly indicates it is the e2e/BT worker for that suite.
func CFMintWorkerName(suiteName string) string {
	return suiteName + "-mint"
}

// ParseCFMintURLFromOutput extracts the mint URL printed by `fullsend
// mint deploy`. The CLI prints a line like:
//
//	Worker deployed at https://<alias>-<worker>.<subdomain>.workers.dev
//
// This is the canonical way to obtain the preview URL from a deploy
// invocation; callers should not re-derive it because the correct
// workers.dev subdomain is only known at deploy time.
func ParseCFMintURLFromOutput(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "Worker deployed at") {
			continue
		}
		idx := strings.Index(line, "https://")
		if idx < 0 {
			continue
		}
		url := strings.TrimRight(line[idx:], " \t\n\r.,;")
		return url
	}
	return ""
}

// GenerateCFMintPreviewAlias creates a unique preview alias for a BT run.
// Format: bt-<8-hex-chars> (e.g., bt-a1b2c3d4). The alias satisfies
// the CF preview alias validation: 2-63 lowercase alphanumeric or hyphens.
func GenerateCFMintPreviewAlias() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating preview alias: %w", err)
	}
	return fmt.Sprintf("bt-%x", b), nil
}

// CFMintDeployArgs builds the CLI arguments for `fullsend mint deploy --platform=cloudflare`.
// Exported so unit tests can verify arg construction without shelling out.
//
// --allowed-orgs and --workflow-host-repos are always passed (even when
// empty) so the CLI sees them as explicitly changed. The CLI uses
// "flag changed" semantics: omitted flags preserve existing Worker
// bindings, while explicitly-empty values clear them. For per-repo
// mode the caller should set AllowedOrgs to "" to avoid dual-enrollment.
func CFMintDeployArgs(alias, workerName string, cfg CFMintConfig) []string {
	args := []string{
		"mint", "deploy",
		"--platform", "cloudflare",
		"--preview", alias,
		"--worker-name", workerName,
		"--pem-dir", cfg.PEMDir,
		"--allowed-orgs", cfg.AllowedOrgs,
		"--per-repo-wif-repos", cfg.PerRepoWIFRepos,
		"--workflow-host-repos", cfg.WorkflowHostRepos,
	}
	if cfg.AppSet != "" {
		args = append(args, "--app-set", cfg.AppSet)
	}
	return args
}

// CFMintTeardownArgs builds the CLI arguments for `fullsend mint delete --platform=cloudflare`.
// Exported so unit tests can verify arg construction without shelling out.
func CFMintTeardownArgs(previewAlias, workerName string) []string {
	return []string{
		"mint", "delete",
		"--platform", "cloudflare",
		"--preview", previewAlias,
		"--worker-name", workerName,
		"--yolo",
	}
}

func (d *cfmintMintDriver) Install(_ context.Context, org string) (State, error) {
	alias, err := GenerateCFMintPreviewAlias()
	if err != nil {
		return nil, err
	}
	d.previewAlias = alias

	mintURL, err := d.deployCFMint(alias, org)
	if err != nil {
		return nil, fmt.Errorf("deploying CF preview mint for BT: %w", err)
	}

	// The driver only manages the mint lifecycle. Per-repo github setup
	// and post-install validation are handled by the ensurer for each
	// leased pool repo.
	return NewPerRepoState(org, "", mintURL), nil
}

func (d *cfmintMintDriver) Teardown(_ context.Context, _ string, _ State) error {
	d.teardownPreview()
	return nil
}

// deployCFMint deploys a Cloudflare Worker preview mint and returns the
// deploy-reported preview URL (which includes the account's workers.dev
// subdomain).
func (d *cfmintMintDriver) deployCFMint(alias, org string) (string, error) {
	args := CFMintDeployArgs(alias, d.workerName, d.cfg)

	d.logf("[cfmint] deploying preview mint: fullsend %s", strings.Join(args, " "))
	output, err := d.cliRunner(d.binary, d.token, args...)
	if err != nil {
		return "", fmt.Errorf("mint deploy --platform=cloudflare --preview=%s: %w", alias, err)
	}

	mintURL := ParseCFMintURLFromOutput(output)
	if mintURL == "" {
		return "", fmt.Errorf("mint deploy --platform=cloudflare --preview=%s: could not parse mint URL from deploy output", alias)
	}
	d.logf("[cfmint] preview mint deployed at %s", mintURL)
	return mintURL, nil
}

// teardownPreview tears down the CF preview mint if one was deployed.
func (d *cfmintMintDriver) teardownPreview() {
	if d.previewAlias == "" {
		return
	}
	args := CFMintTeardownArgs(d.previewAlias, d.workerName)

	d.logf("[cfmint] tearing down preview mint: fullsend %s", strings.Join(args, " "))
	if _, err := d.cliRunner(d.binary, d.token, args...); err != nil {
		// Log but don't fail — the preview is ephemeral and will
		// expire. A teardown failure should not mask test results.
		d.logf("[cfmint] preview mint teardown failed: %v", err)
	} else {
		d.logf("[cfmint] preview mint torn down (alias=%s)", d.previewAlias)
	}
}
