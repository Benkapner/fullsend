package cli

import (
	"context"
	"io"
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
