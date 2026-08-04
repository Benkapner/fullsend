package mintcore

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Claims holds the subset of GitHub Actions OIDC JWT claims validated by the mint.
type Claims struct {
	Issuer          string   `json:"iss"`
	Audience        Audience `json:"aud"`
	IssuedAt        int64    `json:"iat"`
	Expiry          int64    `json:"exp"`
	Repository      string   `json:"repository"`
	RepositoryOwner string   `json:"repository_owner"`
	JobWorkflowRef  string   `json:"job_workflow_ref"`
}

// Audience handles the OIDC aud claim which can be a string or array of strings.
type Audience []string

// UnmarshalJSON handles both string and array-of-strings forms.
func (a *Audience) UnmarshalJSON(data []byte) error {
	var s string
	if json.Unmarshal(data, &s) == nil {
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("aud must not be empty")
		}
		*a = []string{s}
		return nil
	}
	var arr []string
	if err := json.Unmarshal(data, &arr); err != nil {
		return fmt.Errorf("aud must be a string or array of strings")
	}
	if len(arr) == 0 {
		return fmt.Errorf("aud must not be empty")
	}
	for _, v := range arr {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("aud must not contain empty values")
		}
	}
	*a = arr
	return nil
}

// Contains reports whether aud is in the audience list.
func (a Audience) Contains(aud string) bool {
	for _, v := range a {
		if v == aud {
			return true
		}
	}
	return false
}

const upstreamRepoPrefix = "fullsend-ai/fullsend/"

// ParseAllowedOrgs splits a comma-separated ALLOWED_ORGS value into trimmed entries.
func ParseAllowedOrgs(allowedOrgs string) []string {
	if allowedOrgs == "" {
		return nil
	}
	var orgs []string
	for _, o := range strings.Split(allowedOrgs, ",") {
		if trimmed := strings.TrimSpace(o); trimmed != "" {
			orgs = append(orgs, trimmed)
		}
	}
	return orgs
}

// IsPublicMint reports whether the given list contains the wildcard entry "*".
// It is used by provisioner and CLI code that checks ALLOWED_ORGS for legacy
// public-mode detection. New code should use IsPublicMintRepos instead, which
// checks PER_REPO_WIF_REPOS — the canonical source for public mint mode.
func IsPublicMint(allowedOrgs []string) bool {
	for _, entry := range allowedOrgs {
		if entry == "*" {
			return true
		}
	}
	return false
}

// IsPublicMintRepos reports whether perRepoWIFRepos contains the wildcard
// entry "*", meaning every repository gets per-repo treatment (public mint
// mode).
func IsPublicMintRepos(perRepoWIFRepos map[string]bool) bool {
	return perRepoWIFRepos["*"]
}

// IsPerRepoMode reports whether repository gets per-repo treatment.
// A repo is per-repo if it appears in PER_REPO_WIF_REPOS, or if
// PER_REPO_WIF_REPOS contains "*" (public mint mode).
func IsPerRepoMode(repository string, perRepoWIFRepos map[string]bool) bool {
	if perRepoWIFRepos["*"] {
		return true
	}
	return perRepoWIFRepos[strings.ToLower(repository)]
}

// AuthorizeToken performs the common authorization policy called by the
// handler after a verifier backend authenticates the token. It determines
// whether a caller gets per-repo or per-org treatment and validates
// accordingly:
//
//   - If the caller's repository is in PER_REPO_WIF_REPOS (or PER_REPO_WIF_REPOS
//     contains "*"), the caller gets per-repo treatment — authorized without
//     requiring repository_owner in ALLOWED_ORGS.
//   - Otherwise the caller's repository_owner must be in ALLOWED_ORGS (per-org).
//
// In both cases, repository_owner must be non-empty (defense-in-depth).
func AuthorizeToken(claims *Claims, allowedOrgs []string, perRepoWIFRepos map[string]bool) error {
	if claims.RepositoryOwner == "" {
		return fmt.Errorf("missing repository_owner claim")
	}
	if IsPerRepoMode(claims.Repository, perRepoWIFRepos) {
		// Per-repo callers don't need ALLOWED_ORGS membership.
		return nil
	}
	// Per-org path: org must be in ALLOWED_ORGS.
	return ValidateOrgAllowed(claims.RepositoryOwner, allowedOrgs)
}

