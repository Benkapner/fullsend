package jirapoll

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fullsend-ai/fullsend/internal/dispatch"
	"github.com/fullsend-ai/fullsend/internal/forge"
	"github.com/fullsend-ai/fullsend/internal/forge/jira"
	"github.com/fullsend-ai/fullsend/internal/poll"
)

// newTestPoller creates a Poller with a no-op sleep for fast tests.
func newTestPoller(client JiraClient, router dispatch.EventRouter, opts Options) *Poller {
	p := New(client, router, opts)
	p.sleepFn = func(_ time.Duration) {} // skip jitter in tests
	return p
}

// mockClient implements JiraClient with configurable return values.
type mockClient struct {
	mu sync.Mutex

	searchResult []jira.Issue
	searchErr    error
	lastQuery    string
	lastLimit    int

	issues   map[string]*jira.Issue
	issueErr map[string]error

	comments   map[string][]jira.Comment
	commentErr map[string]error

	changelog    map[string][]jira.ChangelogEntry
	changelogErr map[string]error

	properties     map[string]map[string]json.RawMessage // issueKey -> propertyKey -> value
	propertyGetErr map[string]error                      // propertyKey -> error
	propertySetErr map[string]error                      // propertyKey -> error

	myselfUser *jira.User
	myselfErr  error

	roleMembership map[string]string // accountID -> role name

	// getPropertyHook, if set, runs after each GetEntityProperty call
	// captures its return value, and before that value is returned. Used
	// to simulate a concurrent writer changing the stored property
	// between reads.
	getPropertyHook func(issueKey, propKey string)
}

func newMockClient() *mockClient {
	return &mockClient{
		issues:         make(map[string]*jira.Issue),
		issueErr:       make(map[string]error),
		comments:       make(map[string][]jira.Comment),
		commentErr:     make(map[string]error),
		changelog:      make(map[string][]jira.ChangelogEntry),
		changelogErr:   make(map[string]error),
		properties:     make(map[string]map[string]json.RawMessage),
		propertyGetErr: make(map[string]error),
		propertySetErr: make(map[string]error),
	}
}

func (m *mockClient) SearchIssues(_ context.Context, jql string, limit int) ([]jira.Issue, error) {
	m.lastQuery = jql
	m.lastLimit = limit
	if m.searchErr != nil {
		return nil, m.searchErr
	}
	if limit > 0 && len(m.searchResult) > limit {
		return m.searchResult[:limit], nil
	}
	return m.searchResult, nil
}

func (m *mockClient) GetIssue(_ context.Context, key string) (*jira.Issue, error) {
	if err, ok := m.issueErr[key]; ok && err != nil {
		return nil, err
	}
	issue, ok := m.issues[key]
	if !ok {
		return nil, nil
	}
	return issue, nil
}

func (m *mockClient) ListComments(_ context.Context, key string) ([]jira.Comment, error) {
	if err, ok := m.commentErr[key]; ok && err != nil {
		return nil, err
	}
	return m.comments[key], nil
}

func (m *mockClient) ListChangelog(_ context.Context, key string) ([]jira.ChangelogEntry, error) {
	if err, ok := m.changelogErr[key]; ok && err != nil {
		return nil, err
	}
	return m.changelog[key], nil
}

func (m *mockClient) GetEntityProperty(_ context.Context, issueKey, propKey string) (json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err, ok := m.propertyGetErr[propKey]; ok && err != nil {
		return nil, err
	}
	props, ok := m.properties[issueKey]
	if !ok {
		// Match real Jira behavior: 404 when property doesn't exist.
		return nil, fmt.Errorf("property %s not found on %s: %w", propKey, issueKey, forge.ErrNotFound)
	}
	val, ok := props[propKey]
	if !ok {
		return nil, fmt.Errorf("property %s not found on %s: %w", propKey, issueKey, forge.ErrNotFound)
	}
	if m.getPropertyHook != nil {
		m.getPropertyHook(issueKey, propKey)
	}
	return val, nil
}

func (m *mockClient) SetEntityProperty(_ context.Context, issueKey, propKey string, value any) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err, ok := m.propertySetErr[propKey]; ok && err != nil {
		return err
	}
	if m.properties[issueKey] == nil {
		m.properties[issueKey] = make(map[string]json.RawMessage)
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	m.properties[issueKey][propKey] = data
	return nil
}

func (m *mockClient) DeleteEntityProperty(_ context.Context, issueKey, propKey string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if props, ok := m.properties[issueKey]; ok {
		delete(props, propKey)
	}
	return nil
}

func (m *mockClient) GetMyself(_ context.Context) (*jira.User, error) {
	if m.myselfErr != nil {
		return nil, m.myselfErr
	}
	return m.myselfUser, nil
}

