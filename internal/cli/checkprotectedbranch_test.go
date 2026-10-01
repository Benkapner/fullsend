package cli

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fullsend-ai/fullsend/internal/forge"
	gl "github.com/fullsend-ai/fullsend/internal/forge/gitlab"
)

// protectedBranchAPIServer starts a fake GitLab API server exposing the
// two endpoints GetProtectedBranch calls: the exact-name lookup
// (/protected_branches/<branch>) and the paginated listing
// (/protected_branches) used for wildcard matching. exactStatus/exactBody
// control the exact-name response; wildcardNames lists additional
// protected-branch patterns (e.g. "release-*") returned by the listing.
func protectedBranchAPIServer(t *testing.T, exactStatus int, wildcardNames ...string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/group%2Fproject/protected_branches/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(exactStatus)
		if exactStatus == http.StatusOK {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"name":"main","push_access_levels":[{"access_level":40}],"merge_access_levels":[{"access_level":40}]}`))
		}
	})
	mux.HandleFunc("/api/v4/projects/group%2Fproject/protected_branches", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("["))
		for i, name := range wildcardNames {
			if i > 0 {
				w.Write([]byte(","))
			}
			_, _ = w.Write([]byte(`{"name":"` + name + `","push_access_levels":[],"merge_access_levels":[]}`))
		}
		w.Write([]byte("]"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestCheckProtectedBranch_NotProtected(t *testing.T) {
	srv := protectedBranchAPIServer(t, http.StatusNotFound)
	client, err := gl.New("test-token", gl.WithBaseURL(srv.URL))
	require.NoError(t, err)

	err = checkProtectedBranch(context.Background(), client, "group/project", "feature")
	assert.NoError(t, err)
}

func TestCheckProtectedBranch_ExactRuleFailsClosed(t *testing.T) {
	srv := protectedBranchAPIServer(t, http.StatusOK)
	client, err := gl.New("test-token", gl.WithBaseURL(srv.URL))
	require.NoError(t, err)

	err = checkProtectedBranch(context.Background(), client, "group/project", "main")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "protected")
}

func TestCheckProtectedBranch_WildcardRuleFailsClosed(t *testing.T) {
	// No exact-name rule for "release-1.0" (404), but a wildcard rule
	// "release-*" matches it — GetProtectedBranch unions both lookups,
	// so this must still fail closed.
	srv := protectedBranchAPIServer(t, http.StatusNotFound, "release-*")
	client, err := gl.New("test-token", gl.WithBaseURL(srv.URL))
	require.NoError(t, err)

	err = checkProtectedBranch(context.Background(), client, "group/project", "release-1.0")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "protected")
}

func TestCheckProtectedBranch_InvalidProjectPathFailsClosed(t *testing.T) {
	err := checkProtectedBranch(context.Background(), nil, "no-slash", "main")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "namespace/project")
}

type erroringProtectedBranchClient struct {
	forge.Client
}

func (erroringProtectedBranchClient) IsProtectedBranch(ctx context.Context, owner, repo, branch string) (bool, error) {
	return false, errors.New("boom")
}

func TestCheckProtectedBranch_APIErrorFailsClosed(t *testing.T) {
	err := checkProtectedBranch(context.Background(), erroringProtectedBranchClient{}, "group/project", "main")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}

func TestCheckProtectedBranchCmd_MissingProject(t *testing.T) {
	cmd := newCheckProtectedBranchCmd()
	cmd.SetArgs([]string{"--branch", "main"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--project is required")
}

func TestCheckProtectedBranchCmd_MissingBranch(t *testing.T) {
	cmd := newCheckProtectedBranchCmd()
	cmd.SetArgs([]string{"--project", "group/project"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--branch is required")
}

func TestCheckProtectedBranchCmd_MissingToken(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "")
	cmd := newCheckProtectedBranchCmd()
	cmd.SetArgs([]string{"--project", "group/project", "--branch", "main"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GitLab token")
}

func TestCheckProtectedBranchCmd_SucceedsWhenNotProtected(t *testing.T) {
	srv := protectedBranchAPIServer(t, http.StatusNotFound)
	t.Setenv("GITLAB_TOKEN", "test-token")
	cmd := newCheckProtectedBranchCmd()
	cmd.SetArgs([]string{"--project", "group/project", "--branch", "feature", "--gitlab-url", srv.URL})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	require.NoError(t, cmd.Execute())
}

func TestCheckProtectedBranchCmd_FailsWhenProtected(t *testing.T) {
	srv := protectedBranchAPIServer(t, http.StatusOK)
	t.Setenv("GITLAB_TOKEN", "test-token")
	cmd := newCheckProtectedBranchCmd()
	cmd.SetArgs([]string{"--project", "group/project", "--branch", "main", "--gitlab-url", srv.URL})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "protected")
}
