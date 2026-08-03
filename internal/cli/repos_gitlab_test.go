package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fullsend-ai/fullsend/internal/forge"
	"github.com/fullsend-ai/fullsend/internal/forge/gitlab"
	"github.com/fullsend-ai/fullsend/internal/ui"
)

func TestSetupGitLabBotToken(t *testing.T) {
	ctx := context.Background()

	t.Run("creates project access token and stores it", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v4/projects/", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id": 1, "name": "fullsend-bot", "token": "glpat-test-token", "active": true,
			})
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()

		glClient, err := gitlab.New("test-token", gitlab.WithBaseURL(srv.URL))
		require.NoError(t, err)

		fake := &forge.FakeClient{}
		var buf bytes.Buffer
		printer := ui.New(&buf)

		token, err := setupGitLabBotToken(ctx, fake, glClient, printer, "group", "project", "")
		require.NoError(t, err)
		assert.Equal(t, "glpat-test-token", token)

		require.Len(t, fake.CreatedSecrets, 1)
		assert.Equal(t, "FULLSEND_FORGE_TOKEN", fake.CreatedSecrets[0].Name)
	})

	t.Run("falls back to provided token on API failure", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v4/projects/", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()

		glClient, err := gitlab.New("test-token", gitlab.WithBaseURL(srv.URL))
		require.NoError(t, err)

		fake := &forge.FakeClient{}
		var buf bytes.Buffer
		printer := ui.New(&buf)

		token, err := setupGitLabBotToken(ctx, fake, glClient, printer, "group", "project", "glpat-fallback")
		require.NoError(t, err)
		assert.Equal(t, "glpat-fallback", token)

		require.Len(t, fake.CreatedSecrets, 1)
		assert.Equal(t, "FULLSEND_FORGE_TOKEN", fake.CreatedSecrets[0].Name)
	})

	t.Run("errors when API fails and no fallback token", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v4/projects/", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()

		glClient, err := gitlab.New("test-token", gitlab.WithBaseURL(srv.URL))
		require.NoError(t, err)

		fake := &forge.FakeClient{}
		var buf bytes.Buffer
		printer := ui.New(&buf)

		_, err = setupGitLabBotToken(ctx, fake, glClient, printer, "group", "project", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "--gitlab-bot-token")
	})
}

func TestSetupGitLabPipelineSchedules(t *testing.T) {
	ctx := context.Background()

	t.Run("enterprise gets dual schedules", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v4/metadata", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"enterprise": true})
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()

		glClient, err := gitlab.New("test-token", gitlab.WithBaseURL(srv.URL))
		require.NoError(t, err)

		fake := &forge.FakeClient{}
		var buf bytes.Buffer
		printer := ui.New(&buf)

		isEnterprise, err := setupGitLabPipelineSchedules(ctx, fake, glClient, printer, "group", "project", "main")
		require.NoError(t, err)
		assert.True(t, isEnterprise)
		require.Len(t, fake.CreatedSchedules, 2)
		assert.Equal(t, "*/5 * * * *", fake.CreatedSchedules[0].Cron)
		assert.Equal(t, "*/15 * * * *", fake.CreatedSchedules[1].Cron)
	})

	t.Run("free tier gets hourly schedule", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v4/metadata", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"enterprise": false})
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()

		glClient, err := gitlab.New("test-token", gitlab.WithBaseURL(srv.URL))
		require.NoError(t, err)

		fake := &forge.FakeClient{}
		var buf bytes.Buffer
		printer := ui.New(&buf)

		isEnterprise, err := setupGitLabPipelineSchedules(ctx, fake, glClient, printer, "group", "project", "main")
		require.NoError(t, err)
		assert.False(t, isEnterprise)
		require.Len(t, fake.CreatedSchedules, 1)
		assert.Equal(t, "0 * * * *", fake.CreatedSchedules[0].Cron)
	})
}

func TestSetupGitLabPipelineSchedules_FreeScheduleError(t *testing.T) {
	ctx := context.Background()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/metadata", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"enterprise": false})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	glClient, err := gitlab.New("test-token", gitlab.WithBaseURL(srv.URL))
	require.NoError(t, err)

	fake := forge.NewFakeClient()
	fake.Errors["CreatePipelineSchedule"] = fmt.Errorf("quota exceeded")
	var buf bytes.Buffer
	printer := ui.New(&buf)

	_, err = setupGitLabPipelineSchedules(ctx, fake, glClient, printer, "group", "project", "main")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "creating poll schedule")
}