func (m *mockClient) GetProjectRoleMembership(_ context.Context, _ string) (map[string]string, error) {
	if m.roleMembership != nil {
		return m.roleMembership, nil
	}
	return map[string]string{}, nil
}

// stubRouter implements dispatch.EventRouter for testing.
type stubRouter struct {
	stages []string
	err    error
}

func (r *stubRouter) Route(_ *dispatch.NormalizedEvent) ([]string, error) {
	return r.stages, r.err
}

func TestNew(t *testing.T) {
	mc := newMockClient()
	p := New(mc, nil, Options{
		TargetRepo:  "acme/platform",
		JiraBaseURL: "https://acme.atlassian.net",
		JiraProject: "PROJ",
	})
	if p.opts.M != 50 {
		t.Errorf("M = %d, want default 50", p.opts.M)
	}
	if p.opts.N != 5 {
		t.Errorf("N = %d, want default 5", p.opts.N)
	}
	if p.opts.StaleThreshold != 900*time.Second {
		t.Errorf("StaleThreshold = %v, want default 900s", p.opts.StaleThreshold)
	}
}

func TestRunEmptyPoll(t *testing.T) {
	mc := newMockClient()

	dir := t.TempDir()
	outputPath := filepath.Join(dir, "dispatches.json")

	p := newTestPoller(mc, nil, Options{
		TargetRepo:  "acme/platform",
		JiraBaseURL: "https://acme.atlassian.net",
		JiraProject: "PROJ",
		OutputPath:  outputPath,
	})

	err := p.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(data) != "[]\n" {
		t.Errorf("output = %q, want empty JSON array", string(data))
	}
}

func TestRunHappyPath_CommentWithSlashCommand(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	mc := newMockClient()
	mc.roleMembership = map[string]string{
		"557058:abc123def456": "Developers",
	}
	mc.searchResult = []jira.Issue{
		{
			ID:   "10042",
			Key:  "PROJ-123",
			Self: "https://acme.atlassian.net/rest/api/3/issue/10042",
			Fields: jira.IssueFields{
				Summary: "Test issue",
				Labels:  []string{"needs-info", "bug"},
				Status: jira.Status{
					Name:           "Open",
					StatusCategory: jira.StatusCategory{Key: "new"},
				},
				Reporter: jira.User{
					AccountID:   "reporter-id",
					AccountType: "atlassian",
				},
				Created: now.Add(-1 * time.Hour).Format("2006-01-02T15:04:05.000-0700"),
				Updated: now.Format("2006-01-02T15:04:05.000-0700"),
			},
		},
	}
	mc.comments["PROJ-123"] = []jira.Comment{
		{
			ID:      "50001",
			Body:    "/fs-triage check acceptance criteria",
			Created: now.Format("2006-01-02T15:04:05.000-0700"),
			Author: jira.User{
				AccountID:   "557058:abc123def456",
				AccountType: "atlassian",
			},
		},
	}

	dir := t.TempDir()
	outputPath := filepath.Join(dir, "dispatches.json")

	router := &stubRouter{stages: []string{"triage"}}
	p := newTestPoller(mc, router, Options{
		TargetRepo:  "acme/platform",
		JiraBaseURL: "https://acme.atlassian.net",
		JiraProject: "PROJ",
		OutputPath:  outputPath,
	})

	if err := p.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	var dispatches []poll.Dispatch
	if err := json.Unmarshal(data, &dispatches); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	found := false
	for _, d := range dispatches {
		if d.Stage == "triage" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected triage dispatch, got dispatches: %+v", dispatches)
	}
}

func TestRunLabelChange(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	mc := newMockClient()
	mc.roleMembership = map[string]string{
		"user1": "Developers",
	}
	mc.searchResult = []jira.Issue{
		{
			ID:  "10042",
			Key: "PROJ-123",
			Fields: jira.IssueFields{
				Labels: []string{"ready-to-code"},
				Status: jira.Status{
					Name:           "Open",
					StatusCategory: jira.StatusCategory{Key: "new"},
				},
				Reporter: jira.User{AccountID: "reporter-id", AccountType: "atlassian"},
				Created:  now.Add(-1 * time.Hour).Format("2006-01-02T15:04:05.000-0700"),
				Updated:  now.Format("2006-01-02T15:04:05.000-0700"),
			},
		},
	}
	mc.changelog["PROJ-123"] = []jira.ChangelogEntry{
		{
			ID:      "100",
			Created: now.Format("2006-01-02T15:04:05.000-0700"),
			Author: jira.User{
				AccountID:   "user1",
				AccountType: "atlassian",
			},
			Items: []jira.ChangeItem{
				{
					Field:      "labels",
					FromString: "",
					ToString:   "ready-to-code",
				},
			},
		},
	}

	dir := t.TempDir()
	outputPath := filepath.Join(dir, "dispatches.json")

	router := &stubRouter{stages: []string{"code"}}
	p := newTestPoller(mc, router, Options{
		TargetRepo:  "acme/platform",
		JiraBaseURL: "https://acme.atlassian.net",
		JiraProject: "PROJ",
		OutputPath:  outputPath,
	})

	if err := p.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	var dispatches []poll.Dispatch
	if err := json.Unmarshal(data, &dispatches); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	found := false
	for _, d := range dispatches {
		if d.Stage == "code" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected code dispatch from label change, got: %+v", dispatches)
	}
}

