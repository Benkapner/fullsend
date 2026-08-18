// repopool_external_mint.go implements the RepoPoolExternalMint driver.
// It uses a pre-configured mint URL without deploying anything. This is
// the original install path retained for test configurations that use a
// pre-existing mint endpoint.
package install

import (
	"context"
	"fmt"

	"github.com/fullsend-ai/fullsend/internal/forge"
	"github.com/fullsend-ai/fullsend/pkg/e2etest"
)

// NewRepoPoolExternalMint returns a Factory that, given an org name,
// returns a unified Driver backed by a pre-configured mint URL. The
// driver uses the existing mint without deploying anything; teardown
// is a no-op.
//
// The mint URL is read from e2etest env config. Pool size defaults to
// DefaultPoolSize (overridable via BEHAVIOUR_POOL_SIZE).
//
// Runtime dependencies (forge client, token, CLI binary path, GCP
// project, logger) are closed over — they do not appear on the
// Factory signature.
func NewRepoPoolExternalMint(
	client forge.Client,
	token, binary, gcpProjectID string,
	logf func(string, ...any),
) Factory {
	return func(org string) (Driver, error) {
		e2eCfg := e2etest.LoadEnvConfigLite()
		poolSize := envPoolSize()

		mintURL := e2eCfg.MintURL
		if mintURL == "" {
			return nil, fmt.Errorf("external mint: mintURL is required (set E2E_MINT_URL)")
		}

		md := &externalMintDriver{mintURL: mintURL}
		// Install is a no-op for external mint — just validates presence.
		_, err := md.Install(context.Background(), org)
		if err != nil {
			return nil, fmt.Errorf("external mint factory: %w", err)
		}

		ensCfg := e2etest.EnvConfig{
			MintURL:      mintURL,
			GCPProjectID: gcpProjectID,
		}
		ens := newRepoEnsurer(ensCfg, client, token, binary, logf)
		return newComposedDriver(org, md, ens, poolSize, logf)
	}
}

// externalMintDriver uses a pre-configured mint URL.
type externalMintDriver struct {
	mintURL string
}

// Compile-time check that externalMintDriver implements mintDriver.
var _ mintDriver = (*externalMintDriver)(nil)

func (d *externalMintDriver) Install(_ context.Context, _ string) (string, error) {
	// The driver only provides the mint URL. Per-repo github setup and
	// post-install validation are handled by the ensurer for each
	// leased pool repo.
	return d.mintURL, nil
}

func (d *externalMintDriver) Teardown(_ context.Context) error {
	// The external mint driver has no mint infrastructure to tear down.
	return nil
}
