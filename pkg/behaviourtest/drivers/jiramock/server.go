// Package jiramock provides a stateful mock Jira REST API server for
// behaviour tests. The mock implements the endpoints that jira.Client calls,
// backed by in-memory state that step definitions manipulate between requests.
package jiramock

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fullsend-ai/fullsend/internal/forge/jira"
)

// State holds the mutable mock data. All methods are safe for concurrent use.
type State struct {
	mu         sync.Mutex
	issues     map[string]*jira.Issue                // key → issue
	comments   map[string][]jira.Comment             // issueKey → comments
	changelog  map[string][]jira.ChangelogEntry      // issueKey → changelog
	properties map[string]map[string]json.RawMessage // issueKey → propKey → value
	nextID     int
}

// AddIssue inserts an issue with the given key and labels. A synthetic
// numeric ID and timestamp are assigned automatically.
func (s *State) AddIssue(key string, labels []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextID++
	id := strconv.Itoa(s.nextID + 10000)

	s.issues[key] = &jira.Issue{
		ID:  id,
		Key: key,
		Fields: jira.IssueFields{
			Summary: "Test issue " + key,
			Status: jira.Status{
				Name:           "Open",
				StatusCategory: jira.StatusCategory{Key: "new"},
			},
			Labels: labels,
			Reporter: jira.User{
				AccountID:   "reporter-001",
				DisplayName: "Test Reporter",
				AccountType: "atlassian",
				Active:      true,
			},
			Created: time.Now().Add(-1 * time.Hour).Format("2006-01-02T15:04:05.000-0700"),
			Updated: time.Now().Format("2006-01-02T15:04:05.000-0700"),
		},
	}
}

// AddComment appends a comment to the given issue. The comment is authored
// by a canned human user (not a bot).
func (s *State) AddComment(issueKey, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := strconv.Itoa(len(s.comments[issueKey]) + 1)
	now := time.Now().Format("2006-01-02T15:04:05.000-0700")

	s.comments[issueKey] = append(s.comments[issueKey], jira.Comment{
		ID:   id,
		Body: body,
		Author: jira.User{
			AccountID:   "commenter-001",
			DisplayName: "Test Commenter",
			AccountType: "atlassian",
			Active:      true,
		},
		Created: now,
		Updated: now,
	})
}

// AddLabelChange appends a changelog entry representing a label addition
// and updates the issue's label list.
func (s *State) AddLabelChange(issueKey, label string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	issue, ok := s.issues[issueKey]
	if !ok {
		return
	}

	// Build from/to strings from current labels.
	fromLabels := strings.Join(issue.Fields.Labels, " ")
	issue.Fields.Labels = append(issue.Fields.Labels, label)
	toLabels := strings.Join(issue.Fields.Labels, " ")

	id := strconv.Itoa(len(s.changelog[issueKey]) + 1)
	now := time.Now().Format("2006-01-02T15:04:05.000-0700")

	s.changelog[issueKey] = append(s.changelog[issueKey], jira.ChangelogEntry{
		ID: id,
		Author: jira.User{
			AccountID:   "changer-001",
			DisplayName: "Test Changer",
			AccountType: "atlassian",
			Active:      true,
		},
		Created: now,
		Items: []jira.ChangeItem{
			{
				Field:      "labels",
				FromString: fromLabels,
				ToString:   toLabels,
			},
		},
	})
}

// NewServer creates a mock Jira REST API server and returns the httptest
// server and its backing state. The caller should defer srv.Close().
func NewServer() (*httptest.Server, *State) {
	state := &State{
		issues:     make(map[string]*jira.Issue),
		comments:   make(map[string][]jira.Comment),
		changelog:  make(map[string][]jira.ChangelogEntry),
		properties: make(map[string]map[string]json.RawMessage),
	}

	mux := http.NewServeMux()
	registerHandlers(mux, state)
	srv := httptest.NewServer(mux)
	return srv, state
}

func registerHandlers(mux *http.ServeMux, s *State) {
	mux.HandleFunc("/rest/api/3/search/jql", func(w http.ResponseWriter, r *http.Request) {
		handleSearch(w, r, s)
	})
	mux.HandleFunc("/rest/api/3/myself", func(w http.ResponseWriter, r *http.Request) {
		handleMyself(w, r)
	})
	// Catch-all for /rest/api/3/ routes using path-based dispatch.
	mux.HandleFunc("/rest/api/3/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/rest/api/3/")
		handleAPIRoute(w, r, s, path)
	})
}

func handleSearch(w http.ResponseWriter, r *http.Request, s *State) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.Lock()
	issues := make([]jira.Issue, 0, len(s.issues))
	for _, issue := range s.issues {
		issues = append(issues, *issue)
	}
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, jira.SearchResult{
		Issues: issues,
		IsLast: true,
	})
}

func handleMyself(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, jira.User{
		AccountID:   "bot-001",
		DisplayName: "Fullsend Bot",
		AccountType: "app",
		Active:      true,
	})
}

