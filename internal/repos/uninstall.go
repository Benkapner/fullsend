package repos

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/fullsend-ai/fullsend/internal/forge"
)

var uninstallVariables = slices.Concat([]string{forge.PerRepoGuardVar}, requiredVariables)

var uninstallSecrets = requiredSecrets

// UninstallConfig holds all inputs for a multi-repo uninstall operation.
type UninstallConfig struct {
	Manifest       *Manifest
	Repos          []string
	DryRun         bool
	MaxConcurrency int
}

// UninstallResult holds the outcome of uninstalling fullsend from a single repo.
type UninstallResult struct {
	Owner           string
	Repo            string
	Success         bool
	Error           error
	WorkflowDeleted bool
	VarsDeleted     int
	SecretsDeleted  int
}

// Uninstall tears down fullsend from the specified repos.
//
// It runs in a single phase: parallel per-repo cleanup (bounded by
// MaxConcurrency) deletes the workflow file, then deletes variables and
// secrets.
//
// GCP WIF cleanup is handled separately via `inference deprovision`.
//
// Does NOT modify repos.yaml — use RemoveFromManifest for that.
func Uninstall(ctx context.Context, cfg UninstallConfig,
	clients ForgeClientFactory,
	progress ProgressFunc) ([]UninstallResult, error) {

	if len(cfg.Repos) == 0 {
		return nil, fmt.Errorf("at least one repo is required")
	}
	if cfg.MaxConcurrency <= 0 || cfg.MaxConcurrency > 32 {
		return nil, fmt.Errorf("MaxConcurrency must be between 1 and 32, got %d", cfg.MaxConcurrency)
	}
	if progress == nil {
		progress = func(_, _, _ string) {}
	}

	parsed := make([]struct{ owner, repo string }, len(cfg.Repos))
	for i, r := range cfg.Repos {
		owner, name, err := splitOwnerRepo(r)
		if err != nil {
			return nil, err
		}
		parsed[i].owner = owner
		parsed[i].repo = name
	}

	if cfg.DryRun {
		results := make([]UninstallResult, len(parsed))
		for i, p := range parsed {
			results[i] = UninstallResult{
				Owner:   p.owner,
				Repo:    p.repo,
				Success: true,
			}
			progress(p.owner+"/"+p.repo, "dry-run", "Would uninstall")
		}
		return results, nil
	}

	// Parallel per-repo cleanup.
	results := make([]UninstallResult, len(parsed))
	sem := make(chan struct{}, cfg.MaxConcurrency)
	var wg sync.WaitGroup

	for i, p := range parsed {
		wg.Add(1)
		go func(idx int, owner, repo string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				results[idx] = UninstallResult{
					Owner: owner,
					Repo:  repo,
					Error: ctx.Err(),
				}
				return
			}
			defer func() { <-sem }()

			forgeName := ""
			if cfg.Manifest != nil {
				if rc, ok := cfg.Manifest.ResolveConfigWithGlobs(owner, repo); ok {
					forgeName = rc.Forge
				}
			}
			fc, fcErr := clients.ConfigFor(forgeName)
			if fcErr != nil {
				results[idx] = UninstallResult{Owner: owner, Repo: repo, Error: fcErr}
				return
			}
			results[idx] = uninstallRepoResources(ctx, ResolvedConfig{Owner: owner, Repo: repo, ForgeConfig: fc}, progress)
		}(i, p.owner, p.repo)
	}
	wg.Wait()

	for i := range results {
		if results[i].Error == nil {
			results[i].Success = true
		}
	}

	return results, nil
}

func uninstallRepoResources(ctx context.Context, cfg ResolvedConfig, progress ProgressFunc) UninstallResult {
	owner, repo := cfg.Owner, cfg.Repo
	client := cfg.ForgeConfig.Client
	fc := cfg.ForgeConfig
	fullName := owner + "/" + repo
	result := UninstallResult{Owner: owner, Repo: repo}

	progress(fullName, "workflow", "Deleting workflow file")
	_, err := client.DeleteFiles(ctx, owner, repo,
		"chore: remove fullsend workflow", fc.WorkflowPaths)
	if err != nil {
		result.Error = fmt.Errorf("deleting workflow: %w", err)
		progress(fullName, "workflow", fmt.Sprintf("Failed: %v", err))
		return result
	}
	result.WorkflowDeleted = true
	progress(fullName, "workflow", "Workflow deleted")

	var varsDeleted, secretsDeleted int
	var varErr, secretErr error
	var innerWg sync.WaitGroup

	innerWg.Add(2)
	go func() {
		defer innerWg.Done()
		for _, name := range uninstallVariables {
			if delErr := client.DeleteRepoVariable(ctx, owner, repo, name); delErr != nil {
				varErr = fmt.Errorf("deleting variable %s: %w", name, delErr)
				return
			}
			varsDeleted++
		}
	}()
	go func() {
		defer innerWg.Done()
		for _, name := range uninstallSecrets {
			if delErr := client.DeleteRepoSecret(ctx, owner, repo, name); delErr != nil {
				secretErr = fmt.Errorf("deleting secret %s: %w", name, delErr)
				return
			}
			secretsDeleted++
		}
	}()
	innerWg.Wait()

	result.VarsDeleted = varsDeleted
	result.SecretsDeleted = secretsDeleted

	if varErr != nil && secretErr != nil {
		result.Error = errors.Join(varErr, secretErr)
		progress(fullName, "cleanup", fmt.Sprintf("Failed: %v; %v", varErr, secretErr))
		return result
	}
	if varErr != nil {
		result.Error = varErr
		progress(fullName, "vars", fmt.Sprintf("Failed: %v", varErr))
		return result
	}
	if secretErr != nil {
		result.Error = secretErr
		progress(fullName, "secrets", fmt.Sprintf("Failed: %v", secretErr))
		return result
	}

	progress(fullName, "done", fmt.Sprintf("Removed: %d vars, %d secrets", varsDeleted, secretsDeleted))
	return result
}

// splitOwnerRepo splits "owner/repo" and rejects glob characters. Callers
// that accept glob patterns must filter them out before calling this.
func splitOwnerRepo(fullName string) (string, string, error) {
	if !repoNamePattern.MatchString(fullName) {
		return "", "", fmt.Errorf("invalid repo format %q: expected owner/repo with alphanumeric, dash, dot, or underscore characters", fullName)
	}
	parts := strings.SplitN(fullName, "/", 2)
	return parts[0], parts[1], nil
}
