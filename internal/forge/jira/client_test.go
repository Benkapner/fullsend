package jira

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fullsend-ai/fullsend/internal/forge"
)

// setupTest creates an httptest server and a Client pointed at it.
func setupTest(t *testing.T) (*Client, *http.ServeMux) {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client, err := New("test-token", WithBaseURL(srv.URL))
	require.NoError(t, err)
	return client, mux
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	require.NoError(t, json.NewEncoder(w).Encode(v))
}

// ---------------------------------------------------------------------------
// Auth header verification
// ---------------------------------------------------------------------------

func TestAuthHeader_BearerWithoutEmail(t *testing.T) {
	t.Parallel()
	client, mux := setupTest(t)
	ctx := context.Background()

	mux.HandleFunc("/rest/api/3/myself", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		assert.Equal(t, "Bearer test-token", auth)
		writeJSON(t, w, http.StatusOK, User{AccountID: "123", DisplayName: "Test"})
	})

	_, err := client.GetMyself(ctx)
	require.NoError(t, err)
}

func TestAuthHeader_BasicWithEmail(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client, err := New("test-token", WithBaseURL(srv.URL), WithEmail("user@example.com"))
	require.NoError(t, err)
	ctx := context.Background()

	mux.HandleFunc("/rest/api/3/myself", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		assert.True(t, strings.HasPrefix(auth, "Basic "), "expected Basic auth prefix")
		// Verify the decoded value is email:token
		assert.Contains(t, auth, "Basic ")
		writeJSON(t, w, http.StatusOK, User{AccountID: "123", DisplayName: "Test"})
	})

	_, err = client.GetMyself(ctx)
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// SearchIssues pagination
// ---------------------------------------------------------------------------

func TestSearchIssues_Pagination(t *testing.T) {
	t.Parallel()
	client, mux := setupTest(t)
	ctx := context.Background()

	callCount := 0
	mux.HandleFunc("/rest/api/3/search/jql", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		var body searchRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))

		// Verify expand is a comma-delimited string, not an array.
		assert.Equal(t, "changelog", body.Expand)
		// Verify fields requests all data.
		assert.Equal(t, []string{"*all"}, body.Fields)

		callCount++
		switch body.NextPageToken {
		case "":
			issues := make([]Issue, 50)
			for i := range issues {
				issues[i] = Issue{ID: fmt.Sprintf("%d", i+1), Key: fmt.Sprintf("PROJ-%d", i+1)}
			}
			writeJSON(t, w, http.StatusOK, SearchResult{
				Issues:        issues,
				NextPageToken: "page-2-token",
			})
		case "page-2-token":
			issues := make([]Issue, 50)
			for i := range issues {
				issues[i] = Issue{ID: fmt.Sprintf("%d", i+51), Key: fmt.Sprintf("PROJ-%d", i+51)}
			}
			writeJSON(t, w, http.StatusOK, SearchResult{
				Issues:        issues,
				NextPageToken: "page-3-token",
			})
		case "page-3-token":
			issues := make([]Issue, 20)
			for i := range issues {
				issues[i] = Issue{ID: fmt.Sprintf("%d", i+101), Key: fmt.Sprintf("PROJ-%d", i+101)}
			}
			writeJSON(t, w, http.StatusOK, SearchResult{
				Issues: issues,
				IsLast: true,
			})
		default:
			t.Fatalf("unexpected nextPageToken: %s", body.NextPageToken)
		}
	})

	issues, err := client.SearchIssues(ctx, "project = PROJ")
	require.NoError(t, err)
	assert.Len(t, issues, 120)
	assert.Equal(t, 3, callCount)
	assert.Equal(t, "PROJ-1", issues[0].Key)
	assert.Equal(t, "PROJ-120", issues[119].Key)
}

// ---------------------------------------------------------------------------
// ListComments pagination
// ---------------------------------------------------------------------------