func TestSetupGitLabPipelineSchedules_EnterpriseFastScheduleError(t *testing.T) {
	ctx := context.Background()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/metadata", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"enterprise": true})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	glClient, err := gitlab.New("test-token", gitlab.WithBaseURL(srv.URL))
	require.NoError(t, err)

	fake := forge.NewFakeClient()
	fake.Errors["CreatePipelineSchedule"] = fmt.Errorf("quota exceeded")
	var buf bytes.Buffer
	printer := ui.New(&buf)

	isEnterprise, err := setupGitLabPipelineSchedules(ctx, fake, glClient, printer, "group", "project", "main")
	require.Error(t, err)
	assert.True(t, isEnterprise)
	assert.Contains(t, err.Error(), "creating fast poll schedule")
}

func TestSetupGitLabPipelineSchedules_ListError(t *testing.T) {
	ctx := context.Background()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/metadata", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"enterprise": false})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	glClient, err := gitlab.New("test-token", gitlab.WithBaseURL(srv.URL))
	require.NoError(t, err)

	fake := forge.NewFakeClient()
	fake.Errors["ListPipelineSchedules"] = fmt.Errorf("forbidden")
	var buf bytes.Buffer
	printer := ui.New(&buf)

	isEnterprise, err := setupGitLabPipelineSchedules(ctx, fake, glClient, printer, "group", "project", "main")
	require.NoError(t, err)
	assert.False(t, isEnterprise)
	assert.Contains(t, buf.String(), "Could not list existing schedules")
}

func TestSetupGitLabBotToken_StoreCredentialFailure(t *testing.T) {
	ctx := context.Background()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id": 1, "name": "fullsend-bot", "token": "glpat-test", "active": true,
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	glClient, err := gitlab.New("test-token", gitlab.WithBaseURL(srv.URL))
	require.NoError(t, err)

	fake := forge.NewFakeClient()
	fake.Errors["CreateRepoSecret"] = fmt.Errorf("forbidden")
	var buf bytes.Buffer
	printer := ui.New(&buf)

	_, err = setupGitLabBotToken(ctx, fake, glClient, printer, "group", "project", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "storing bot PAT")
}

func TestCleanupGitLabPipelineSchedules(t *testing.T) {
	ctx := context.Background()

	fake := &forge.FakeClient{
		PipelineSchedules: map[string][]forge.PipelineSchedule{
			"group/project": {
				{ID: 1, Description: "fullsend fast poll", Active: true},
				{ID: 2, Description: "fullsend full poll", Active: true},
				{ID: 3, Description: "unrelated schedule", Active: true},
			},
		},
	}
	var buf bytes.Buffer
	printer := ui.New(&buf)

	err := cleanupGitLabPipelineSchedules(ctx, fake, printer, "group", "project")
	require.NoError(t, err)
	assert.Equal(t, []int64{1, 2}, fake.DeletedScheduleIDs)
}

func TestSetupGitLabBotToken_NilClient_FallbackToken(t *testing.T) {
	ctx := context.Background()
	fake := &forge.FakeClient{}
	var buf bytes.Buffer
	printer := ui.New(&buf)

	token, err := setupGitLabBotToken(ctx, fake, nil, printer, "group", "project", "glpat-manual")
	require.NoError(t, err)
	assert.Equal(t, "glpat-manual", token)
	require.Len(t, fake.CreatedSecrets, 1)
	assert.Equal(t, "FULLSEND_FORGE_TOKEN", fake.CreatedSecrets[0].Name)
}

func TestSetupGitLabBotToken_NilClient_NoFallback(t *testing.T) {
	ctx := context.Background()
	fake := &forge.FakeClient{}
	var buf bytes.Buffer
	printer := ui.New(&buf)

	_, err := setupGitLabBotToken(ctx, fake, nil, printer, "group", "project", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no GitLab client available")
}

func TestSetupGitLabBotToken_RevokesExistingBeforeCreate(t *testing.T) {
	ctx := context.Background()

	var revokedIDs []int
	var created bool
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/group%2Fproject/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": 10, "name": "fullsend-bot", "active": true},
				{"id": 11, "name": "other-token", "active": true},
			})
			return
		}
		if r.Method == http.MethodPost {
			created = true
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id": 20, "name": "fullsend-bot", "token": "glpat-new", "active": true,
			})
		}
	})
	mux.HandleFunc("/api/v4/projects/group%2Fproject/access_tokens/10", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			revokedIDs = append(revokedIDs, 10)
			w.WriteHeader(http.StatusNoContent)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	glClient, err := gitlab.New("test-token", gitlab.WithBaseURL(srv.URL))
	require.NoError(t, err)

	fake := &forge.FakeClient{}
	var buf bytes.Buffer
	printer := ui.New(&buf)

	token, err := setupGitLabBotToken(ctx, fake, glClient, printer, "group", "project", "")
	require.NoError(t, err)
	assert.Equal(t, "glpat-new", token)
	assert.Equal(t, []int{10}, revokedIDs, "should revoke existing fullsend-bot token")
	assert.True(t, created)
}

