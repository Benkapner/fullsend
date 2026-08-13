package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fullsend-ai/fullsend/internal/config"
	"github.com/fullsend-ai/fullsend/internal/forge"
	"github.com/fullsend-ai/fullsend/internal/forge/jira"
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
		trackerName: TrackerGitHub,
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
		trackerName: TrackerGitHub,
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
		trackerName: TrackerGitHub,
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
		trackerName: TrackerGitHub,
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

func TestRunIssuesPostComment_JiraCapsStickyMaxSize(t *testing.T) {
	fc := forge.NewFakeClient()
	fc.AuthenticatedUser = "bot"
	tc := tracker.NewForgeClient(fc)
	ctx := context.Background()

	// Sized to land in the 32-65 KiB band called out by the review: over
	// Jira's MarkdownToADF limit (jira.MaxMarkdownBytes = 32 KiB) but
	// under sticky's own default cap (65000), so only a tracker-aware
	// MaxSize catches it.
	firstBody := strings.Repeat("a", 40000)

	cfg := &issuesPostCommentConfig{
		trackerName: TrackerJira,
		project:     "acme/widgets",
		number:      42,
		marker:      "<!-- test:agent -->",
		testClient:  tc,
		testPrinter: ui.New(io.Discard),
		testBody:    firstBody,
	}
	require.NoError(t, runIssuesPostComment(ctx, cfg))

	cfg.testBody = "second run"
	require.NoError(t, runIssuesPostComment(ctx, cfg))

	comments, err := tc.ListComments(ctx, "acme/widgets", 42)
	require.NoError(t, err)
	require.Len(t, comments, 1)

	// Combining "second run" with the collapsed 40 KB history would
	// exceed jira.MaxMarkdownBytes, so the Jira path must trim the
	// history rather than assembling a body that would fail once it
	// reaches Jira's MarkdownToADF.
	assert.LessOrEqual(t, len(comments[0].Body), jira.MaxMarkdownBytes)
	assert.NotContains(t, string(comments[0].Body), "Previous run")
	assert.Contains(t, string(comments[0].Body), "second run")
}

func TestRunIssuesPostComment_NonJiraKeepsDefaultStickyMaxSize(t *testing.T) {
	fc := forge.NewFakeClient()
	fc.AuthenticatedUser = "bot"
	tc := tracker.NewForgeClient(fc)
	ctx := context.Background()

	firstBody := strings.Repeat("a", 40000)

	cfg := &issuesPostCommentConfig{
		trackerName: TrackerGitHub,
		project:     "acme/widgets",
		number:      42,
		marker:      "<!-- test:agent -->",
		testClient:  tc,
		testPrinter: ui.New(io.Discard),
		testBody:    firstBody,
	}
	require.NoError(t, runIssuesPostComment(ctx, cfg))

	cfg.testBody = "second run"
	require.NoError(t, runIssuesPostComment(ctx, cfg))

	comments, err := tc.ListComments(ctx, "acme/widgets", 42)
	require.NoError(t, err)
	require.Len(t, comments, 1)

	// Non-Jira trackers keep sticky's default 65000-byte cap, so the same
	// accumulated body (well under 65000) keeps its history intact.
	assert.Contains(t, string(comments[0].Body), "Previous run")
}

func TestValidateJiraMarker_RejectsEscapedChars(t *testing.T) {
	unsafeChars := []string{`\`, "*", "_", "`", "[", "]", "&"}
	for _, c := range unsafeChars {
		marker := "<!-- fullsend:post" + c + "review -->"
		err := validateJiraMarker(marker)
		assert.Errorf(t, err, "validateJiraMarker(%q): want error, marker contains %q which Jira's ADFToMarkdown escapes", marker, c)
	}
}

func TestValidateJiraMarker_AllowsSafeChars(t *testing.T) {
	safeMarkers := []string{
		"<!-- fullsend:post-review -->",
		"<!-- fullsend:triage-agent -->",
		"<!-- fullsend:post:review -->",
	}
	for _, marker := range safeMarkers {
		assert.NoErrorf(t, validateJiraMarker(marker), "validateJiraMarker(%q): want no error", marker)
	}
}

func TestRunIssuesPostComment_JiraRejectsUnsafeMarker(t *testing.T) {
	cfg := &issuesPostCommentConfig{
		trackerName: TrackerJira,
		project:     "PROJ",
		number:      42,
		marker:      "<!-- fullsend:post_review -->",
		testPrinter: ui.New(io.Discard),
		testBody:    "body",
	}

	err := runIssuesPostComment(context.Background(), cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--marker")
}

func TestRunIssuesPostComment_JiraRoundTripUpdatesInPlace(t *testing.T) {
	// Drives postTrackerStickyComment twice through tracker.NewJiraClient
	// (via NewFakeJiraClient) so comment bodies actually round-trip
	// through the real jira.MarkdownToADF/ADFToMarkdown conversions,
	// unlike the forge.NewFakeClient-backed tests above. This is the
	// regression test for marker re-detection surviving that round trip:
	// without it, a marker containing an escaped character would create
	// a new comment every run instead of updating in place.
	tc, err := tracker.NewFakeJiraClient("https://acme.atlassian.net")
	require.NoError(t, err)
	ctx := context.Background()

	cfg := &issuesPostCommentConfig{
		trackerName: TrackerJira,
		project:     "PROJ",
		number:      42,
		marker:      "<!-- fullsend:triage-agent -->",
		testClient:  tc,
		testPrinter: ui.New(io.Discard),
		testBody:    "first run",
	}
	require.NoError(t, runIssuesPostComment(ctx, cfg))

	cfg.testBody = "second run"
	require.NoError(t, runIssuesPostComment(ctx, cfg))

	comments, err := tc.ListComments(ctx, "PROJ", 42)
	require.NoError(t, err)
	require.Len(t, comments, 1, "second run should update the existing comment in place, not flood a new one")
	assert.Contains(t, string(comments[0].Body), "second run")
	assert.Contains(t, string(comments[0].Body), "Previous run")
}

// --- resolveTracker tests ---
//
// fullsend-ai/fullsend#5991 specifies --tracker is required unless a
// default is supplied via config.

func TestResolveTracker_FlagOverridesConfig(t *testing.T) {
	reader, err := config.ParsePerRepoConfig([]byte("tracker: jira\n"))
	require.NoError(t, err)

	got, err := resolveTracker(TrackerGitHub, "", reader)
	require.NoError(t, err)
	assert.Equal(t, TrackerGitHub, got)
}

func TestResolveTracker_FallsBackToConfigReader(t *testing.T) {
	reader, err := config.ParsePerRepoConfig([]byte("tracker: jira\n"))
	require.NoError(t, err)

	got, err := resolveTracker("", "", reader)
	require.NoError(t, err)
	assert.Equal(t, TrackerJira, got)
}

func TestResolveTracker_FallsBackToFullsendDirConfig(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("tracker: gitlab\n"), 0o644))

	got, err := resolveTracker("", dir, nil)
	require.NoError(t, err)
	assert.Equal(t, TrackerGitLab, got)
}

