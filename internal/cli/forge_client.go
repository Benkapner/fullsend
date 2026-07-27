package cli

import (
	"fmt"
	"os"
	"strings"
	"sync"

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

// forgeClientFactory lazily creates and caches per-forge API clients.
// Each client is created on first use and reused for subsequent calls
// with the same forge name. The sync.Mutex protects the client cache
// for concurrent goroutines in per-repo batch loops.
type forgeClientFactory struct {
	gitlabToken string
	mu          sync.Mutex
	clients     map[string]forge.Client
}

// newForgeClientFactory returns a ForgeClientFactory that lazily creates
// and caches forge clients. A GitLab token is only resolved if the
// factory is asked for a GitLab client, so single-forge GitHub manifests
// never require GITLAB_TOKEN.
func newForgeClientFactory(gitlabToken string) repos.ForgeClientFactory {
	return &forgeClientFactory{
		gitlabToken: gitlabToken,
		clients:     make(map[string]forge.Client),
	}
}

// ConfigFor returns a ForgeConfig with a live Client for the named forge.
// Clients are created lazily and cached — at most 2 clients per command
// invocation (one GitHub, one GitLab).
func (f *forgeClientFactory) ConfigFor(forgeName string) (repos.ForgeConfig, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Normalize empty forge name to github (backward compat).
	if forgeName == "" {
		forgeName = repos.ForgeGitHub
	}

	client, ok := f.clients[forgeName]
	if !ok {
		var err error
		client, err = newForgeClient(forgeName, f.gitlabToken)
		if err != nil {
			return repos.ForgeConfig{}, err
		}
		f.clients[forgeName] = client
	}

	cfg := repos.ForgeConfigFor(forgeName)
	cfg.Client = client
	return cfg, nil
}

// singleClientFactory wraps a single forge.Client as a ForgeClientFactory,
// returning the same client for any forge name. Used in tests and CLI
// test-override paths where a single FakeClient backs all operations.
type singleClientFactory struct {
	client forge.Client
}

func newSingleClientFactory(client forge.Client) repos.ForgeClientFactory {
	return &singleClientFactory{client: client}
}

func (f *singleClientFactory) ConfigFor(forgeName string) (repos.ForgeConfig, error) {
	if forgeName == "" {
		forgeName = repos.ForgeGitHub
	}
	cfg := repos.ForgeConfigFor(forgeName)
	cfg.Client = f.client
	return cfg, nil
}

// getGitLabToken extracts the --gitlab-token flag from the command chain.
func getGitLabToken(cmd *cobra.Command) string {
	token, _ := cmd.Flags().GetString("gitlab-token")
	if token == "" {
		token, _ = cmd.InheritedFlags().GetString("gitlab-token")
	}
	return token
}
