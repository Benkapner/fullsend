package cli

import (
	"os"
	"strings"

	gh "github.com/fullsend-ai/fullsend/internal/forge/github"
	"github.com/fullsend-ai/fullsend/internal/repos"
)

// newGitHubLiveClient builds a GitHub API client. The manifestURL
// parameter, when non-empty, is the forge instance URL from the
// manifest's forge.github.url field. For GitHub.com the default
// API endpoint is used; for GitHub Enterprise Server the API URL
// is derived from the instance URL (<url>/api/v3).
//
// The GITHUB_API_URL environment variable is kept as a fallback for
// callers without a manifest (e.g., repos migrate) and for tests.
func newGitHubLiveClient(token, manifestURL string) *gh.LiveClient {
	client := gh.New(token)
	if apiURL := githubAPIURL(manifestURL); apiURL != "" {
		client = client.WithBaseURL(apiURL)
	} else if base := strings.TrimSpace(os.Getenv("GITHUB_API_URL")); base != "" {
		client = client.WithBaseURL(base)
	}
	return client
}

// githubAPIURL derives the GitHub REST API base URL from a forge
// instance URL. Returns "" for github.com (the client default) and
// for empty input (let env var or built-in default take over).
func githubAPIURL(instanceURL string) string {
	normalized := strings.TrimRight(instanceURL, "/")
	if normalized == "" || normalized == repos.DefaultGitHubURL {
		return ""
	}
	return normalized + "/api/v3"
}
