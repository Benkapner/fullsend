package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fullsend-ai/fullsend/internal/forge"
	"github.com/fullsend-ai/fullsend/internal/sticky"
	"github.com/fullsend-ai/fullsend/internal/tracker"
	"github.com/fullsend-ai/fullsend/internal/ui"
)

func TestNewIssuesCmd_SubcommandRegistration(t *testing.T) {
	cmd := newIssuesCmd()

	var names []string
	for _, sub := range cmd.Commands() {
		names = append(names, sub.Name())
	}
	assert.Contains(t, names, "get")
	assert.Contains(t, names, "post-comment")
}

func TestNewIssuesGetCmd_RequiredFlags(t *testing.T) {
	cmd := newIssuesGetCmd()

	for _, name := range []string{"tracker", "project", "number"} {
		f := cmd.Flags().Lookup(name)
		require.NotNil(t, f, "flag %q should exist", name)
	}
}

func TestNewIssuesGetCmd_OptionalFlags(t *testing.T) {
	cmd := newIssuesGetCmd()

	for _, name := range []string{"token", "jira-url", "jira-email"} {
		f := cmd.Flags().Lookup(name)
		require.NotNil(t, f, "flag %q should exist", name)
	}
}

func TestNewIssuesPostCommentCmd_RequiredFlags(t *testing.T) {
	cmd := newIssuesPostCommentCmd()

	for _, name := range []string{"tracker", "project", "number", "marker"} {
		f := cmd.Flags().Lookup(name)
		require.NotNil(t, f, "flag %q should exist", name)
	}
}

func TestNewIssuesPostCommentCmd_DefaultFlags(t *testing.T) {
	cmd := newIssuesPostCommentCmd()

	result := cmd.Flags().Lookup("result")
	require.NotNil(t, result)
	assert.Equal(t, "-", result.DefValue)

	dryRun := cmd.Flags().Lookup("dry-run")
	require.NotNil(t, dryRun)
	assert.Equal(t, "false", dryRun.DefValue)
}

func TestFindMarkedTrackerComment(t *testing.T) {
	marker := "<!-- test:marker -->"
	comments := []tracker.Comment{
		{ID: "1", Body: "irrelevant comment"},
		{ID: "2", Body: tracker.Body(marker + "\nsome content")},
		{ID: "3", Body: "another comment"},
	}

	found := findMarkedTrackerComment(comments, marker)
	require.NotNil(t, found)
	assert.Equal(t, "2", found.ID)
}

func TestFindMarkedTrackerComment_NotFound(t *testing.T) {
	comments := []tracker.Comment{
		{ID: "1", Body: "no marker here"},
	}

	found := findMarkedTrackerComment(comments, "<!-- missing -->")
	assert.Nil(t, found)
}

func TestFindMarkedTrackerComment_Empty(t *testing.T) {
	found := findMarkedTrackerComment(nil, "<!-- marker -->")
	assert.Nil(t, found)
}

func TestPostTrackerStickyComment_Create(t *testing.T) {
	fc := forge.NewFakeClient()
	fc.AuthenticatedUser = "bot"
	tc := tracker.NewForgeClient(fc)

	printer := ui.New(io.Discard)
	cfg := sticky.Config{Marker: "<!-- test -->"}

	url, err := postTrackerStickyComment(context.Background(), tc, "acme/widgets", 42, "hello world", cfg, printer)
	require.NoError(t, err)
	assert.NotEmpty(t, url)

	// Verify the comment was created with marker prefix.
	comments, err := tc.ListComments(context.Background(), "acme/widgets", 42)
	require.NoError(t, err)
	require.Len(t, comments, 1)
	assert.Contains(t, string(comments[0].Body), "<!-- test -->")
	assert.Contains(t, string(comments[0].Body), "hello world")
}

