package repos

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/fullsend-ai/fullsend/internal/forge"
)

// Change describes a single field that needs to be updated to reconcile
// a repo's actual state with the manifest's desired state.
type Change struct {
	Owner    string `json:"owner"`
	Repo     string `json:"repo"`
	Field    string `json:"field"`
	Type     string `json:"type"`                // "variable" or "secret"
	Action   string `json:"action"`              // "create" or "update"
	OldValue string `json:"old_value,omitempty"` // empty for secrets (not readable)
	NewValue string `json:"new_value,omitempty"` // empty for secrets
}

// DiffResult holds the output of a diff operation.
type DiffResult struct {
	Changes  []Change `json:"changes"`
	Warnings []string `json:"warnings,omitempty"`
}

// SyncResult holds the output of a sync operation.
// FailedRepo identifies a repo that failed sync.
type FailedRepo struct {
	Owner string `json:"owner"`
	Repo  string `json:"repo"`
}

type SyncResult struct {
	Applied     []Change     `json:"applied"`
	Failed      int          `json:"failed,omitempty"`
	FailedRepos []FailedRepo `json:"failed_repos,omitempty"`
	Warnings    []string     `json:"warnings,omitempty"`
}

// managedVariables lists the repo variables that sync reconciles.
// Values are logged in cleartext via progress callbacks — only add
// non-sensitive fields here. Use managedSecrets for sensitive data.
// The guard variable (FULLSEND_PER_REPO_INSTALL) is NOT included here —
// it is managed by `repos install`. diffRepo skips repos where the guard
// is not "true" to avoid bricking future install runs.
var managedVariables = []struct {
	name      string
	resolveFn func(cfg ResolvedConfig) string
}{
	{"FULLSEND_MINT_URL", func(cfg ResolvedConfig) string { return cfg.MintURL }},
}

// managedSecrets is intentionally empty. Secrets are written once at
// install time (`repos install`) and are NOT reconciled by sync.
// GCP project ID and WIF provider are sensitive install-time-only
// values — sync only reconciles variables.
var managedSecrets = []struct {
	name      string
	resolveFn func(cfg ResolvedConfig) string
}{}

func validateConcurrency(n int) error {
	if n < 1 || n > 32 {
		return fmt.Errorf("concurrency must be between 1 and 32, got %d", n)
	}
	return nil
}

// Diff compares the manifest's desired state against the actual forge
// state and returns a list of changes needed to reconcile. Only examines
// repos that are already installed (guard variable is "true");
// uninstalled repos are reported as warnings.
//
// For secrets, Diff only reports missing secrets (action "create")
// because secret values cannot be read back for comparison.
func Diff(ctx context.Context, manifest *Manifest, clients ForgeClientFactory, maxConcurrency int, repoFilter []string) (*DiffResult, error) {
	if err := validateConcurrency(maxConcurrency); err != nil {
		return nil, err
	}

	resolved, err := manifest.ExpandGlobs(ctx, clients)
	if err != nil {
		return nil, fmt.Errorf("resolving repos: %w", err)
	}

	var filterWarnings []string
	if len(repoFilter) > 0 {
		var unmatched []string
		resolved, unmatched, err = filterRepos(resolved, repoFilter)
		if err != nil {
			return nil, err
		}
		for _, p := range unmatched {
			filterWarnings = append(filterWarnings, fmt.Sprintf("--repo filter %q matched no manifest entries", p))
		}
	}

	type repoResult struct {
		changes  []Change
		warnings []string
	}

	results := make([]repoResult, len(resolved))
	sem := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup

	for i, rr := range resolved {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return nil, ctx.Err()
		}
		wg.Add(1)
		go func(idx int, rr ResolvedRepo) {
			defer wg.Done()
			defer func() { <-sem }()

			cfg := manifest.ResolveConfigForEntry(rr.Owner, rr.Repo, rr.Entry)
			fc, fcErr := clients.ConfigFor(cfg.Forge)
			if fcErr != nil {
				results[idx] = repoResult{warnings: []string{fmt.Sprintf("%s/%s: forge client error: %v", rr.Owner, rr.Repo, fcErr)}}
				return
			}
			cfg.ForgeConfig = fc
			changes, warnings, _ := diffRepo(ctx, cfg)
			results[idx] = repoResult{changes: changes, warnings: warnings}
		}(i, rr)
	}
	wg.Wait()

	allChanges := make([]Change, 0)
	allWarnings := append([]string{}, filterWarnings...)
	for _, r := range results {
		allChanges = append(allChanges, r.changes...)
		allWarnings = append(allWarnings, r.warnings...)
	}

	return &DiffResult{Changes: allChanges, Warnings: allWarnings}, nil
}

