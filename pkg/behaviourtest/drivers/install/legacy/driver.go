// Package legacy implements an install.Driver that uses a pre-configured
// mint URL for per-repo install. This is the original install path retained
// as a separate driver so other test configurations can select it.
package legacy

import (
	"context"
	"fmt"

	"github.com/fullsend-ai/fullsend/internal/forge"
	"github.com/fullsend-ai/fullsend/pkg/behaviourtest/drivers/install"
	"github.com/fullsend-ai/fullsend/pkg/behaviourtest/drivers/install/common"
	"github.com/fullsend-ai/fullsend/pkg/e2etest"
)

// driver uses a pre-configured mint URL for per-repo install.
type driver struct {
	client       forge.Client
	token        string
	binary       string
	mintURL      string
	gcpProjectID string
	repo         string
	logf         func(string, ...any)
}

// NewDriver creates a legacy install driver that uses the provided
// mintURL for fullsend github setup. repo is the target repository
// name (without org prefix) for the suite-level install (e.g.
// "test-repo-01").
func NewDriver(
	client forge.Client,
	token, binary, mintURL, gcpProjectID, repo string,
	logf func(string, ...any),
) (install.Driver, error) {
	if mintURL == "" {
		return nil, fmt.Errorf("legacy: mintURL is required")
	}
	if repo == "" {
		return nil, fmt.Errorf("legacy: repo is required")
	}
	return &driver{
		client:       client,
		token:        token,
		binary:       binary,
		mintURL:      mintURL,
		gcpProjectID: gcpProjectID,
		repo:         repo,
		logf:         logf,
	}, nil
}

func (d *driver) Install(ctx context.Context, org string) (install.State, error) {
	repo := d.repo
	target := org + "/" + repo

	if err := common.RunGitHubSetup(d.binary, d.token, target, d.mintURL, d.gcpProjectID, e2etest.TryRunCLI, d.logf); err != nil {
		return nil, err
	}

	if err := install.ValidatePerRepoPostInstall(ctx, d.client, org, repo); err != nil {
		return nil, err
	}

	return install.NewPerRepoState(org, repo, d.mintURL), nil
}

func (d *driver) Teardown(ctx context.Context, org string, state install.State) error {
	repo := state.TestRepo()
	d.logf("[install] tearing down per-repo install on %s/%s", org, repo)
	e2etest.TeardownPerRepoInstall(ctx, d.client, d.token, org, repo, d.logf)
	return nil
}