func handleAPIRoute(w http.ResponseWriter, r *http.Request, s *State, path string) {
	// /issue/{key}/comment
	// /issue/{key}/changelog
	// /issue/{key}/properties/{propKey}
	// /project/{key}/role
	// /project/{key}/role/{id}

	parts := strings.Split(path, "/")

	if len(parts) >= 2 && parts[0] == "project" {
		handleProjectRoute(w, r, s, parts)
		return
	}

	if len(parts) >= 2 && parts[0] == "issue" {
		handleIssueRoute(w, r, s, parts)
		return
	}

	http.NotFound(w, r)
}

func handleProjectRoute(w http.ResponseWriter, _ *http.Request, _ *State, parts []string) {
	// GET /project/{key}/role → role list
	// GET /project/{key}/role/{id} → role detail
	if len(parts) >= 3 && parts[2] == "role" {
		if len(parts) == 3 {
			// Role list — return a canned set with one role.
			writeJSON(w, http.StatusOK, jira.ProjectRoleList{
				"Developers": fmt.Sprintf("http://localhost/rest/api/3/project/%s/role/10001", parts[1]),
			})
			return
		}
		if len(parts) == 4 {
			// Role detail — return the commenter and changer as developers.
			writeJSON(w, http.StatusOK, jira.ProjectRoleDetail{
				Name: "Developers",
				Actors: []jira.RoleActor{
					{
						ID:          1,
						DisplayName: "Test Commenter",
						Type:        "atlassian-user-role-actor",
						ActorUser:   &jira.RoleActorUser{AccountID: "commenter-001"},
					},
					{
						ID:          2,
						DisplayName: "Test Changer",
						Type:        "atlassian-user-role-actor",
						ActorUser:   &jira.RoleActorUser{AccountID: "changer-001"},
					},
				},
			})
			return
		}
	}
	http.NotFound(w, nil)
}

func handleIssueRoute(w http.ResponseWriter, r *http.Request, s *State, parts []string) {
	issueKey := parts[1]

	if len(parts) == 2 {
		handleGetIssue(w, s, issueKey)
		return
	}

	switch parts[2] {
	case "comment":
		handleComments(w, s, issueKey)
	case "changelog":
		handleChangelog(w, s, issueKey)
	case "properties":
		if len(parts) < 4 {
			http.NotFound(w, r)
			return
		}
		propKey := strings.Join(parts[3:], "/")
		handleProperty(w, r, s, issueKey, propKey)
	default:
		http.NotFound(w, r)
	}
}

func handleGetIssue(w http.ResponseWriter, s *State, key string) {
	s.mu.Lock()
	issue, ok := s.issues[key]
	s.mu.Unlock()

	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"errorMessages": []string{"Issue does not exist"},
		})
		return
	}
	writeJSON(w, http.StatusOK, issue)
}

func handleComments(w http.ResponseWriter, s *State, issueKey string) {
	s.mu.Lock()
	comments := s.comments[issueKey]
	s.mu.Unlock()

	if comments == nil {
		comments = []jira.Comment{}
	}

	writeJSON(w, http.StatusOK, jira.CommentPage{
		Comments:   comments,
		Total:      len(comments),
		MaxResults: 100,
		StartAt:    0,
	})
}

func handleChangelog(w http.ResponseWriter, s *State, issueKey string) {
	s.mu.Lock()
	entries := s.changelog[issueKey]
	s.mu.Unlock()

	if entries == nil {
		entries = []jira.ChangelogEntry{}
	}

	writeJSON(w, http.StatusOK, struct {
		Values     []jira.ChangelogEntry `json:"values"`
		Total      int                   `json:"total"`
		MaxResults int                   `json:"maxResults"`
		StartAt    int                   `json:"startAt"`
		IsLast     bool                  `json:"isLast"`
	}{
		Values:     entries,
		Total:      len(entries),
		MaxResults: 100,
		StartAt:    0,
		IsLast:     true,
	})
}

func handleProperty(w http.ResponseWriter, r *http.Request, s *State, issueKey, propKey string) {
	switch r.Method {
	case http.MethodGet:
		s.mu.Lock()
		props := s.properties[issueKey]
		var val json.RawMessage
		if props != nil {
			val = props[propKey]
		}
		s.mu.Unlock()

		if val == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{
				"errorMessages": []string{fmt.Sprintf("Property '%s' not found", propKey)},
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"key":   propKey,
			"value": json.RawMessage(val),
		})

	case http.MethodPut:
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}

		s.mu.Lock()
		if s.properties[issueKey] == nil {
			s.properties[issueKey] = make(map[string]json.RawMessage)
		}
		s.properties[issueKey][propKey] = json.RawMessage(body)
		s.mu.Unlock()

		w.WriteHeader(http.StatusOK)

	case http.MethodDelete:
		s.mu.Lock()
		if props := s.properties[issueKey]; props != nil {
			delete(props, propKey)
		}
		s.mu.Unlock()

		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