// diffRepo computes the changes needed for a single repo.
// The returned bool is true when the repo was successfully examined;
// false means a fatal condition (API error, guard missing) and callers
// should not attempt further writes.
func diffRepo(ctx context.Context, cfg ResolvedConfig) ([]Change, []string, bool) {
	owner, repo := cfg.Owner, cfg.Repo
	client := cfg.ForgeConfig.Client
	vars, err := client.ListRepoVariables(ctx, owner, repo)
	if err != nil {
		return nil, []string{fmt.Sprintf("%s/%s: error listing variables: %v", owner, repo, err)}, false
	}

	guard := vars[forge.PerRepoGuardVar]
	if guard != "true" {
		return nil, []string{fmt.Sprintf("%s/%s: not installed (guard variable missing) — run `repos install`", owner, repo)}, false
	}

	var changes []Change
	var warnings []string

	for _, mv := range managedVariables {
		desired := mv.resolveFn(cfg)
		if desired == "" {
			continue
		}
		actual, exists := vars[mv.name]
		if !exists {
			changes = append(changes, Change{
				Owner:    owner,
				Repo:     repo,
				Field:    mv.name,
				Type:     "variable",
				Action:   "create",
				NewValue: desired,
			})
		} else if actual != desired {
			changes = append(changes, Change{
				Owner:    owner,
				Repo:     repo,
				Field:    mv.name,
				Type:     "variable",
				Action:   "update",
				OldValue: actual,
				NewValue: desired,
			})
		}
	}

	for _, ms := range managedSecrets {
		desired := ms.resolveFn(cfg)
		if desired == "" {
			continue
		}
		exists, secretErr := client.RepoSecretExists(ctx, owner, repo, ms.name)
		if secretErr != nil {
			warnings = append(warnings, fmt.Sprintf("%s/%s: error checking secret %s: %v", owner, repo, ms.name, secretErr))
			continue
		}
		if !exists {
			changes = append(changes, Change{
				Owner:  owner,
				Repo:   repo,
				Field:  ms.name,
				Type:   "secret",
				Action: "create",
			})
		}
	}

	return changes, warnings, true
}

// Sync reconciles configuration drift for installed repos by applying
// variable changes to match the manifest's desired state. Variables are
// only written when drift is detected. Secrets are written once at
// install time and are not reconciled by sync.
//
// Sync does NOT touch scaffold shim version (@ref) or harness files.
// Version changes are handled by the upgrade phase of repos install.
func Sync(ctx context.Context, manifest *Manifest, clients ForgeClientFactory, maxConcurrency int, repoFilter []string, progress ProgressFunc) (*SyncResult, error) {
	if err := validateConcurrency(maxConcurrency); err != nil {
		return nil, err
	}

	if progress == nil {
		progress = func(_, _, _ string) {}
	}

	resolved, err := manifest.ExpandGlobs(ctx, clients)
	if err != nil {
		return nil, fmt.Errorf("resolving repos: %w", err)
	}

	var syncFilterWarnings []string
	if len(repoFilter) > 0 {
		var unmatched []string
		resolved, unmatched, err = filterRepos(resolved, repoFilter)
		if err != nil {
			return nil, err
		}
		for _, p := range unmatched {
			syncFilterWarnings = append(syncFilterWarnings, fmt.Sprintf("--repo filter %q matched no manifest entries", p))
		}
	}

	type repoResult struct {
		applied  []Change
		warnings []string
		failed   bool
	}

	results := make([]repoResult, len(resolved))
	sem := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup

	for i, rr := range resolved {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return nil, ctx.Err()
		}
		wg.Add(1)
		go func(idx int, rr ResolvedRepo) {
			defer wg.Done()
			defer func() { <-sem }()

			cfg := manifest.ResolveConfigForEntry(rr.Owner, rr.Repo, rr.Entry)
			repoFullName := rr.Owner + "/" + rr.Repo

			fc, fcErr := clients.ConfigFor(cfg.Forge)
			if fcErr != nil {
				results[idx] = repoResult{warnings: []string{fmt.Sprintf("%s: forge client error: %v", repoFullName, fcErr)}}
				return
			}
			cfg.ForgeConfig = fc

			changes, diffWarnings, ok := diffRepo(ctx, cfg)
			var res repoResult
			res.warnings = append(res.warnings, diffWarnings...)

			if !ok {
				results[idx] = res
				return
			}

			if len(changes) == 0 {
				secretChanges, secretErr := ensureSecrets(ctx, cfg, progress)
				res.applied = append(res.applied, secretChanges...)
				if secretErr != nil {
					res.warnings = append(res.warnings, secretErr.Error())
					res.failed = true
					progress(repoFullName, "error", secretErr.Error())
				}
				results[idx] = res
				return
			}

			applied, applyErr := applyChanges(ctx, cfg, changes, progress)
			res.applied = append(res.applied, applied...)
			if applyErr != nil {
				res.warnings = append(res.warnings, applyErr.Error())
				res.failed = true
				progress(repoFullName, "error", applyErr.Error())
			} else {
				progress(repoFullName, "done", fmt.Sprintf("applied %d changes", len(applied)))
			}
			results[idx] = res
		}(i, rr)
	}
	wg.Wait()

	allApplied := make([]Change, 0)
	syncWarnings := append([]string{}, syncFilterWarnings...)
	failedCount := 0
	var failedRepos []FailedRepo
	for i, r := range results {
		allApplied = append(allApplied, r.applied...)
		syncWarnings = append(syncWarnings, r.warnings...)
		if r.failed {
			failedCount++
			failedRepos = append(failedRepos, FailedRepo{Owner: resolved[i].Owner, Repo: resolved[i].Repo})
		}
	}

	result := &SyncResult{Applied: allApplied, Failed: failedCount, FailedRepos: failedRepos, Warnings: syncWarnings}
	if failedCount > 0 {
		return result, fmt.Errorf("%d repos failed to sync", failedCount)
	}
	return result, nil
}