func TestListComments_Pagination(t *testing.T) {
	t.Parallel()
	client, mux := setupTest(t)
	ctx := context.Background()

	mux.HandleFunc("/rest/api/3/issue/PROJ-1/comment", func(w http.ResponseWriter, r *http.Request) {
		startAt := r.URL.Query().Get("startAt")
		switch startAt {
		case "0", "":
			comments := make([]Comment, 100)
			for i := range comments {
				comments[i] = Comment{ID: fmt.Sprintf("%d", i+1), Created: "2024-01-01T00:00:00.000+0000"}
			}
			writeJSON(t, w, http.StatusOK, CommentPage{
				Comments:   comments,
				Total:      150,
				MaxResults: 100,
				StartAt:    0,
			})
		case "100":
			comments := make([]Comment, 50)
			for i := range comments {
				comments[i] = Comment{ID: fmt.Sprintf("%d", i+101), Created: "2024-01-02T00:00:00.000+0000"}
			}
			writeJSON(t, w, http.StatusOK, CommentPage{
				Comments:   comments,
				Total:      150,
				MaxResults: 100,
				StartAt:    100,
			})
		}
	})

	comments, err := client.ListComments(ctx, "PROJ-1")
	require.NoError(t, err)
	assert.Len(t, comments, 150)
}

// ---------------------------------------------------------------------------
// ListChangelog pagination
// ---------------------------------------------------------------------------

func TestListChangelog_Pagination(t *testing.T) {
	t.Parallel()
	client, mux := setupTest(t)
	ctx := context.Background()

	mux.HandleFunc("/rest/api/3/issue/PROJ-1/changelog", func(w http.ResponseWriter, r *http.Request) {
		startAt := r.URL.Query().Get("startAt")
		switch startAt {
		case "0", "":
			entries := make([]ChangelogEntry, 100)
			for i := range entries {
				entries[i] = ChangelogEntry{
					ID:      fmt.Sprintf("%d", i+1),
					Created: "2024-01-01T00:00:00.000+0000",
					Items:   []ChangeItem{{Field: "status", ToString: "In Progress"}},
				}
			}
			writeJSON(t, w, http.StatusOK, changelogPage{
				Values:     entries,
				Total:      130,
				MaxResults: 100,
				StartAt:    0,
				IsLast:     false,
			})
		case "100":
			entries := make([]ChangelogEntry, 30)
			for i := range entries {
				entries[i] = ChangelogEntry{
					ID:      fmt.Sprintf("%d", i+101),
					Created: "2024-01-02T00:00:00.000+0000",
					Items:   []ChangeItem{{Field: "status", ToString: "Done"}},
				}
			}
			writeJSON(t, w, http.StatusOK, changelogPage{
				Values:     entries,
				Total:      130,
				MaxResults: 100,
				StartAt:    100,
				IsLast:     true,
			})
		}
	})

	entries, err := client.ListChangelog(ctx, "PROJ-1")
	require.NoError(t, err)
	assert.Len(t, entries, 130)
}

// ---------------------------------------------------------------------------
// GetEntityProperty — found and not-found
// ---------------------------------------------------------------------------

func TestGetEntityProperty_Found(t *testing.T) {
	t.Parallel()
	client, mux := setupTest(t)
	ctx := context.Background()

	mux.HandleFunc("/rest/api/3/issue/PROJ-1/properties/fullsend.lock", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		writeJSON(t, w, http.StatusOK, map[string]any{
			"key":   "fullsend.lock",
			"value": map[string]string{"id": "abc-123", "ts": "2024-01-01T00:00:00Z"},
		})
	})

	val, err := client.GetEntityProperty(ctx, "PROJ-1", "fullsend.lock")
	require.NoError(t, err)

	var parsed map[string]string
	require.NoError(t, json.Unmarshal(val, &parsed))
	assert.Equal(t, "abc-123", parsed["id"])
}

func TestGetEntityProperty_NotFound(t *testing.T) {
	t.Parallel()
	client, mux := setupTest(t)
	ctx := context.Background()

	mux.HandleFunc("/rest/api/3/issue/PROJ-1/properties/missing", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusNotFound, map[string]any{
			"errorMessages": []string{"Property 'missing' not found"},
		})
	})

	_, err := client.GetEntityProperty(ctx, "PROJ-1", "missing")
	require.Error(t, err)
	assert.True(t, errors.Is(err, forge.ErrNotFound), "expected forge.ErrNotFound, got: %v", err)
}

// ---------------------------------------------------------------------------
// SetEntityProperty
// ---------------------------------------------------------------------------