func TestRunBotFiltering(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	mc := newMockClient()
	mc.searchResult = []jira.Issue{
		{
			ID:  "10042",
			Key: "PROJ-123",
			Fields: jira.IssueFields{
				Labels: []string{"bug"},
				Status: jira.Status{
					Name:           "Open",
					StatusCategory: jira.StatusCategory{Key: "new"},
				},
				Reporter: jira.User{AccountID: "reporter-id", AccountType: "atlassian"},
				Created:  now.Add(-2 * time.Hour).Format("2006-01-02T15:04:05.000-0700"),
				Updated:  now.Format("2006-01-02T15:04:05.000-0700"),
			},
		},
	}
	setLastCheck(mc, "PROJ-123", "acme", "platform", now.Add(-30*time.Minute))

	mc.comments["PROJ-123"] = []jira.Comment{
		{
			ID:      "50001",
			Body:    "/fs-triage handle this",
			Created: now.Format("2006-01-02T15:04:05.000-0700"),
			Author: jira.User{
				AccountID:   "bot-account",
				AccountType: "app",
			},
		},
	}

	dir := t.TempDir()
	outputPath := filepath.Join(dir, "dispatches.json")

	router := &stubRouter{stages: []string{"triage"}}
	p := newTestPoller(mc, router, Options{
		TargetRepo:  "acme/platform",
		JiraBaseURL: "https://acme.atlassian.net",
		JiraProject: "PROJ",
		OutputPath:  outputPath,
	})

	if err := p.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	var dispatches []poll.Dispatch
	if err := json.Unmarshal(data, &dispatches); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(dispatches) != 0 {
		t.Errorf("expected 0 dispatches (bot filtered), got %d", len(dispatches))
	}
}

func TestRunLockContention(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	mc := newMockClient()
	mc.searchResult = []jira.Issue{
		{
			ID:  "10042",
			Key: "PROJ-123",
			Fields: jira.IssueFields{
				Labels:   []string{"bug"},
				Reporter: jira.User{AccountID: "reporter-id"},
				Created:  now.Add(-1 * time.Hour).Format("2006-01-02T15:04:05.000-0700"),
				Updated:  now.Format("2006-01-02T15:04:05.000-0700"),
			},
		},
	}

	lockPropKey := lockPropertyKey("acme", "platform")
	lockVal := LockValue{
		ID:    "other-poller-uuid",
		TS:    now.Format(time.RFC3339),
		Phase: "running",
	}
	lockData, _ := json.Marshal(lockVal)
	mc.properties["PROJ-123"] = map[string]json.RawMessage{
		lockPropKey: lockData,
	}

	dir := t.TempDir()
	outputPath := filepath.Join(dir, "dispatches.json")

	router := &stubRouter{stages: []string{"triage"}}
	p := newTestPoller(mc, router, Options{
		TargetRepo:  "acme/platform",
		JiraBaseURL: "https://acme.atlassian.net",
		JiraProject: "PROJ",
		OutputPath:  outputPath,
	})

	if err := p.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(data) != "[]\n" {
		t.Errorf("expected empty dispatches (locked), got %q", string(data))
	}
}

func TestRunStaleLock(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	mc := newMockClient()
	mc.roleMembership = map[string]string{
		"user1": "Developers",
	}
	mc.searchResult = []jira.Issue{
		{
			ID:  "10042",
			Key: "PROJ-123",
			Fields: jira.IssueFields{
				Labels:   []string{"bug"},
				Reporter: jira.User{AccountID: "reporter-id", AccountType: "atlassian"},
				Created:  now.Add(-1 * time.Hour).Format("2006-01-02T15:04:05.000-0700"),
				Updated:  now.Format("2006-01-02T15:04:05.000-0700"),
			},
		},
	}

	lockPropKey := lockPropertyKey("acme", "platform")
	lockVal := LockValue{
		ID:    "old-poller-uuid",
		TS:    now.Add(-2 * time.Hour).Format(time.RFC3339),
		Phase: "running",
	}
	lockData, _ := json.Marshal(lockVal)
	mc.properties["PROJ-123"] = map[string]json.RawMessage{
		lockPropKey: lockData,
	}

	mc.comments["PROJ-123"] = []jira.Comment{
		{
			ID:      "50001",
			Body:    "/fs-triage handle this",
			Created: now.Format("2006-01-02T15:04:05.000-0700"),
			Author: jira.User{
				AccountID:   "user1",
				AccountType: "atlassian",
			},
		},
	}

	dir := t.TempDir()
	outputPath := filepath.Join(dir, "dispatches.json")

	router := &stubRouter{stages: []string{"triage"}}
	p := newTestPoller(mc, router, Options{
		TargetRepo:     "acme/platform",
		JiraBaseURL:    "https://acme.atlassian.net",
		JiraProject:    "PROJ",
		OutputPath:     outputPath,
		StaleThreshold: 15 * time.Minute,
	})

	if err := p.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	var dispatches []poll.Dispatch
	if err := json.Unmarshal(data, &dispatches); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(dispatches) == 0 {
		t.Error("expected dispatches after stale lock cleanup")
	}
}

