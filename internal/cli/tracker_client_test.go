package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewTrackerClient_UnsupportedTracker(t *testing.T) {
	_, err := newTrackerClient("servicenow", "", "", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported tracker")
	assert.Contains(t, err.Error(), "servicenow")
}

func TestNewTrackerClient_GitHub_NoToken(t *testing.T) {
	// With no token and no env var, GitHub falls back to `gh auth token`
	// which may or may not be available. We just verify it doesn't panic.
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	_, err := newTrackerClient(TrackerGitHub, "", "", "")
	// Error is acceptable (no token available in CI) — we're testing
	// that the code path runs without panicking.
	if err != nil {
		assert.Contains(t, err.Error(), "token")
	}
}

func TestNewTrackerClient_GitHub_ExplicitToken(t *testing.T) {
	tc, err := newTrackerClient(TrackerGitHub, "ghp_test123", "", "")
	assert.NoError(t, err)
	assert.NotNil(t, tc)
}

func TestNewTrackerClient_GitLab_NoToken(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "")
	_, err := newTrackerClient(TrackerGitLab, "", "", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "GitLab token")
}

func TestNewTrackerClient_GitLab_ExplicitToken(t *testing.T) {
	tc, err := newTrackerClient(TrackerGitLab, "glpat-test123", "", "")
	assert.NoError(t, err)
	assert.NotNil(t, tc)
}

func TestNewTrackerClient_Jira_NoToken(t *testing.T) {
	t.Setenv("JIRA_TOKEN", "")
	_, err := newTrackerClient(TrackerJira, "", "https://test.atlassian.net", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "JIRA_TOKEN")
}

func TestNewTrackerClient_Jira_NoBaseURL(t *testing.T) {
	t.Setenv("JIRA_BASE_URL", "")
	_, err := newTrackerClient(TrackerJira, "token123", "", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "JIRA_BASE_URL")
}

func TestNewTrackerClient_Jira_Valid(t *testing.T) {
	tc, err := newTrackerClient(TrackerJira, "token123", "https://test.atlassian.net", "user@example.com")
	assert.NoError(t, err)
	assert.NotNil(t, tc)
}

func TestNewTrackerClient_Jira_EnvVars(t *testing.T) {
	t.Setenv("JIRA_TOKEN", "env-token")
	t.Setenv("JIRA_BASE_URL", "https://env.atlassian.net")
	t.Setenv("JIRA_USER_EMAIL", "env@example.com")

	tc, err := newTrackerClient(TrackerJira, "", "", "")
	assert.NoError(t, err)
	assert.NotNil(t, tc)
}

func TestResolveJiraToken_Explicit(t *testing.T) {
	token, err := resolveJiraToken("explicit-token")
	assert.NoError(t, err)
	assert.Equal(t, "explicit-token", token)
}

func TestResolveJiraToken_EnvVar(t *testing.T) {
	t.Setenv("JIRA_TOKEN", "env-token")
	token, err := resolveJiraToken("")
	assert.NoError(t, err)
	assert.Equal(t, "env-token", token)
}

func TestResolveJiraToken_Missing(t *testing.T) {
	t.Setenv("JIRA_TOKEN", "")
	_, err := resolveJiraToken("")
	assert.Error(t, err)
}