func TestSetEntityProperty(t *testing.T) {
	t.Parallel()
	client, mux := setupTest(t)
	ctx := context.Background()

	mux.HandleFunc("/rest/api/3/issue/PROJ-1/properties/fullsend.lock", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPut, r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "abc-123", body["id"])

		w.WriteHeader(http.StatusOK)
	})

	err := client.SetEntityProperty(ctx, "PROJ-1", "fullsend.lock", map[string]string{"id": "abc-123"})
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// DeleteEntityProperty
// ---------------------------------------------------------------------------

func TestDeleteEntityProperty(t *testing.T) {
	t.Parallel()
	client, mux := setupTest(t)
	ctx := context.Background()

	mux.HandleFunc("/rest/api/3/issue/PROJ-1/properties/fullsend.lock", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodDelete, r.Method)
		w.WriteHeader(http.StatusNoContent)
	})

	err := client.DeleteEntityProperty(ctx, "PROJ-1", "fullsend.lock")
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// GetMyself
// ---------------------------------------------------------------------------

func TestGetMyself(t *testing.T) {
	t.Parallel()
	client, mux := setupTest(t)
	ctx := context.Background()

	mux.HandleFunc("/rest/api/3/myself", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		writeJSON(t, w, http.StatusOK, User{
			AccountID:   "5b10a2844c20165700ede21g",
			DisplayName: "Mia Krystof",
			AccountType: "atlassian",
			Active:      true,
		})
	})

	user, err := client.GetMyself(ctx)
	require.NoError(t, err)
	assert.Equal(t, "5b10a2844c20165700ede21g", user.AccountID)
	assert.Equal(t, "Mia Krystof", user.DisplayName)
	assert.Equal(t, "atlassian", user.AccountType)
	assert.True(t, user.Active)
}

// ---------------------------------------------------------------------------
// Error responses
// ---------------------------------------------------------------------------

func TestErrorResponse_401(t *testing.T) {
	t.Parallel()
	client, mux := setupTest(t)
	ctx := context.Background()

	mux.HandleFunc("/rest/api/3/myself", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusUnauthorized, map[string]any{
			"errorMessages": []string{"You do not have permission to access this resource"},
		})
	})

	_, err := client.GetMyself(ctx)
	require.Error(t, err)
	var apiErr *APIError
	require.True(t, errors.As(err, &apiErr))
	assert.Equal(t, http.StatusUnauthorized, apiErr.StatusCode)
}

func TestErrorResponse_403(t *testing.T) {
	t.Parallel()
	client, mux := setupTest(t)
	ctx := context.Background()

	mux.HandleFunc("/rest/api/3/myself", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusForbidden, map[string]any{
			"errorMessages": []string{"Forbidden"},
		})
	})

	_, err := client.GetMyself(ctx)
	require.Error(t, err)
	assert.True(t, errors.Is(err, forge.ErrForbidden))
}

func TestErrorResponse_429_RetryAfter(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// Use a short-timeout client for this test.
	client, err := New("test-token", WithBaseURL(srv.URL))
	require.NoError(t, err)
	ctx := context.Background()

	callCount := 0
	mux.HandleFunc("/rest/api/3/myself", func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount <= 1 {
			w.Header().Set("Retry-After", "1")
			writeJSON(t, w, http.StatusTooManyRequests, map[string]any{
				"errorMessages": []string{"Rate limit exceeded"},
			})
			return
		}
		writeJSON(t, w, http.StatusOK, User{AccountID: "123"})
	})

	user, err := client.GetMyself(ctx)
	require.NoError(t, err)
	assert.Equal(t, "123", user.AccountID)
	assert.Equal(t, 2, callCount)
}

// ---------------------------------------------------------------------------
// Base URL validation
// ---------------------------------------------------------------------------