// TestFilterLockedStaleCleanupPreservesConcurrentFreshLock guards against a
// regression where stale-lock cleanup released the lock property
// unconditionally (expectedID == "") instead of using the stale lock's own
// ID, so a fresh lock written by a different poller in the race window
// between the read and the release got deleted without any ownership check.
func TestFilterLockedStaleCleanupPreservesConcurrentFreshLock(t *testing.T) {
	mc := newMockClient()
	p := newTestPoller(mc, nil, Options{
		TargetRepo:     "acme/platform",
		StaleThreshold: 15 * time.Minute,
	})

	propKey := lockPropertyKey("acme", "platform")
	staleLock := LockValue{
		ID:    "stale-poller-uuid",
		TS:    time.Now().Add(-1 * time.Hour).Format(time.RFC3339),
		Phase: "running",
	}
	staleData, err := json.Marshal(staleLock)
	if err != nil {
		t.Fatalf("marshal stale lock: %v", err)
	}
	mc.properties["PROJ-123"] = map[string]json.RawMessage{propKey: staleData}

	// Simulate a different poller acquiring a fresh lock on the same issue
	// in the window between this poller's stale-lock read and its release.
	freshLock := LockValue{
		ID:    "fresh-poller-uuid",
		TS:    time.Now().UTC().Format(time.RFC3339),
		Phase: "pending",
	}
	freshData, err := json.Marshal(freshLock)
	if err != nil {
		t.Fatalf("marshal fresh lock: %v", err)
	}
	mc.getPropertyHook = func(issueKey, key string) {
		if issueKey == "PROJ-123" && key == propKey {
			mc.properties["PROJ-123"][propKey] = freshData
		}
	}

	if _, err := p.filterLocked(context.Background(), []jira.Issue{{Key: "PROJ-123"}}); err != nil {
		t.Fatalf("filterLocked() error: %v", err)
	}

	raw, ok := mc.properties["PROJ-123"][propKey]
	if !ok {
		t.Fatal("expected fresh lock to survive stale-lock cleanup, but property was deleted")
	}
	var current LockValue
	if err := json.Unmarshal(raw, &current); err != nil {
		t.Fatalf("unmarshal current lock: %v", err)
	}
	if current.ID != freshLock.ID {
		t.Errorf("expected surviving lock ID %q, got %q", freshLock.ID, current.ID)
	}
}

// TestSearchCandidatesQuotesProjectKey checks that the default JQL quotes
// the project key rather than interpolating it bare, as defense in depth
// against JQL injection even though the key is already validated upstream.
func TestSearchCandidatesQuotesProjectKey(t *testing.T) {
	mc := newMockClient()
	p := newTestPoller(mc, nil, Options{JiraProject: "PROJ"})

	if _, err := p.searchCandidates(context.Background()); err != nil {
		t.Fatalf("searchCandidates() error: %v", err)
	}

	want := `project = "PROJ" AND status != Done ORDER BY updated DESC`
	if mc.lastQuery != want {
		t.Errorf("JQL = %q, want %q", mc.lastQuery, want)
	}
}

// TestSearchCandidatesBoundsBySettingM checks that searchCandidates asks
// SearchIssues to stop paginating once M results are collected, instead of
// exhausting the full JQL match set and truncating client-side.
func TestSearchCandidatesBoundsBySettingM(t *testing.T) {
	mc := newMockClient()
	mc.searchResult = make([]jira.Issue, 200)
	for i := range mc.searchResult {
		mc.searchResult[i] = jira.Issue{ID: fmt.Sprintf("%d", i+1), Key: fmt.Sprintf("PROJ-%d", i+1)}
	}
	p := newTestPoller(mc, nil, Options{JiraProject: "PROJ", M: 50})

	candidates, err := p.searchCandidates(context.Background())
	if err != nil {
		t.Fatalf("searchCandidates() error: %v", err)
	}
	if len(candidates) != 50 {
		t.Errorf("len(candidates) = %d, want 50", len(candidates))
	}
	if mc.lastLimit != 50 {
		t.Errorf("SearchIssues called with limit %d, want 50 (p.opts.M)", mc.lastLimit)
	}
}