func TestResolveTracker_NoFlagNoConfig_Errors(t *testing.T) {
	_, err := resolveTracker("", "", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--tracker is required")
}

func TestResolveTracker_FullsendDirWithoutTrackerSet_Errors(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("runtime: claude\n"), 0o644))

	_, err := resolveTracker("", dir, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--tracker is required")
}

// --- config-default --tracker integration tests ---

func TestRunIssuesPostComment_TrackerFromConfig(t *testing.T) {
	fc := forge.NewFakeClient()
	fc.AuthenticatedUser = "bot"
	tc := tracker.NewForgeClient(fc)

	reader, err := config.ParsePerRepoConfig([]byte("tracker: github\n"))
	require.NoError(t, err)

	cfg := &issuesPostCommentConfig{
		project:          "acme/widgets",
		number:           42,
		marker:           "<!-- test:agent -->",
		testClient:       tc,
		testConfigReader: reader,
		testPrinter:      ui.New(io.Discard),
		testBody:         "from config default",
	}

	err = runIssuesPostComment(context.Background(), cfg)
	require.NoError(t, err)

	comments, err := tc.ListComments(context.Background(), "acme/widgets", 42)
	require.NoError(t, err)
	require.Len(t, comments, 1)
	assert.Contains(t, string(comments[0].Body), "from config default")
}

func TestRunIssuesPostComment_TrackerFlagOverridesConfig(t *testing.T) {
	// --tracker jira should win over a "github" config default and
	// trigger jira-specific marker validation, proving the flag takes
	// priority over the config value.
	reader, err := config.ParsePerRepoConfig([]byte("tracker: github\n"))
	require.NoError(t, err)

	cfg := &issuesPostCommentConfig{
		trackerName:      TrackerJira,
		project:          "PROJ",
		number:           42,
		marker:           "<!-- fullsend:post_review -->", // contains an unsafe char
		testConfigReader: reader,
		testPrinter:      ui.New(io.Discard),
		testBody:         "body",
	}

	err = runIssuesPostComment(context.Background(), cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--marker")
}

func TestRunIssuesPostComment_NoTrackerNoConfig_Errors(t *testing.T) {
	cfg := &issuesPostCommentConfig{
		project:     "acme/widgets",
		number:      42,
		marker:      "<!-- test:agent -->",
		testPrinter: ui.New(io.Discard),
		testBody:    "body",
	}

	err := runIssuesPostComment(context.Background(), cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--tracker is required")
}

// --- cobra-level: --tracker is no longer unconditionally required ---

func TestIssuesGetCmd_TrackerNotRequired(t *testing.T) {
	cmd := newIssuesGetCmd()
	cmd.SetArgs([]string{"--project", "acme/widgets", "--number", "1"})
	err := cmd.Execute()
	// No --tracker and no config default available: this should reach
	// RunE and fail there via resolveTracker, not at cobra's
	// required-flag validation stage.
	require.Error(t, err)
	assert.NotContains(t, err.Error(), `required flag(s) "tracker"`)
	assert.Contains(t, err.Error(), "--tracker is required")
}

func TestIssuesPostCommentCmd_TrackerNotRequired(t *testing.T) {
	cmd := newIssuesPostCommentCmd()
	cmd.SetArgs([]string{"--project", "acme/widgets", "--number", "1", "--marker", "<!-- test -->"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.NotContains(t, err.Error(), `required flag(s) "tracker"`)
	assert.Contains(t, err.Error(), "--tracker is required")
}
