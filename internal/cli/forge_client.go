package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/fullsend-ai/fullsend/internal/forge"
	gl "github.com/fullsend-ai/fullsend/internal/forge/gitlab"
	"github.com/fullsend-ai/fullsend/internal/repos"
	"github.com/spf13/cobra"
)

// resolveGitLabToken returns a GitLab personal or project access token
// from the environment. Unlike GitHub tokens, there is no `glab auth
// token` fallback — the token must be set explicitly.
func resolveGitLabToken() (string, error) {
	if token := os.Getenv("GITLAB_TOKEN"); token != "" {
		return token, nil
	}
	return "", fmt.Errorf("no GitLab token found: set GITLAB_TOKEN or pass --gitlab-token")
}

// newForgeClient creates a forge.Client for the given forge type.
// For GitHub, it uses the standard token resolution chain (GH_TOKEN,
// GITHUB_TOKEN, gh auth token). For GitLab, it uses GITLAB_TOKEN or
// the provided gitlabToken override.
func newForgeClient(forgeName, gitlabToken string) (forge.Client, error) {
	switch forgeName {
	case repos.ForgeGitLab:
		token := gitlabToken
		if token == "" {
			var err error
			token, err = resolveGitLabToken()
			if err != nil {
				return nil, err
			}
		}
		var opts []gl.Option
		if base := strings.TrimSpace(os.Getenv("GITLAB_API_URL")); base != "" {
			opts = append(opts, gl.WithBaseURL(base))
		}
		return gl.New(token, opts...)
	case repos.ForgeGitHub, "":
		token, err := resolveToken()
		if err != nil {
			return nil, err
		}
		return newGitHubLiveClient(token), nil
	default:
		return nil, fmt.Errorf("unsupported forge %q", forgeName)
	}
}

// forgeClientFromManifest resolves the dominant forge from the manifest
// defaults and returns the appropriate client. For manifests with a
// single forge type, this returns the correct client. Mixed-forge
// manifests (e.g., both GitHub and GitLab entries) are not yet fully
// supported — the default forge client is used and GitLab-specific
// entries will fail with a clear error.
func forgeClientFromManifest(m *repos.Manifest, gitlabToken string) (forge.Client, error) {
	forgeName := m.Defaults.Forge
	if forgeName == "" {
		forgeName = repos.ForgeGitHub
	}
	return newForgeClient(forgeName, gitlabToken)
}

// getGitLabToken extracts the --gitlab-token flag from the command chain.
func getGitLabToken(cmd *cobra.Command) string {
	token, _ := cmd.Flags().GetString("gitlab-token")
	if token == "" {
		token, _ = cmd.InheritedFlags().GetString("gitlab-token")
	}
	return token
}