func TestSetupGitLabPipelineSchedules_DeletesExisting(t *testing.T) {
	ctx := context.Background()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/metadata", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"enterprise": false})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	glClient, err := gitlab.New("test-token", gitlab.WithBaseURL(srv.URL))
	require.NoError(t, err)

	fake := &forge.FakeClient{
		PipelineSchedules: map[string][]forge.PipelineSchedule{
			"group/project": {
				{ID: 5, Description: "fullsend fast poll", Active: true},
				{ID: 6, Description: "unrelated", Active: true},
			},
		},
	}
	var buf bytes.Buffer
	printer := ui.New(&buf)

	isEnterprise, err := setupGitLabPipelineSchedules(ctx, fake, glClient, printer, "group", "project", "main")
	require.NoError(t, err)
	assert.False(t, isEnterprise)
	assert.Equal(t, []int64{5}, fake.DeletedScheduleIDs, "should delete existing fullsend schedule")
	require.Len(t, fake.CreatedSchedules, 1)
}

func TestCleanupGitLabPipelineSchedules_ListError(t *testing.T) {
	ctx := context.Background()

	fake := forge.NewFakeClient()
	fake.Errors["ListPipelineSchedules"] = fmt.Errorf("forbidden")
	var buf bytes.Buffer
	printer := ui.New(&buf)

	err := cleanupGitLabPipelineSchedules(ctx, fake, printer, "group", "project")
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Could not list pipeline schedules")
}

func TestCleanupGitLabPipelineSchedules_DeleteError(t *testing.T) {
	ctx := context.Background()

	fake := &forge.FakeClient{
		PipelineSchedules: map[string][]forge.PipelineSchedule{
			"group/project": {
				{ID: 1, Description: "fullsend fast poll", Active: true},
			},
		},
	}
	fake.Errors = map[string]error{"DeletePipelineSchedule": fmt.Errorf("forbidden")}
	var buf bytes.Buffer
	printer := ui.New(&buf)

	err := cleanupGitLabPipelineSchedules(ctx, fake, printer, "group", "project")
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Failed to delete schedule ID 1")
	assert.Contains(t, buf.String(), "Removed 0 pipeline schedule(s)")
}

func TestCleanupGitLabBotToken(t *testing.T) {
	ctx := context.Background()

	t.Run("nil glClient is a no-op", func(t *testing.T) {
		var buf bytes.Buffer
		printer := ui.New(&buf)

		err := cleanupGitLabBotToken(ctx, nil, printer, "group", "project")
		require.NoError(t, err)
	})

	t.Run("revokes active fullsend-bot tokens", func(t *testing.T) {
		var revokedIDs []int
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v4/projects/group%2Fproject/access_tokens", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": 10, "name": "fullsend-bot", "active": true},
				{"id": 11, "name": "other-token", "active": true},
				{"id": 12, "name": "fullsend-bot", "active": false},
			})
		})
		mux.HandleFunc("/api/v4/projects/group%2Fproject/access_tokens/10", func(w http.ResponseWriter, r *http.Request) {
			revokedIDs = append(revokedIDs, 10)
			w.WriteHeader(http.StatusNoContent)
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()

		glClient, err := gitlab.New("test-token", gitlab.WithBaseURL(srv.URL))
		require.NoError(t, err)

		var buf bytes.Buffer
		printer := ui.New(&buf)

		err = cleanupGitLabBotToken(ctx, glClient, printer, "group", "project")
		require.NoError(t, err)
		assert.Equal(t, []int{10}, revokedIDs)
		assert.Contains(t, buf.String(), "Revoked bot access token")
	})

	t.Run("no active tokens", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v4/projects/group%2Fproject/access_tokens", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode([]map[string]interface{}{})
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()

		glClient, err := gitlab.New("test-token", gitlab.WithBaseURL(srv.URL))
		require.NoError(t, err)

		var buf bytes.Buffer
		printer := ui.New(&buf)

		err = cleanupGitLabBotToken(ctx, glClient, printer, "group", "project")
		require.NoError(t, err)
		assert.Contains(t, buf.String(), "No active bot access token found")
	})

	t.Run("list error is non-fatal", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v4/projects/group%2Fproject/access_tokens", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()

		glClient, err := gitlab.New("test-token", gitlab.WithBaseURL(srv.URL))
		require.NoError(t, err)

		var buf bytes.Buffer
		printer := ui.New(&buf)

		err = cleanupGitLabBotToken(ctx, glClient, printer, "group", "project")
		require.NoError(t, err)
		assert.Contains(t, buf.String(), "Could not list project access tokens")
	})
}