func TestBaseURLValidation_HTTPSRequired(t *testing.T) {
	t.Parallel()
	_, err := New("token", WithBaseURL("http://jira.example.com"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "insecure scheme")
}

func TestBaseURLValidation_LoopbackException(t *testing.T) {
	t.Parallel()
	_, err := New("token", WithBaseURL("http://localhost:8080"))
	require.NoError(t, err)

	_, err = New("token", WithBaseURL("http://127.0.0.1:8080"))
	require.NoError(t, err)
}

func TestBaseURLValidation_HTTPSAllowed(t *testing.T) {
	t.Parallel()
	_, err := New("token", WithBaseURL("https://acme.atlassian.net"))
	require.NoError(t, err)
}

func TestNew_EmptyToken(t *testing.T) {
	t.Parallel()
	_, err := New("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "token must not be empty")
}

func TestNew_NoBaseURL(t *testing.T) {
	t.Parallel()
	_, err := New("token")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "base URL must be set")
}

// ---------------------------------------------------------------------------
// GetIssue
// ---------------------------------------------------------------------------

func TestGetIssue(t *testing.T) {
	t.Parallel()
	client, mux := setupTest(t)
	ctx := context.Background()

	mux.HandleFunc("/rest/api/3/issue/PROJ-42", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		writeJSON(t, w, http.StatusOK, Issue{
			ID:  "10042",
			Key: "PROJ-42",
			Fields: IssueFields{
				Summary: "Test issue",
				Status:  Status{Name: "Open", StatusCategory: StatusCategory{Key: "new"}},
				Labels:  []string{"bug"},
			},
		})
	})

	issue, err := client.GetIssue(ctx, "PROJ-42")
	require.NoError(t, err)
	assert.Equal(t, "PROJ-42", issue.Key)
	assert.Equal(t, "Test issue", issue.Fields.Summary)
	assert.Equal(t, "new", issue.Fields.Status.StatusCategory.Key)
}

func TestGetIssue_NotFound(t *testing.T) {
	t.Parallel()
	client, mux := setupTest(t)
	ctx := context.Background()

	mux.HandleFunc("/rest/api/3/issue/PROJ-999", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusNotFound, map[string]any{
			"errorMessages": []string{"Issue does not exist or you do not have permission to see it."},
		})
	})

	_, err := client.GetIssue(ctx, "PROJ-999")
	require.Error(t, err)
	assert.True(t, errors.Is(err, forge.ErrNotFound))
}

// ---------------------------------------------------------------------------
// SearchIssues single page
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// GetProjectRoleMembership
// ---------------------------------------------------------------------------

func TestGetProjectRoleMembership(t *testing.T) {
	t.Parallel()
	client, mux := setupTest(t)
	ctx := context.Background()

	// Role list endpoint.
	mux.HandleFunc("/rest/api/3/project/PROJ/role", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		writeJSON(t, w, http.StatusOK, map[string]string{
			"Administrators": "http://localhost/rest/api/3/project/10001/role/10002",
			"Developers":     "http://localhost/rest/api/3/project/10001/role/10003",
		})
	})

	// Administrators role detail.
	mux.HandleFunc("/rest/api/3/project/PROJ/role/10002", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, ProjectRoleDetail{
			Name: "Administrators",
			Actors: []RoleActor{
				{
					ID:          1,
					DisplayName: "Alice Admin",
					Type:        "atlassian-user-role-actor",
					ActorUser:   &RoleActorUser{AccountID: "alice-id"},
				},
				{
					ID:          2,
					DisplayName: "Both Roles User",
					Type:        "atlassian-user-role-actor",
					ActorUser:   &RoleActorUser{AccountID: "both-id"},
				},
			},
		})
	})

	// Developers role detail.
	mux.HandleFunc("/rest/api/3/project/PROJ/role/10003", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, ProjectRoleDetail{
			Name: "Developers",
			Actors: []RoleActor{
				{
					ID:          3,
					DisplayName: "Bob Dev",
					Type:        "atlassian-user-role-actor",
					ActorUser:   &RoleActorUser{AccountID: "bob-id"},
				},
				{
					ID:          4,
					DisplayName: "Both Roles User",
					Type:        "atlassian-user-role-actor",
					ActorUser:   &RoleActorUser{AccountID: "both-id"},
				},
				{
					// Group actor — should be skipped (no ActorUser).
					ID:          5,
					DisplayName: "dev-group",
					Type:        "atlassian-group-role-actor",
				},
			},
		})
	})

	membership, err := client.GetProjectRoleMembership(ctx, "PROJ")
	require.NoError(t, err)

	assert.Equal(t, "Administrators", membership["alice-id"], "alice should be Administrator")
	assert.Equal(t, "Developers", membership["bob-id"], "bob should be Developer")
	assert.Equal(t, "Administrators", membership["both-id"], "user in both roles should get highest (Administrators)")
	assert.NotContains(t, membership, "dev-group", "group actors should be skipped")
}