func TestPostTrackerStickyComment_Update(t *testing.T) {
	fc := forge.NewFakeClient()
	fc.AuthenticatedUser = "bot"
	tc := tracker.NewForgeClient(fc)

	printer := ui.New(io.Discard)
	cfg := sticky.Config{Marker: "<!-- test -->"}
	ctx := context.Background()

	// First post creates the comment.
	_, err := postTrackerStickyComment(ctx, tc, "acme/widgets", 42, "first run", cfg, printer)
	require.NoError(t, err)

	// Second post updates in-place.
	_, err = postTrackerStickyComment(ctx, tc, "acme/widgets", 42, "second run", cfg, printer)
	require.NoError(t, err)

	// Verify only one comment exists (updated, not duplicated).
	comments, err := tc.ListComments(ctx, "acme/widgets", 42)
	require.NoError(t, err)
	require.Len(t, comments, 1)
	assert.Contains(t, string(comments[0].Body), "second run")
	assert.Contains(t, string(comments[0].Body), "Previous run")
}

func TestPostTrackerStickyComment_EmptyBody(t *testing.T) {
	fc := forge.NewFakeClient()
	tc := tracker.NewForgeClient(fc)
	printer := ui.New(io.Discard)
	cfg := sticky.Config{Marker: "<!-- test -->"}

	_, err := postTrackerStickyComment(context.Background(), tc, "acme/widgets", 42, "", cfg, printer)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "comment body is empty")
}

func TestPostTrackerStickyComment_EmptyMarker(t *testing.T) {
	fc := forge.NewFakeClient()
	tc := tracker.NewForgeClient(fc)
	printer := ui.New(io.Discard)
	cfg := sticky.Config{Marker: ""}

	_, err := postTrackerStickyComment(context.Background(), tc, "acme/widgets", 42, "hello", cfg, printer)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "marker is empty")
}

func TestPostTrackerStickyComment_DryRun_Create(t *testing.T) {
	fc := forge.NewFakeClient()
	tc := tracker.NewForgeClient(fc)
	printer := ui.New(io.Discard)
	cfg := sticky.Config{Marker: "<!-- test -->", DryRun: true}

	url, err := postTrackerStickyComment(context.Background(), tc, "acme/widgets", 42, "hello", cfg, printer)
	require.NoError(t, err)
	assert.Empty(t, url) // dry run returns empty URL

	// No comment should be created.
	comments, err := tc.ListComments(context.Background(), "acme/widgets", 42)
	require.NoError(t, err)
	assert.Empty(t, comments)
}

func TestPostTrackerStickyComment_DryRun_Update(t *testing.T) {
	fc := forge.NewFakeClient()
	fc.AuthenticatedUser = "bot"
	tc := tracker.NewForgeClient(fc)
	printer := ui.New(io.Discard)
	cfg := sticky.Config{Marker: "<!-- test -->"}
	ctx := context.Background()

	// Create the initial comment (not dry run).
	_, err := postTrackerStickyComment(ctx, tc, "acme/widgets", 42, "first", cfg, printer)
	require.NoError(t, err)

	// Dry run update should not modify the comment.
	cfg.DryRun = true
	url, err := postTrackerStickyComment(ctx, tc, "acme/widgets", 42, "second", cfg, printer)
	require.NoError(t, err)
	assert.Empty(t, url)

	// Comment should still have original content.
	comments, err := tc.ListComments(ctx, "acme/widgets", 42)
	require.NoError(t, err)
	require.Len(t, comments, 1)
	assert.Contains(t, string(comments[0].Body), "first")
	assert.NotContains(t, string(comments[0].Body), "second")
}

// --- runIssuesGet tests ---

func newFakeTrackerWithIssue(t *testing.T, project string, number int) tracker.Client {
	t.Helper()
	fc := forge.NewFakeClient()
	fc.AuthenticatedUser = "bot"
	// Split project into owner/repo for the forge fake.
	issue, err := fc.CreateIssue(context.Background(), "acme", "widgets", "Widget is broken", "details here", "bug", "p1")
	require.NoError(t, err)
	require.Equal(t, number, issue.Number)
	// Add a comment.
	_, err = fc.CreateIssueComment(context.Background(), "acme", "widgets", number, "first comment body")
	require.NoError(t, err)
	return tracker.NewForgeClient(fc)
}

