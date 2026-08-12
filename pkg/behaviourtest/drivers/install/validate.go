package install

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/fullsend-ai/fullsend/internal/config"
	"github.com/fullsend-ai/fullsend/internal/forge"
	"github.com/fullsend-ai/fullsend/internal/layers"
	"github.com/fullsend-ai/fullsend/internal/scaffold"
)

// ValidatePerRepoPostInstall checks that a per-repo install left the
// expected files and configuration in the target repo.
func ValidatePerRepoPostInstall(ctx context.Context, client forge.Client, org, repo string) error {
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