// ensureSecrets writes all managed secrets for convergence, since their
// values cannot be read back for comparison.
func ensureSecrets(ctx context.Context, cfg ResolvedConfig, progress ProgressFunc) ([]Change, error) {
	owner, repo := cfg.Owner, cfg.Repo
	client := cfg.ForgeConfig.Client
	repoFullName := owner + "/" + repo
	var applied []Change

	for _, ms := range managedSecrets {
		value := ms.resolveFn(cfg)
		if value == "" {
			continue
		}
		progress(repoFullName, "sync", fmt.Sprintf("ensure secret %s", ms.name))
		if err := client.CreateRepoSecret(ctx, owner, repo, ms.name, value); err != nil {
			return applied, fmt.Errorf("%s/%s: setting secret %s: %w", owner, repo, ms.name, err)
		}
		applied = append(applied, Change{
			Owner:  owner,
			Repo:   repo,
			Field:  ms.name,
			Type:   "secret",
			Action: "update",
		})
	}

	return applied, nil
}

func applyChanges(ctx context.Context, cfg ResolvedConfig, changes []Change, progress ProgressFunc) ([]Change, error) {
	owner, repo := cfg.Owner, cfg.Repo
	client := cfg.ForgeConfig.Client
	repoFullName := owner + "/" + repo
	var applied []Change

	for _, c := range changes {
		if c.Type != "variable" {
			continue
		}
		progress(repoFullName, "sync", fmt.Sprintf("%s %s=%s", c.Action, c.Field, c.NewValue))
		if err := client.CreateOrUpdateRepoVariable(ctx, owner, repo, c.Field, c.NewValue); err != nil {
			return applied, fmt.Errorf("%s/%s: setting variable %s: %w", owner, repo, c.Field, err)
		}
		applied = append(applied, c)
	}

	secretChanges, secretErr := ensureSecrets(ctx, cfg, progress)
	applied = append(applied, secretChanges...)

	return applied, secretErr
}

// FormatDiffTable renders a DiffResult as a human-readable table.
func FormatDiffTable(result *DiffResult) string {
	if len(result.Changes) == 0 && len(result.Warnings) == 0 {
		return "No changes needed — all repos match the manifest.\n"
	}

	var b strings.Builder

	if len(result.Changes) > 0 {
		maxRepo := len("REPO")
		maxField := len("FIELD")
		for _, c := range result.Changes {
			name := c.Owner + "/" + c.Repo
			if len(name) > maxRepo {
				maxRepo = len(name)
			}
			if len(c.Field) > maxField {
				maxField = len(c.Field)
			}
		}

		fmt.Fprintf(&b, "%-*s  %-*s  %-20s  %s\n", maxRepo, "REPO", maxField, "FIELD", "CURRENT", "DESIRED")
		for _, c := range result.Changes {
			name := c.Owner + "/" + c.Repo
			current := c.OldValue
			desired := c.NewValue
			if c.Type == "secret" {
				current = "(missing)"
				desired = "(secret value)"
			}
			if current == "" {
				current = "(not set)"
			}
			fmt.Fprintf(&b, "%-*s  %-*s  %-20s  %s\n", maxRepo, name, maxField, c.Field, current, desired)
		}
	}

	for _, w := range result.Warnings {
		fmt.Fprintf(&b, "WARNING: %s\n", w)
	}

	return b.String()
}