func TestRunNoChangesSinceLastCheck(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	mc := newMockClient()
	mc.searchResult = []jira.Issue{
		{
			ID:  "10042",
			Key: "PROJ-123",
			Fields: jira.IssueFields{
				Labels:   []string{"bug"},
				Reporter: jira.User{AccountID: "reporter-id"},
				Created:  now.Add(-2 * time.Hour).Format("2006-01-02T15:04:05.000-0700"),
				Updated:  now.Format("2006-01-02T15:04:05.000-0700"),
			},
		},
	}

	setLastCheck(mc, "PROJ-123", "acme", "platform", now.Add(1*time.Hour))

	mc.comments["PROJ-123"] = []jira.Comment{
		{
			ID:      "50001",
			Body:    "old comment",
			Created: now.Add(-30 * time.Minute).Format("2006-01-02T15:04:05.000-0700"),
			Author: jira.User{
				AccountID:   "user1",
				AccountType: "atlassian",
			},
		},
	}

	dir := t.TempDir()
	outputPath := filepath.Join(dir, "dispatches.json")

	router := &stubRouter{stages: []string{"triage"}}
	p := newTestPoller(mc, router, Options{
		TargetRepo:  "acme/platform",
		JiraBaseURL: "https://acme.atlassian.net",
		JiraProject: "PROJ",
		OutputPath:  outputPath,
	})

	if err := p.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(data) != "[]\n" {
		t.Errorf("expected empty dispatches (no changes), got %q", string(data))
	}
}

func TestRunMultipleEvents(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	mc := newMockClient()
	mc.roleMembership = map[string]string{
		"user1": "Developers",
	}
	mc.searchResult = []jira.Issue{
		{
			ID:  "10042",
			Key: "PROJ-123",
			Fields: jira.IssueFields{
				Labels: []string{"ready-to-code", "bug"},
				Status: jira.Status{
					Name:           "Open",
					StatusCategory: jira.StatusCategory{Key: "new"},
				},
				Reporter: jira.User{AccountID: "reporter-id", AccountType: "atlassian"},
				Created:  now.Add(-2 * time.Hour).Format("2006-01-02T15:04:05.000-0700"),
				Updated:  now.Format("2006-01-02T15:04:05.000-0700"),
			},
		},
	}

	setLastCheck(mc, "PROJ-123", "acme", "platform", now.Add(-30*time.Minute))

	mc.comments["PROJ-123"] = []jira.Comment{
		{
			ID:      "50001",
			Body:    "/fs-triage check this",
			Created: now.Format("2006-01-02T15:04:05.000-0700"),
			Author:  jira.User{AccountID: "user1", AccountType: "atlassian"},
		},
	}
	mc.changelog["PROJ-123"] = []jira.ChangelogEntry{
		{
			ID:      "100",
			Created: now.Format("2006-01-02T15:04:05.000-0700"),
			Author:  jira.User{AccountID: "user1", AccountType: "atlassian"},
			Items: []jira.ChangeItem{
				{
					Field:      "labels",
					FromString: "bug",
					ToString:   "ready-to-code bug",
				},
			},
		},
	}

	dir := t.TempDir()
	outputPath := filepath.Join(dir, "dispatches.json")

	router := &stubRouter{stages: []string{"triage"}}
	p := newTestPoller(mc, router, Options{
		TargetRepo:  "acme/platform",
		JiraBaseURL: "https://acme.atlassian.net",
		JiraProject: "PROJ",
		OutputPath:  outputPath,
	})

	if err := p.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	var dispatches []poll.Dispatch
	if err := json.Unmarshal(data, &dispatches); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(dispatches) < 2 {
		t.Errorf("expected at least 2 dispatches (comment + label), got %d: %+v", len(dispatches), dispatches)
	}
}

