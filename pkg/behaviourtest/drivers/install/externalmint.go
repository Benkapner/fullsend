// externalmint.go implements the external mint driver. It uses a
// pre-configured mint URL without deploying anything. This is the
// original install path retained for test configurations that use
// a pre-existing mint endpoint.
package install

import (
	"context"
	"fmt"

	"github.com/fullsend-ai/fullsend/internal/forge"
	"github.com/fullsend-ai/fullsend/pkg/e2etest"
)

// NewExternalMintFactory returns a Factory for an external
// (pre-configured) mint URL. When called, the factory returns a
// unified Driver that owns allocation, deallocation, ensure, and
// teardown. The driver uses the provided mint URL without deploying
// anything; teardown is a no-op.
func NewExternalMintFactory(mintURL string, poolSize int) Factory {
	return func(
		org string,
		client forge.Client,
		token, binary, gcpProjectID string,
		logf func(string, ...any),
	) (Driver, error) {
		if mintURL == "" {
			return nil, fmt.Errorf("external mint: mintURL is required")
		}
		md := &externalMintDriver{mintURL: mintURL}

		ctx := context.Background()
		mintState, err := md.Install(ctx, org)
		if err != nil {
			return nil, fmt.Errorf("external mint factory: %w", err)
		}

		e2eCfg := e2etest.EnvConfig{
			MintURL:      mintURL,
			GCPProjectID: gcpProjectID,
		}
		ens := newRepoEnsurer(e2eCfg, client, token, binary, logf)
		return newComposedDriver(org, md, mintState, ens, poolSize, logf)
	}
}

// externalMintDriver uses a pre-configured mint URL.
type externalMintDriver struct {
	mintURL string
}

// Compile-time check that externalMintDriver implements mintDriver.
var _ mintDriver = (*externalMintDriver)(nil)

func (d *externalMintDriver) Install(_ context.Context, org string) (State, error) {
	// The driver only provides the mint URL. Per-repo github setup and
	// post-install validation are handled by the ensurer for each
	// leased pool repo.
	return NewPerRepoState(org, "", d.mintURL), nil
}

func (d *externalMintDriver) Teardown(_ context.Context, _ string, _ State) error {
	// The external mint driver has no mint infrastructure to tear down.
	return nil
}