// ValidateOrgAllowed checks that org is in the allowed list (case-insensitive).
// When allowedOrgs contains *, any non-empty org is accepted (public mint mode).
func ValidateOrgAllowed(org string, allowedOrgs []string) error {
	if org == "" {
		return fmt.Errorf("missing repository_owner claim")
	}
	if IsPublicMint(allowedOrgs) {
		return nil
	}
	for _, entry := range allowedOrgs {
		if strings.EqualFold(entry, org) {
			return nil
		}
	}
	return fmt.Errorf("repository_owner %q not in allowed orgs", org)
}

// ValidateWorkflowRef checks that a job_workflow_ref claim references an
// allowed workflow. In public mint mode (PER_REPO_WIF_REPOS contains *),
// only upstream fullsend-ai/fullsend workflows are accepted and the basename
// allowlist is skipped. In tight mode, the ref may belong to the token
// owner's .fullsend config repo, the upstream fullsend-ai/fullsend repo,
// or a registered per-repo repo, and the workflow file must be in the
// allowed list. The repository parameter is the token's repository claim
// and is used to cross-check per-repo matches.
func ValidateWorkflowRef(ref, repository string, perRepoWIFRepos map[string]bool, allowedWorkflowFiles []string) error {
	if ref == "" {
		return fmt.Errorf("missing job_workflow_ref claim")
	}

	lowerRef := strings.ToLower(ref)

	if IsPublicMintRepos(perRepoWIFRepos) {
		if !strings.HasPrefix(lowerRef, upstreamRepoPrefix) {
			return fmt.Errorf("job_workflow_ref must reference fullsend-ai/fullsend upstream workflows in public mint mode")
		}
		relPath := strings.TrimPrefix(lowerRef, upstreamRepoPrefix)
		if atIdx := strings.Index(relPath, "@"); atIdx > 0 {
			relPath = relPath[:atIdx]
		}
		if !strings.HasPrefix(relPath, ".github/workflows/") {
			return fmt.Errorf("job_workflow_ref does not reference a workflow file")
		}
		workflowFile := strings.TrimPrefix(relPath, ".github/workflows/")
		if workflowFile == "" || strings.Contains(workflowFile, "/") {
			return fmt.Errorf("job_workflow_ref does not reference a workflow file")
		}
		return nil
	}

	var relPath string
	matched := false

	// Extract the repository owner from the repository claim and only
	// check that specific org's .fullsend/ prefix. This ensures the
	// workflow ref matches the token's own org.
	if idx := strings.Index(repository, "/"); idx > 0 {
		repoOwner := strings.ToLower(repository[:idx])
		configPrefix := repoOwner + "/.fullsend/"
		if strings.HasPrefix(lowerRef, configPrefix) {
			relPath = strings.TrimPrefix(lowerRef, configPrefix)
			matched = true
		}
	}

	if !matched {
		if strings.HasPrefix(lowerRef, upstreamRepoPrefix) {
			relPath = strings.TrimPrefix(lowerRef, upstreamRepoPrefix)
			matched = true
		}
	}

	if !matched {
		repoKey := strings.ToLower(repository)
		if perRepoWIFRepos[repoKey] {
			repoPrefix := repoKey + "/"
			if strings.HasPrefix(lowerRef, repoPrefix) {
				relPath = strings.TrimPrefix(lowerRef, repoPrefix)
				matched = true
			}
		}
	}

	if !matched {
		return fmt.Errorf("job_workflow_ref does not reference .fullsend, upstream repo, or registered per-repo repo")
	}

	if atIdx := strings.Index(relPath, "@"); atIdx > 0 {
		relPath = relPath[:atIdx]
	}

	if !strings.HasPrefix(relPath, ".github/workflows/") {
		return fmt.Errorf("job_workflow_ref does not reference a workflow file")
	}

	workflowFile := strings.TrimPrefix(relPath, ".github/workflows/")
	for _, wf := range allowedWorkflowFiles {
		if wf == "*" || strings.EqualFold(wf, workflowFile) {
			return nil
		}
	}
	return fmt.Errorf("workflow file %q not in allowed list", workflowFile)
}
