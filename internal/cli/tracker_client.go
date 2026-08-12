package cli

import (
	"fmt"
	"os"

	gh "github.com/fullsend-ai/fullsend/internal/forge/github"
	"github.com/fullsend-ai/fullsend/internal/forge/jira"
	"github.com/fullsend-ai/fullsend/internal/tracker"
)

const (
	// TrackerGitHub is the --tracker value for GitHub.
	TrackerGitHub = "github"
	// TrackerGitLab is the --tracker value for GitLab.
	TrackerGitLab = "gitlab"
	// TrackerJira is the --tracker value for Jira.
	TrackerJira = "jira"
)

// newTrackerClient creates a tracker.Client for the given tracker type.
//
// For GitHub and GitLab, it wraps the corresponding forge.Client via
// tracker.NewForgeClient. For Jira, it creates a jira.LiveClient and
// wraps it via tracker.NewJiraClient. Token resolution mirrors the
// existing forge client factory patterns.
//
// The token parameter overrides environment-variable resolution when
// non-empty (for the --token CLI flag). jiraBaseURL and jiraEmail are
// Jira-specific parameters sourced from --jira-url/--jira-email flags
// or JIRA_BASE_URL/JIRA_USER_EMAIL environment variables.
func newTrackerClient(trackerName, token, jiraBaseURL, jiraEmail string) (tracker.Client, error) {
	switch trackerName {
	case TrackerGitHub:
		ghToken, err := resolveGitHubToken(token)
		if err != nil {
			return nil, err
		}
		fc := gh.New(ghToken)
		return tracker.NewForgeClient(fc), nil

	case TrackerGitLab:
		glToken, err := resolveGitLabTrackerToken(token)
		if err != nil {
			return nil, err
		}
		fc, err := newForgeClient("gitlab", glToken, "")
		if err != nil {
			return nil, err
		}
		return tracker.NewForgeClient(fc), nil

	case TrackerJira:
		jiraToken, err := resolveJiraToken(token)
		if err != nil {
			return nil, err
		}
		baseURL := jiraBaseURL
		if baseURL == "" {
			baseURL = os.Getenv("JIRA_BASE_URL")
		}
		if baseURL == "" {
			return nil, fmt.Errorf("--jira-url or JIRA_BASE_URL required for Jira tracker")
		}
		email := jiraEmail
		if email == "" {
			email = os.Getenv("JIRA_USER_EMAIL")
		}
		var opts []jira.Option
		opts = append(opts, jira.WithBaseURL(baseURL))
		if email != "" {
			opts = append(opts, jira.WithEmail(email))
		}
		jc, err := jira.New(jiraToken, opts...)
		if err != nil {
			return nil, fmt.Errorf("creating Jira client: %w", err)
		}
		tc, err := tracker.NewJiraClient(jc, baseURL)
		if err != nil {
			return nil, fmt.Errorf("creating Jira tracker client: %w", err)
		}
		return tc, nil

	default:
		return nil, fmt.Errorf("unsupported tracker %q: use %q, %q, or %q", trackerName, TrackerGitHub, TrackerGitLab, TrackerJira)
	}
}

// resolveGitHubToken returns a GitHub token from the explicit override,
// environment variables, or gh auth token — the same chain as
// resolveToken but accepting an explicit override first.
func resolveGitHubToken(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	return resolveToken()
}

// resolveGitLabTrackerToken returns a GitLab token from the explicit
// override or GITLAB_TOKEN environment variable.
func resolveGitLabTrackerToken(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	return resolveGitLabToken()
}

// resolveJiraToken returns a Jira API token from the explicit override
// or JIRA_TOKEN environment variable.
func resolveJiraToken(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if token := os.Getenv("JIRA_TOKEN"); token != "" {
		return token, nil
	}
	return "", fmt.Errorf("--token or JIRA_TOKEN required for Jira tracker")
}