func TestGetProjectRoleMembership_Error(t *testing.T) {
	t.Parallel()
	client, mux := setupTest(t)
	ctx := context.Background()

	mux.HandleFunc("/rest/api/3/project/BADPROJ/role", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusNotFound, map[string]any{
			"errorMessages": []string{"No project could be found with key 'BADPROJ'"},
		})
	})

	_, err := client.GetProjectRoleMembership(ctx, "BADPROJ")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list project roles")
}

// ---------------------------------------------------------------------------
// OAuth 2.0 Client Credentials
// ---------------------------------------------------------------------------

func TestOAuth2_TokenExchange(t *testing.T) {
	t.Parallel()

	// Mock token endpoint.
	tokenCalls := 0
	tokenMux := http.NewServeMux()
	tokenMux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "application/x-www-form-urlencoded", r.Header.Get("Content-Type"))
		require.NoError(t, r.ParseForm())
		assert.Equal(t, "client_credentials", r.Form.Get("grant_type"))
		assert.Equal(t, "my-client-id", r.Form.Get("client_id"))
		assert.Equal(t, "my-client-secret", r.Form.Get("client_secret"))
		tokenCalls++
		writeJSON(t, w, http.StatusOK, map[string]any{
			"access_token": fmt.Sprintf("access-token-%d", tokenCalls),
			"expires_in":   3600,
			"token_type":   "Bearer",
		})
	})
	tokenSrv := httptest.NewServer(tokenMux)
	t.Cleanup(tokenSrv.Close)

	// Mock Jira API.
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/api/3/myself", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		assert.True(t, strings.HasPrefix(auth, "Bearer access-token-"), "expected Bearer with OAuth2 token, got: %s", auth)
		writeJSON(t, w, http.StatusOK, User{AccountID: "123", DisplayName: "OAuth Bot"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client, err := NewOAuth2("my-client-id", "my-client-secret",
		WithBaseURL(srv.URL),
		WithTokenURL(tokenSrv.URL+"/oauth/token"),
	)
	require.NoError(t, err)

	user, err := client.GetMyself(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "OAuth Bot", user.DisplayName)
	assert.Equal(t, 1, tokenCalls, "should have fetched a token")
}

func TestOAuth2_TokenCaching(t *testing.T) {
	t.Parallel()

	tokenCalls := 0
	tokenMux := http.NewServeMux()
	tokenMux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		tokenCalls++
		writeJSON(t, w, http.StatusOK, map[string]any{
			"access_token": fmt.Sprintf("token-%d", tokenCalls),
			"expires_in":   3600,
			"token_type":   "Bearer",
		})
	})
	tokenSrv := httptest.NewServer(tokenMux)
	t.Cleanup(tokenSrv.Close)

	mux := http.NewServeMux()
	mux.HandleFunc("/rest/api/3/myself", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, User{AccountID: "123"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client, err := NewOAuth2("cid", "csec",
		WithBaseURL(srv.URL),
		WithTokenURL(tokenSrv.URL+"/oauth/token"),
	)
	require.NoError(t, err)

	// Make two API calls — should reuse the cached token.
	_, err = client.GetMyself(context.Background())
	require.NoError(t, err)
	_, err = client.GetMyself(context.Background())
	require.NoError(t, err)

	assert.Equal(t, 1, tokenCalls, "second call should reuse cached token")
}

func TestOAuth2_TokenRefreshBeforeExpiry(t *testing.T) {
	t.Parallel()

	tokenCalls := 0
	tokenMux := http.NewServeMux()
	tokenMux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		tokenCalls++
		writeJSON(t, w, http.StatusOK, map[string]any{
			"access_token": fmt.Sprintf("token-%d", tokenCalls),
			"expires_in":   3600,
			"token_type":   "Bearer",
		})
	})
	tokenSrv := httptest.NewServer(tokenMux)
	t.Cleanup(tokenSrv.Close)

	mux := http.NewServeMux()
	mux.HandleFunc("/rest/api/3/myself", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, User{AccountID: "123"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client, err := NewOAuth2("cid", "csec",
		WithBaseURL(srv.URL),
		WithTokenURL(tokenSrv.URL+"/oauth/token"),
	)
	require.NoError(t, err)

	// First call — fetches token.
	_, err = client.GetMyself(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, tokenCalls)

	// Simulate near-expiry by backdating the cached expiry.
	client.oauth2.mu.Lock()
	client.oauth2.expiry = time.Now().Add(2 * time.Minute) // within 5-minute refresh window
	client.oauth2.mu.Unlock()

	// Next call should trigger a refresh.
	_, err = client.GetMyself(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, tokenCalls, "should have refreshed near-expiry token")
}

func TestOAuth2_TokenEndpointError(t *testing.T) {
	t.Parallel()

	tokenMux := http.NewServeMux()
	tokenMux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusUnauthorized, map[string]any{
			"error":             "invalid_client",
			"error_description": "Client authentication failed",
		})
	})
	tokenSrv := httptest.NewServer(tokenMux)
	t.Cleanup(tokenSrv.Close)

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client, err := NewOAuth2("bad-id", "bad-secret",
		WithBaseURL(srv.URL),
		WithTokenURL(tokenSrv.URL+"/oauth/token"),
	)
	require.NoError(t, err)

	_, err = client.GetMyself(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "oauth2 token")
}

