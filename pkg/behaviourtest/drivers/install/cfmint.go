package install

import (
	"crypto/rand"
	"fmt"
	"strings"

	"github.com/fullsend-ai/fullsend/pkg/e2etest"
)

// btPoolSize is the number of test-repo-NN repos in the pool org.
// Used to auto-generate --per-repo-wif-repos.
const btPoolSize = 12

// defaultWorkerName is the CF Worker script name used for preview mints.
const defaultWorkerName = "fullsend-mint"

// MintURLProvider is optionally implemented by State values that carry
// the effective mint URL. When a CF preview mint is deployed, the state
// carries the derived preview URL. The suite uses this to override
// e2eCfg.MintURL before creating the RepoEnsurer.
type MintURLProvider interface {
	MintURL() string
}

// cfMintConfig holds Cloudflare Worker mint deployment configuration.
// PEMDir is materialized from TEST_*_PEM env vars by e2etest.loadEnvConfig;
// all other deploy parameters (allowed-orgs, per-repo-wif-repos) are
// derived from the acquired pool org at deploy time.
type cfMintConfig struct {
	PEMDir string
}

// newCFMintConfig constructs a cfMintConfig from the environment config.
func newCFMintConfig(e2eCfg e2etest.EnvConfig) cfMintConfig {
	return cfMintConfig{
		PEMDir: e2eCfg.CFMintPEMDir,
	}
}

// previewMintURL returns the deterministic preview mint URL for the given
// preview alias. The URL format matches the CF provisioner:
// https://<alias>-<worker-name>.workers.dev
func previewMintURL(alias string) string {
	return fmt.Sprintf("https://%s-%s.workers.dev", alias, defaultWorkerName)
}

// generatePreviewAlias creates a unique preview alias for a BT run.
// Format: bt-<8-hex-chars> (e.g., bt-a1b2c3d4). The alias satisfies
// the CF preview alias validation: 2-63 lowercase alphanumeric or hyphens.
func generatePreviewAlias() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating preview alias: %w", err)
	}
	return fmt.Sprintf("bt-%x", b), nil
}

// deployCFMint deploys a Cloudflare Worker preview mint via the fullsend
// CLI and returns the derived preview mint URL.
//
// The deploy command passes all mint configuration (PEMs, allowlists,
// provenance) in one invocation. Preview Workers are self-contained:
// no separate enroll/unenroll/add-role/remove-role commands are used.
//
// Allowed-orgs defaults to the acquired pool org and per-repo-wif-repos
// is auto-generated from the pool naming convention. The CI workflow
// provides CLOUDFLARE_ACCOUNT_ID and CLOUDFLARE_API_TOKEN as env vars
// that the fullsend CLI / Wrangler pick up automatically.
func deployCFMint(
	binary, token string,
	cfg cfMintConfig,
	alias, org string,
	runCLI CLIRunnerFunc,
	logf func(string, ...any),
) (string, error) {
	args := []string{
		"mint", "deploy",
		"--platform", "cloudflare",
		"--preview", alias,
		"--pem-dir", cfg.PEMDir,
		"--allowed-orgs", org,
		"--per-repo-wif-repos", buildPerRepoWIFRepos(org),
	}

	logf("[cfmint] deploying preview mint: fullsend %s", strings.Join(args, " "))
	if _, err := runCLI(binary, token, args...); err != nil {
		return "", fmt.Errorf("mint deploy --platform=cloudflare --preview=%s: %w", alias, err)
	}

	mintURL := previewMintURL(alias)
	logf("[cfmint] preview mint deployed at %s", mintURL)
	return mintURL, nil
}

// teardownCFMint tears down a Cloudflare Worker preview mint via the
// fullsend CLI. This calls "fullsend mint delete --platform=cloudflare
// --preview=<alias> --yolo" to abandon the preview alias without
// affecting the durable Worker script.
func teardownCFMint(
	binary, token string,
	alias string,
	runCLI CLIRunnerFunc,
	logf func(string, ...any),
) error {
	args := []string{
		"mint", "delete",
		"--platform", "cloudflare",
		"--preview", alias,
		"--yolo",
	}

	logf("[cfmint] tearing down preview mint: fullsend %s", strings.Join(args, " "))
	if _, err := runCLI(binary, token, args...); err != nil {
		return fmt.Errorf("mint delete --platform=cloudflare --preview=%s: %w", alias, err)
	}

	logf("[cfmint] preview mint torn down (alias=%s)", alias)
	return nil
}

// buildPerRepoWIFRepos constructs the --per-repo-wif-repos value from
// the pool org and the standard BT repo naming convention. Returns a
// comma-separated list: org/test-repo-01,...,org/test-repo-12.
func buildPerRepoWIFRepos(org string) string {
	repos := make([]string, btPoolSize)
	for i := range btPoolSize {
		repos[i] = fmt.Sprintf("%s/test-repo-%02d", org, i+1)
	}
	return strings.Join(repos, ",")
}
