package mintcore

import (
	"fmt"
	"strings"
)

// Org-mode shape labels returned by validateReposScope when a per-org caller
// uses a broader-than-self same-org repos list. Empty means the default path
// (foreign empty repos, or same-org requesting-repo-only).
const (
	reposScopeShapeFullsendAny      = "fullsend-any"
	reposScopeShapeEnrolledFullsend = "enrolled-fullsend"
	reposScopeShapeEnrolledPair     = "enrolled-pair"
)

// normalizeMintRepos treats a single "*" entry as an alias for an empty
// repos list (installation-wide scope). Since repos is now required,
// ["*"] is the only path to installation-wide scope.
func normalizeMintRepos(repos []string) []string {
	if len(repos) == 1 && repos[0] == "*" {
		return nil
	}
	return repos
}

// EnvTruthy reports whether v is a truthy feature-flag value.
func EnvTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

// repositoryBareName returns the repository name without the org prefix.
func repositoryBareName(repository string) string {
	parts := strings.Split(repository, "/")
	return parts[len(parts)-1]
}

// validateReposScope enforces mint repos authorization after OIDC verification.
//
// INVARIANT: For foreign (cross-org) requests, this function returns nil
// unconditionally. Callers MUST invoke mintTokenCrossOrg for foreign
// requests with non-empty repos to perform repo-level FOREIGN grant
// authorization. Without that follow-up check, foreign requests with
// specific repos would bypass authorization entirely.
//
// Same-org requests differ based on whether the caller is per-repo or per-org:
//
//   - Per-repo callers (perRepo=true): must list exactly the requesting
//     repository. No broader shapes are allowed.
//   - Per-org callers (perRepo=false): may use org-mode shapes:
//     caller .fullsend: any non-empty validated list;
//     other callers: exactly [.fullsend] or {requestingBare, .fullsend}.
//     Same-org installation-wide (empty repos) is always denied.
//
// On success, shape is non-empty only when an org-mode exception matched.
func validateReposScope(isTargetForeign bool, requestingRepo string, repos []string, perRepo bool) (shape string, err error) {
	if isTargetForeign {
		// Empty repos → installation-wide (org-level FOREIGN grant).
		// Non-empty repos → repo-scoped (repo-level FOREIGN grant
		// validated later in mintTokenCrossOrg).
		return "", nil
	}

	if len(repos) == 0 {
		return "", fmt.Errorf("same-org mint requires non-empty repos")
	}

	bare := repositoryBareName(requestingRepo)
	if len(repos) == 1 && strings.EqualFold(repos[0], bare) {
		return "", nil
	}

	if perRepo {
		return "", fmt.Errorf("per-repo mint requires repos to be exactly the requesting repository")
	}

	// Per-org callers get org-mode shapes.
	if strings.EqualFold(bare, ".fullsend") {
		return reposScopeShapeFullsendAny, nil
	}

	if len(repos) == 1 && strings.EqualFold(repos[0], ".fullsend") {
		return reposScopeShapeEnrolledFullsend, nil
	}

	if len(repos) == 2 {
		a, b := repos[0], repos[1]
		if (strings.EqualFold(a, bare) && strings.EqualFold(b, ".fullsend")) ||
			(strings.EqualFold(b, bare) && strings.EqualFold(a, ".fullsend")) {
			return reposScopeShapeEnrolledPair, nil
		}
	}

	return "", fmt.Errorf("repos scope not allowed for per-org caller")
}