func TestRunIssuesGet(t *testing.T) {
	tc := newFakeTrackerWithIssue(t, "acme/widgets", 1)
	var buf bytes.Buffer

	cfg := &issuesGetConfig{
		project:    "acme/widgets",
		number:     1,
		testClient: tc,
		testWriter: &buf,
	}

	err := runIssuesGet(context.Background(), cfg)
	require.NoError(t, err)

	var result issueGetResult
	err = json.Unmarshal(buf.Bytes(), &result)
	require.NoError(t, err)

	assert.Equal(t, 1, result.Number)
	assert.Equal(t, "Widget is broken", result.Title)
	assert.Equal(t, "details here", result.Body)
	assert.Contains(t, result.Labels, "bug")
	assert.Contains(t, result.Labels, "p1")
	require.Len(t, result.Comments, 1)
	assert.Equal(t, "first comment body", result.Comments[0].Body)
	assert.Equal(t, "bot", result.Comments[0].Author)
}

func TestRunIssuesGet_NoComments(t *testing.T) {
	fc := forge.NewFakeClient()
	fc.AuthenticatedUser = "bot"
	_, err := fc.CreateIssue(context.Background(), "acme", "widgets", "No comments yet", "body text")
	require.NoError(t, err)
	tc := tracker.NewForgeClient(fc)

	var buf bytes.Buffer
	cfg := &issuesGetConfig{
		project:    "acme/widgets",
		number:     1,
		testClient: tc,
		testWriter: &buf,
	}

	err = runIssuesGet(context.Background(), cfg)
	require.NoError(t, err)

	var result issueGetResult
	err = json.Unmarshal(buf.Bytes(), &result)
	require.NoError(t, err)

	assert.Equal(t, "No comments yet", result.Title)
	assert.Empty(t, result.Comments)
}

func TestRunIssuesGet_InvalidNumber(t *testing.T) {
	fc := forge.NewFakeClient()
	tc := tracker.NewForgeClient(fc)

	for _, n := range []int{0, -1, -100} {
		cfg := &issuesGetConfig{
			project:    "acme/widgets",
			number:     n,
			testClient: tc,
			testWriter: io.Discard,
		}

		err := runIssuesGet(context.Background(), cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "--number must be a positive integer")
	}
}

func TestRunIssuesGet_NilLabelsOutputAsEmptyArray(t *testing.T) {
	fc := forge.NewFakeClient()
	fc.AuthenticatedUser = "bot"
	// Create an issue without any labels — Labels will be nil.
	_, err := fc.CreateIssue(context.Background(), "acme", "widgets", "No labels", "body text")
	require.NoError(t, err)
	tc := tracker.NewForgeClient(fc)

	var buf bytes.Buffer
	cfg := &issuesGetConfig{
		project:    "acme/widgets",
		number:     1,
		testClient: tc,
		testWriter: &buf,
	}

	err = runIssuesGet(context.Background(), cfg)
	require.NoError(t, err)

	// Verify labels serializes as [] not null.
	var raw map[string]json.RawMessage
	err = json.Unmarshal(buf.Bytes(), &raw)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(raw["labels"]), "nil labels should serialize as empty JSON array, not null")
}

func TestRunIssuesGet_IssueNotFound(t *testing.T) {
	fc := forge.NewFakeClient()
	tc := tracker.NewForgeClient(fc)

	cfg := &issuesGetConfig{
		project:    "acme/widgets",
		number:     999,
		testClient: tc,
		testWriter: io.Discard,
	}

	err := runIssuesGet(context.Background(), cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "getting issue")
}

// --- runIssuesPostComment tests ---

