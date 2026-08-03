package mintcore

import (
	"fmt"
	"strings"
)

// Compat shape labels returned by validateReposScope when PER_ORG_FOREIGN_COMPAT
// allows a broader-than-self same-org repos list. Empty means the default path
// (foreign empty repos, or same-org requesting-repo-only).
const (
	reposScopeShapeFullsendAny      = "fullsend-any"
	reposScopeShapeEnrolledFullsend = "enrolled-fullsend"
	reposScopeShapeEnrolledPair     = "enrolled-pair"
)

// normalizeMintRepos treats a single "*" entry as an alias for an empty
// repos list (installation-wide scope on the foreign path).
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
// Foreign (cross-org) requests may only omit repos (or use "*" → empty).
// Same-org requests must list exactly the requesting repository, unless
// PER_ORG_FOREIGN_COMPAT is on:
//   - caller .fullsend: any non-empty validated list
//   - other callers: exactly [.fullsend] or {requestingBare, .fullsend}
//
// On success, shape is non-empty only when a compat exception matched.
func validateReposScope(isTargetForeign bool, requestingRepo string, repos []string, compat bool) (shape string, err error) {
	if isTargetForeign {
		if len(repos) != 0 {
			return "", fmt.Errorf("foreign mint requires empty repos")
		}
		return "", nil
	}

	if len(repos) == 0 {
		return "", fmt.Errorf("same-org mint requires non-empty repos")
	}

	bare := repositoryBareName(requestingRepo)
	if len(repos) == 1 && strings.EqualFold(repos[0], bare) {
		return "", nil
	}

	if !compat {
		return "", fmt.Errorf("same-org mint requires repos to be exactly the requesting repository")
	}

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

	return "", fmt.Errorf("repos scope not allowed under PER_ORG_FOREIGN_COMPAT for requesting repository")
}