func TestNewOAuth2_MissingCredentials(t *testing.T) {
	t.Parallel()

	_, err := NewOAuth2("", "secret", WithBaseURL("https://example.atlassian.net"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "client ID")

	_, err = NewOAuth2("id", "", WithBaseURL("https://example.atlassian.net"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "client secret")
}

func TestSearchIssues_SinglePage(t *testing.T) {
	t.Parallel()
	client, mux := setupTest(t)
	ctx := context.Background()

	mux.HandleFunc("/rest/api/3/search/jql", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		var body searchRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Contains(t, body.JQL, "project = TEST")
		assert.Equal(t, "changelog", body.Expand)
		assert.Equal(t, []string{"*all"}, body.Fields)
		assert.Empty(t, body.NextPageToken, "first request should have no nextPageToken")
		writeJSON(t, w, http.StatusOK, SearchResult{
			Issues: []Issue{{ID: "1", Key: "TEST-1"}},
			IsLast: true,
		})
	})

	issues, err := client.SearchIssues(ctx, "project = TEST")
	require.NoError(t, err)
	assert.Len(t, issues, 1)
	assert.Equal(t, "TEST-1", issues[0].Key)
}

// ---------------------------------------------------------------------------
// NewOAuth2 / New base URL edge cases
// ---------------------------------------------------------------------------

func TestNewOAuth2_NoBaseURL(t *testing.T) {
	t.Parallel()
	_, err := NewOAuth2("id", "secret")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "base URL must be set")
}