func TestRunIssuesPostComment_Create(t *testing.T) {
	fc := forge.NewFakeClient()
	fc.AuthenticatedUser = "bot"
	tc := tracker.NewForgeClient(fc)

	cfg := &issuesPostCommentConfig{
		project:     "acme/widgets",
		number:      42,
		marker:      "<!-- test:agent -->",
		testClient:  tc,
		testPrinter: ui.New(io.Discard),
		testBody:    "automated comment body",
	}

	err := runIssuesPostComment(context.Background(), cfg)
	require.NoError(t, err)

	// Verify the comment was created.
	comments, err := tc.ListComments(context.Background(), "acme/widgets", 42)
	require.NoError(t, err)
	require.Len(t, comments, 1)
	assert.Contains(t, string(comments[0].Body), "<!-- test:agent -->")
	assert.Contains(t, string(comments[0].Body), "automated comment body")
}

func TestRunIssuesPostComment_Update(t *testing.T) {
	fc := forge.NewFakeClient()
	fc.AuthenticatedUser = "bot"
	tc := tracker.NewForgeClient(fc)
	ctx := context.Background()

	cfg := &issuesPostCommentConfig{
		project:     "acme/widgets",
		number:      42,
		marker:      "<!-- test:agent -->",
		testClient:  tc,
		testPrinter: ui.New(io.Discard),
		testBody:    "first run",
	}

	// First run creates.
	err := runIssuesPostComment(ctx, cfg)
	require.NoError(t, err)

	// Second run updates.
	cfg.testBody = "second run"
	err = runIssuesPostComment(ctx, cfg)
	require.NoError(t, err)

	comments, err := tc.ListComments(ctx, "acme/widgets", 42)
	require.NoError(t, err)
	require.Len(t, comments, 1)
	assert.Contains(t, string(comments[0].Body), "second run")
}

func TestRunIssuesPostComment_InvalidNumber(t *testing.T) {
	cfg := &issuesPostCommentConfig{
		number:      0,
		marker:      "<!-- test -->",
		testPrinter: ui.New(io.Discard),
		testBody:    "body",
	}

	err := runIssuesPostComment(context.Background(), cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--number must be a positive integer")
}

func TestRunIssuesPostComment_EmptyMarker(t *testing.T) {
	cfg := &issuesPostCommentConfig{
		number:      1,
		marker:      "   ",
		testPrinter: ui.New(io.Discard),
		testBody:    "body",
	}

	err := runIssuesPostComment(context.Background(), cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--marker must not be empty")
}

func TestRunIssuesPostComment_DryRun(t *testing.T) {
	fc := forge.NewFakeClient()
	tc := tracker.NewForgeClient(fc)

	cfg := &issuesPostCommentConfig{
		project:     "acme/widgets",
		number:      42,
		marker:      "<!-- test:agent -->",
		dryRun:      true,
		testClient:  tc,
		testPrinter: ui.New(io.Discard),
		testBody:    "dry run body",
	}

	err := runIssuesPostComment(context.Background(), cfg)
	require.NoError(t, err)

	// No comment should be created in dry-run mode.
	comments, err := tc.ListComments(context.Background(), "acme/widgets", 42)
	require.NoError(t, err)
	assert.Empty(t, comments)
}

func TestRunIssuesPostComment_FromFile(t *testing.T) {
	fc := forge.NewFakeClient()
	fc.AuthenticatedUser = "bot"
	tc := tracker.NewForgeClient(fc)

	// Write body to a temp file.
	dir := t.TempDir()
	bodyFile := filepath.Join(dir, "body.txt")
	err := os.WriteFile(bodyFile, []byte("comment from file"), 0o644)
	require.NoError(t, err)

	cfg := &issuesPostCommentConfig{
		project:     "acme/widgets",
		number:      42,
		marker:      "<!-- test:agent -->",
		result:      bodyFile,
		testClient:  tc,
		testPrinter: ui.New(io.Discard),
	}

	err = runIssuesPostComment(context.Background(), cfg)
	require.NoError(t, err)

	comments, err := tc.ListComments(context.Background(), "acme/widgets", 42)
	require.NoError(t, err)
	require.Len(t, comments, 1)
	assert.Contains(t, string(comments[0].Body), "comment from file")
}