func TestIsLockStale(t *testing.T) {
	threshold := 15 * time.Minute

	tests := []struct {
		name string
		lock LockValue
		want bool
	}{
		{
			name: "fresh lock",
			lock: LockValue{ID: "uuid", TS: time.Now().Format(time.RFC3339)},
			want: false,
		},
		{
			name: "stale lock",
			lock: LockValue{ID: "uuid", TS: time.Now().Add(-1 * time.Hour).Format(time.RFC3339)},
			want: true,
		},
		{
			name: "unparseable timestamp",
			lock: LockValue{ID: "uuid", TS: "garbage"},
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isLockStale(tc.lock, threshold)
			if got != tc.want {
				t.Errorf("isLockStale() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSelectRandom(t *testing.T) {
	issues := make([]jira.Issue, 10)
	for i := range issues {
		issues[i] = jira.Issue{Key: "PROJ-" + string(rune('0'+i))}
	}

	selected := selectRandom(issues, 3)
	if len(selected) != 3 {
		t.Errorf("selected %d, want 3", len(selected))
	}

	all := selectRandom(make([]jira.Issue, 2), 5)
	if len(all) != 2 {
		t.Errorf("selected %d, want 2", len(all))
	}
}

func TestDeduplicate(t *testing.T) {
	now := time.Now()
	events := []JiraEvent{
		{Type: "comment_added", CommentID: "123", IssueKey: "PROJ-1", UpdatedAt: now},
		{Type: "comment_added", CommentID: "123", IssueKey: "PROJ-1", UpdatedAt: now},
		{Type: "comment_added", CommentID: "456", IssueKey: "PROJ-1", UpdatedAt: now},
	}

	unique := deduplicate(events)
	if len(unique) != 2 {
		t.Errorf("expected 2 unique events, got %d", len(unique))
	}
}

func TestFilterBotEvents(t *testing.T) {
	events := []JiraEvent{
		{
			Type:          "comment_added",
			CommentAuthor: jira.User{AccountID: "bot", AccountType: "app"},
		},
		{
			Type:          "comment_added",
			CommentAuthor: jira.User{AccountID: "human", AccountType: "atlassian"},
		},
	}

	filtered := filterBotEvents(events)
	if len(filtered) != 1 {
		t.Errorf("expected 1 event after bot filter, got %d", len(filtered))
	}
	if filtered[0].CommentAuthor.AccountID != "human" {
		t.Error("expected human event to remain")
	}
}

func TestSplitOwnerRepo(t *testing.T) {
	tests := []struct {
		input     string
		wantOwner string
		wantRepo  string
	}{
		{"acme/platform", "acme", "platform"},
		{"org/sub/project", "org/sub", "project"},
		{"project", "", "project"},
	}
	for _, tc := range tests {
		owner, repo := splitOwnerRepo(tc.input)
		if owner != tc.wantOwner || repo != tc.wantRepo {
			t.Errorf("splitOwnerRepo(%q) = (%q, %q), want (%q, %q)",
				tc.input, owner, repo, tc.wantOwner, tc.wantRepo)
		}
	}
}

func TestDetectChanges_FirstPoll(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	mc := newMockClient()

	p := New(mc, nil, Options{
		TargetRepo:  "acme/platform",
		JiraBaseURL: "https://acme.atlassian.net",
		JiraProject: "PROJ",
	})

	issue := jira.Issue{
		ID:  "10042",
		Key: "PROJ-123",
		Fields: jira.IssueFields{
			Labels:   []string{"bug"},
			Reporter: jira.User{AccountID: "reporter-id"},
			Created:  now.Format("2006-01-02T15:04:05.000-0700"),
		},
	}

	result, err := p.detectChanges(context.Background(), issue, time.Time{})
	if err != nil {
		t.Fatalf("detectChanges() error: %v", err)
	}

	found := false
	for _, e := range result.events {
		if e.Type == "opened" {
			found = true
		}
	}
	if !found {
		t.Error("expected 'opened' event on first poll")
	}
}

func TestDetectChanges_StatusChange(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	lastCheck := now.Add(-30 * time.Minute)
	mc := newMockClient()

	mc.changelog["PROJ-123"] = []jira.ChangelogEntry{
		{
			ID:      "200",
			Created: now.Format("2006-01-02T15:04:05.000-0700"),
			Author:  jira.User{AccountID: "user1", AccountType: "atlassian"},
			Items: []jira.ChangeItem{
				{
					Field:      "status",
					FromString: "Open",
					ToString:   "Done",
				},
			},
		},
	}

	p := New(mc, nil, Options{
		TargetRepo:  "acme/platform",
		JiraBaseURL: "https://acme.atlassian.net",
		JiraProject: "PROJ",
	})

	issue := jira.Issue{
		ID:  "10042",
		Key: "PROJ-123",
		Fields: jira.IssueFields{
			Labels: []string{"bug"},
			Status: jira.Status{
				Name:           "Done",
				StatusCategory: jira.StatusCategory{Key: "done"},
			},
			Reporter: jira.User{AccountID: "reporter-id"},
			Created:  now.Add(-2 * time.Hour).Format("2006-01-02T15:04:05.000-0700"),
		},
	}

	result, err := p.detectChanges(context.Background(), issue, lastCheck)
	if err != nil {
		t.Fatalf("detectChanges() error: %v", err)
	}

	found := false
	for _, e := range result.events {
		if e.Type == "closed" {
			found = true
		}
	}
	if !found {
		t.Error("expected 'closed' event from status change to Done category")
	}
}

func TestDetectChanges_UnsupportedFieldAdvancesMaxSeen(t *testing.T) {
	// Regression test: when a changelog entry has only unsupported fields
	// (e.g., "assignee"), detectChanges should still report the timestamp
	// in maxSeen so processIssue can advance lastCheck past it, preventing
	// the poller from stalling on the same updates every cycle.
	now := time.Now().Truncate(time.Second)
	lastCheck := now.Add(-30 * time.Minute)
	mc := newMockClient()

	assigneeChangeTime := now.Add(-10 * time.Minute)
	mc.changelog["PROJ-123"] = []jira.ChangelogEntry{
		{
			ID:      "300",
			Created: assigneeChangeTime.Format("2006-01-02T15:04:05.000-0700"),
			Author:  jira.User{AccountID: "user1", AccountType: "atlassian"},
			Items: []jira.ChangeItem{
				{
					Field:      "assignee",
					FromString: "Alice",
					ToString:   "Bob",
				},
			},
		},
	}

	p := New(mc, nil, Options{
		TargetRepo:  "acme/platform",
		JiraBaseURL: "https://acme.atlassian.net",
		JiraProject: "PROJ",
	})

	issue := jira.Issue{
		ID:  "10042",
		Key: "PROJ-123",
		Fields: jira.IssueFields{
			Labels:   []string{"bug"},
			Reporter: jira.User{AccountID: "reporter-id"},
			Created:  now.Add(-2 * time.Hour).Format("2006-01-02T15:04:05.000-0700"),
		},
	}

	result, err := p.detectChanges(context.Background(), issue, lastCheck)
	if err != nil {
		t.Fatalf("detectChanges() error: %v", err)
	}

	if len(result.events) != 0 {
		t.Errorf("expected 0 routable events for unsupported field, got %d", len(result.events))
	}
	if result.maxSeen.IsZero() {
		t.Fatal("maxSeen should be non-zero for unsupported changelog entries")
	}
	if !result.maxSeen.Equal(assigneeChangeTime) {
		t.Errorf("maxSeen = %v, want %v", result.maxSeen, assigneeChangeTime)
	}
}

func TestParseJiraTimestamp(t *testing.T) {
	tests := []struct {
		input string
		valid bool
	}{
		{"2026-01-15T10:30:00.000-0500", true},
		{"2026-01-15T10:30:00.000+0000", true},
		{"2026-01-15T10:30:00.000Z", true},
		{"2026-01-15T10:30:00Z", true},
		{"garbage", false},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			_, err := parseJiraTimestamp(tc.input)
			if tc.valid && err != nil {
				t.Errorf("parseJiraTimestamp(%q) unexpected error: %v", tc.input, err)
			}
			if !tc.valid && err == nil {
				t.Errorf("parseJiraTimestamp(%q) expected error, got nil", tc.input)
			}
		})
	}
}

// setLastCheck is a test helper to pre-populate lastCheck for an issue.
func setLastCheck(mc *mockClient, issueKey, owner, repo string, t time.Time) {
	propKey := lastCheckPropertyKey(owner, repo)
	ts, _ := json.Marshal(t.UTC().Format(time.RFC3339Nano))
	if mc.properties[issueKey] == nil {
		mc.properties[issueKey] = make(map[string]json.RawMessage)
	}
	mc.properties[issueKey][propKey] = ts
}

func TestReadLock_NotFound(t *testing.T) {
	mc := newMockClient()
	p := New(mc, nil, Options{TargetRepo: "acme/platform"})

	// No properties set — mock returns forge.ErrNotFound.
	lock, err := p.readLock(context.Background(), "PROJ-123")
	if err != nil {
		t.Fatalf("readLock() should return nil error for missing property, got: %v", err)
	}
	if lock != nil {
		t.Errorf("readLock() = %+v, want nil (unlocked)", lock)
	}
}

func TestReadLastCheck_NotFound(t *testing.T) {
	mc := newMockClient()
	p := New(mc, nil, Options{TargetRepo: "acme/platform"})

	// No properties set — mock returns forge.ErrNotFound.
	lastCheck, err := p.readLastCheck(context.Background(), "PROJ-123")
	if err != nil {
		t.Fatalf("readLastCheck() should return zero time for missing property, got error: %v", err)
	}
	if !lastCheck.IsZero() {
		t.Errorf("readLastCheck() = %v, want zero time", lastCheck)
	}
}

func TestLastCheck_SubSecondPrecision(t *testing.T) {
	mc := newMockClient()
	p := newTestPoller(mc, nil, Options{TargetRepo: "acme/platform"})

	ctx := context.Background()
	ts := time.Date(2026, 7, 30, 19, 23, 30, 556000000, time.UTC) // .556s

	if err := p.advanceLastCheck(ctx, "PROJ-123", ts); err != nil {
		t.Fatalf("advanceLastCheck() error: %v", err)
	}

	got, err := p.readLastCheck(ctx, "PROJ-123")
	if err != nil {
		t.Fatalf("readLastCheck() error: %v", err)
	}

	if !got.Equal(ts) {
		t.Errorf("readLastCheck() = %v, want %v (sub-second precision lost)", got, ts)
	}

	// A comment at the exact same timestamp should NOT pass the After check.
	if ts.After(got) {
		t.Error("timestamp.After(lastCheck) should be false for equal times")
	}
}

func TestRunFirstPoll_NoLockProperty(t *testing.T) {
	// Verifies the full poll cycle works when no entity properties exist
	// (first poll on a fresh issue), which is the forge.ErrNotFound path.
	now := time.Now().Truncate(time.Second)
	mc := newMockClient()
	mc.searchResult = []jira.Issue{
		{
			ID:  "10042",
			Key: "PROJ-123",
			Fields: jira.IssueFields{
				Labels:   []string{"bug"},
				Reporter: jira.User{AccountID: "reporter-id", AccountType: "atlassian"},
				Created:  now.Add(-1 * time.Hour).Format("2006-01-02T15:04:05.000-0700"),
				Updated:  now.Format("2006-01-02T15:04:05.000-0700"),
			},
		},
	}
	mc.comments["PROJ-123"] = []jira.Comment{
		{
			ID:      "50001",
			Body:    "/fs-triage check this",
			Created: now.Format("2006-01-02T15:04:05.000-0700"),
			Author:  jira.User{AccountID: "user1", AccountType: "atlassian"},
		},
	}

	dir := t.TempDir()
	outputPath := filepath.Join(dir, "dispatches.json")

	router := &stubRouter{stages: []string{"triage"}}
	p := newTestPoller(mc, router, Options{
		TargetRepo:  "acme/platform",
		JiraBaseURL: "https://acme.atlassian.net",
		JiraProject: "PROJ",
		OutputPath:  outputPath,
	})

	if err := p.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	var dispatches []poll.Dispatch
	if err := json.Unmarshal(data, &dispatches); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(dispatches) == 0 {
		t.Error("expected dispatches on first poll with no prior entity properties")
	}
}

func TestRunUnsupportedChangelogField_AdvancesLastCheck(t *testing.T) {
	// Regression test: when a changelog entry contains only unsupported fields
	// (e.g., "assignee"), processIssue should still advance lastCheck so the
	// poller does not re-scan the same updates every cycle.
	now := time.Now().Truncate(time.Second)
	mc := newMockClient()
	mc.searchResult = []jira.Issue{
		{
			ID:  "10042",
			Key: "PROJ-123",
			Fields: jira.IssueFields{
				Labels:   []string{"bug"},
				Reporter: jira.User{AccountID: "reporter-id", AccountType: "atlassian"},
				Created:  now.Add(-2 * time.Hour).Format("2006-01-02T15:04:05.000-0700"),
				Updated:  now.Format("2006-01-02T15:04:05.000-0700"),
			},
		},
	}

	setLastCheck(mc, "PROJ-123", "acme", "platform", now.Add(-30*time.Minute))

	// Only an unsupported changelog field — should produce zero routable events
	// but still advance lastCheck.
	assigneeChangeTime := now.Add(-10 * time.Minute)
	mc.changelog["PROJ-123"] = []jira.ChangelogEntry{
		{
			ID:      "300",
			Created: assigneeChangeTime.Format("2006-01-02T15:04:05.000-0700"),
			Author:  jira.User{AccountID: "user1", AccountType: "atlassian"},
			Items: []jira.ChangeItem{
				{
					Field:      "assignee",
					FromString: "Alice",
					ToString:   "Bob",
				},
			},
		},
	}

	dir := t.TempDir()
	outputPath := filepath.Join(dir, "dispatches.json")

	router := &stubRouter{stages: []string{"triage"}}
	p := newTestPoller(mc, router, Options{
		TargetRepo:  "acme/platform",
		JiraBaseURL: "https://acme.atlassian.net",
		JiraProject: "PROJ",
		OutputPath:  outputPath,
	})

	if err := p.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	// Verify no dispatches were produced (unsupported field).
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(data) != "[]\n" {
		t.Errorf("expected empty dispatches for unsupported field change, got %q", string(data))
	}

	// Verify lastCheck was advanced past the changelog entry.
	lastCheck, err := p.readLastCheck(context.Background(), "PROJ-123")
	if err != nil {
		t.Fatalf("readLastCheck() error: %v", err)
	}
	if lastCheck.IsZero() {
		t.Fatal("lastCheck should have been advanced, but is zero")
	}
	if !lastCheck.Equal(assigneeChangeTime) && !lastCheck.After(now.Add(-30*time.Minute)) {
		t.Errorf("lastCheck = %v, expected it to be advanced past the original %v", lastCheck, now.Add(-30*time.Minute))
	}
}
