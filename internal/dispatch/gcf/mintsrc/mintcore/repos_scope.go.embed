package mintcore

import (
	"fmt"
	"strings"
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
	if i := strings.LastIndex(repository, "/"); i >= 0 {
		return repository[i+1:]
	}
	return repository
}

// validateReposScope enforces mint repos authorization after OIDC verification.
//
// Foreign (cross-org) requests may only omit repos (or use "*" → empty).
// Same-org requests must list exactly the requesting repository, unless
// PER_ORG_FOREIGN_COMPAT is on:
//   - caller .fullsend: any non-empty validated list
//   - other callers: exactly [.fullsend] or {requestingBare, .fullsend}
func validateReposScope(foreign bool, requestingRepo string, repos []string, compat bool) error {
	if foreign {
		if len(repos) != 0 {
			return fmt.Errorf("foreign mint requires empty repos")
		}
		return nil
	}

	if len(repos) == 0 {
		return fmt.Errorf("same-org mint requires non-empty repos")
	}

	bare := repositoryBareName(requestingRepo)
	if len(repos) == 1 && strings.EqualFold(repos[0], bare) {
		return nil
	}

	if !compat {
		return fmt.Errorf("same-org mint requires repos to be exactly the requesting repository")
	}

	if strings.EqualFold(bare, ".fullsend") {
		return nil
	}

	if len(repos) == 1 && strings.EqualFold(repos[0], ".fullsend") {
		return nil
	}
	if len(repos) == 2 {
		a, b := repos[0], repos[1]
		if (strings.EqualFold(a, bare) && strings.EqualFold(b, ".fullsend")) ||
			(strings.EqualFold(b, bare) && strings.EqualFold(a, ".fullsend")) {
			return nil
		}
	}

	return fmt.Errorf("repos scope not allowed under PER_ORG_FOREIGN_COMPAT for requesting repository")
}