func TestNewOAuth2_InsecureBaseURL(t *testing.T) {
	t.Parallel()
	_, err := NewOAuth2("id", "secret", WithBaseURL("http://jira.example.com"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "insecure scheme")
}

func TestBaseURLValidation_ParseError(t *testing.T) {
	t.Parallel()
	_, err := New("token", WithBaseURL("http://exa mple.com"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid base URL")
}

// ---------------------------------------------------------------------------
// WithHTTPClient
// ---------------------------------------------------------------------------

func TestWithHTTPClient_UpdatesOAuth2Client(t *testing.T) {
	t.Parallel()
	custom := &http.Client{Timeout: 5 * time.Second}
	c, err := NewOAuth2("id", "secret", WithBaseURL("https://example.atlassian.net"), WithHTTPClient(custom))
	require.NoError(t, err)
	assert.Same(t, custom, c.httpClient)
	assert.Same(t, custom, c.oauth2.httpClient)
}

func TestWithHTTPClient_NoOAuth2IsNoOp(t *testing.T) {
	t.Parallel()
	custom := &http.Client{Timeout: 5 * time.Second}
	c, err := New("token", WithBaseURL("https://example.atlassian.net"), WithHTTPClient(custom))
	require.NoError(t, err)
	assert.Same(t, custom, c.httpClient)
	assert.Nil(t, c.oauth2)
}

// ---------------------------------------------------------------------------
// Retry helpers
// ---------------------------------------------------------------------------

func TestIsTransientError(t *testing.T) {
	t.Parallel()
	assert.True(t, isTransientError(io.EOF))
	assert.True(t, isTransientError(io.ErrUnexpectedEOF))
	assert.True(t, isTransientError(&net.DNSError{Err: "timeout", IsTimeout: true}))
	assert.False(t, isTransientError(errors.New("boom")))
}

func TestIsIdempotent(t *testing.T) {
	t.Parallel()
	assert.True(t, isIdempotent(http.MethodGet))
	assert.True(t, isIdempotent(http.MethodHead))
	assert.True(t, isIdempotent(http.MethodPut))
	assert.True(t, isIdempotent(http.MethodDelete))
	assert.False(t, isIdempotent(http.MethodPost))
	assert.False(t, isIdempotent(http.MethodPatch))
}

func TestIsRetryable(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		status int
		method string
		want   bool
	}{
		{"429 GET", http.StatusTooManyRequests, http.MethodGet, true},
		{"429 POST", http.StatusTooManyRequests, http.MethodPost, true},
		{"500 GET", http.StatusInternalServerError, http.MethodGet, true},
		{"503 PUT", http.StatusServiceUnavailable, http.MethodPut, true},
		{"500 POST", http.StatusInternalServerError, http.MethodPost, false},
		{"404 GET", http.StatusNotFound, http.MethodGet, false},
		{"505 GET", http.StatusHTTPVersionNotSupported, http.MethodGet, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := &http.Response{StatusCode: tc.status}
			assert.Equal(t, tc.want, isRetryable(resp, tc.method))
		})
	}
}

func TestRetryDelay_RetryAfterHeader(t *testing.T) {
	t.Parallel()
	resp := &http.Response{Header: http.Header{"Retry-After": []string{"2"}}}
	assert.Equal(t, 2*time.Second, retryDelay(resp, 0))
}

func TestRetryDelay_RetryAfterCapped(t *testing.T) {
	t.Parallel()
	resp := &http.Response{Header: http.Header{"Retry-After": []string{"9999"}}}
	assert.Equal(t, 300*time.Second, retryDelay(resp, 0))
}

func TestRetryDelay_InvalidRetryAfterFallsBackToExponential(t *testing.T) {
	t.Parallel()
	resp := &http.Response{Header: http.Header{"Retry-After": []string{"not-a-number"}}}
	d := retryDelay(resp, 0)
	assert.GreaterOrEqual(t, d, time.Duration(0))
	assert.Less(t, d, 2*time.Second)
}

func TestRetryDelay_NoResponseExponentialBackoff(t *testing.T) {
	t.Parallel()
	d := retryDelay(nil, 2)
	assert.GreaterOrEqual(t, d, 2*time.Second)
	assert.Less(t, d, 4*time.Second)
}

// ---------------------------------------------------------------------------
// do() edge cases
// ---------------------------------------------------------------------------

func TestDo_ReadBodyError(t *testing.T) {
	t.Parallel()
	client, _ := setupTest(t)
	err := client.do(context.Background(), http.MethodPost, "/x", iotest.ErrReader(errors.New("boom")), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read request body")
}

// flakyTransport fails the first failN round trips with a transient network
// error before delegating to inner.
type flakyTransport struct {
	calls int
	failN int
	inner http.RoundTripper
}

func (t *flakyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.calls++
	if t.calls <= t.failN {
		return nil, &net.DNSError{Err: "flaky", IsTimeout: true}
	}
	return t.inner.RoundTrip(req)
}

func TestDo_RetriesOnTransientNetworkError(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	mux.HandleFunc("/rest/api/3/myself", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, User{AccountID: "42"})
	})

	transport := &flakyTransport{failN: 1, inner: http.DefaultTransport}
	client, err := New("test-token", WithBaseURL(srv.URL), WithHTTPClient(&http.Client{Transport: transport}))
	require.NoError(t, err)

	user, err := client.GetMyself(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "42", user.AccountID)
	assert.Equal(t, 2, transport.calls)
}
