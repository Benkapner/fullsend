package tracker

import (
	"context"
	"errors"
	"testing"

	"github.com/fullsend-ai/fullsend/internal/forge"
	"github.com/fullsend-ai/fullsend/internal/forge/jira"
)

// newTestJiraClient constructs a JiraClient for tests, failing immediately
// on a validation error so call sites can stay a single line.
func newTestJiraClient(t *testing.T, jc jiraClient, baseURL string) *JiraClient {
	t.Helper()
	c, err := NewJiraClient(jc, baseURL)
	if err != nil {
		t.Fatalf("NewJiraClient(%q) returned error: %v", baseURL, err)
	}
	return c
}

func TestNewJiraClient_RejectsCredentialBaseURL(t *testing.T) {
	_, err := NewJiraClient(&FakeJiraClient{}, "https://user:token@acme.atlassian.net")
	if err == nil {
		t.Fatal("NewJiraClient with credential-bearing base URL: got nil error, want an error")
	}
}

func TestJiraClient_GetIssue(t *testing.T) {
	fc := &FakeJiraClient{
		Issues: map[string]*jira.Issue{
			"PROJ-42": {
				Key: "PROJ-42",
				Fields: jira.IssueFields{
					Summary: "Widget is broken",
					Description: map[string]any{
						"type":    "doc",
						"version": 1,
						"content": []any{
							map[string]any{
								"type": "paragraph",
								"content": []any{
									map[string]any{"type": "text", "text": "details here"},
								},
							},
						},
					},
					Labels: []string{"bug"},
				},
			},
		},
	}

	c := newTestJiraClient(t, fc, "https://acme.atlassian.net")
	issue, err := c.GetIssue(context.Background(), "PROJ", 42)
	if err != nil {
		t.Fatalf("GetIssue returned error: %v", err)
	}
	if issue.Number != 42 {
		t.Errorf("issue.Number = %d, want 42", issue.Number)
	}
	if issue.Title != "Widget is broken" {
		t.Errorf("issue.Title = %q, want %q", issue.Title, "Widget is broken")
	}
	if issue.Body != "details here" {
		t.Errorf("issue.Body = %q, want %q", issue.Body, "details here")
	}
	if len(issue.Labels) != 1 || issue.Labels[0] != "bug" {
		t.Errorf("issue.Labels = %+v, want [bug]", issue.Labels)
	}
	if issue.URL != "https://acme.atlassian.net/browse/PROJ-42" {
		t.Errorf("issue.URL = %q, want %q", issue.URL, "https://acme.atlassian.net/browse/PROJ-42")
	}
}

func TestJiraClient_GetIssue_NotFound(t *testing.T) {
	fc := &FakeJiraClient{Issues: map[string]*jira.Issue{}}
	c := newTestJiraClient(t, fc, "https://acme.atlassian.net")

	_, err := c.GetIssue(context.Background(), "PROJ", 999)
	if !IsNotFound(err) {
		t.Errorf("GetIssue error = %v, want tracker.IsNotFound", err)
	}
}

func TestJiraClient_ListComments(t *testing.T) {
	fc := &FakeJiraClient{
		Comments: map[string][]jira.Comment{
			"PROJ-42": {
				{
					ID:      "1",
					Body:    "plain string body",
					Author:  jira.User{DisplayName: "Jane Doe"},
					Created: "2026-08-06T00:00:00.000+0000",
				},
			},
		},
	}
	c := newTestJiraClient(t, fc, "https://acme.atlassian.net")

	comments, err := c.ListComments(context.Background(), "PROJ", 42)
	if err != nil {
		t.Fatalf("ListComments returned error: %v", err)
	}
	if len(comments) != 1 {
		t.Fatalf("len(comments) = %d, want 1", len(comments))
	}
	if comments[0].Body != "plain string body" {
		t.Errorf("comments[0].Body = %q, want %q", comments[0].Body, "plain string body")
	}
	if comments[0].Author != "Jane Doe" {
		t.Errorf("comments[0].Author = %q, want %q", comments[0].Author, "Jane Doe")
	}
}

func TestJiraClient_ListComments_EditedCommentAttributesToEditor(t *testing.T) {
	// Jira sets UpdateAuthor when a comment has been edited, which may
	// differ from Author if someone with Edit-All-Comments rewrote
	// another user's comment. Consumers of tracker.Comment.Author must
	// see the editor's identity, not the original author's, mirroring
	// jirapoll/discover.go's edit-attribution logic (ADR 0054) so a
	// rewritten comment can't be misattributed to whoever originally
	// posted it.
	fc := &FakeJiraClient{
		Comments: map[string][]jira.Comment{
			"PROJ-42": {
				{
					ID:           "1",
					Body:         "edited body",
					Author:       jira.User{DisplayName: "Original Author"},
					UpdateAuthor: jira.User{AccountID: "acct-2", DisplayName: "Editor"},
					Created:      "2026-08-06T00:00:00.000+0000",
					Updated:      "2026-08-06T01:00:00.000+0000",
				},
				{
					ID:      "2",
					Body:    "unedited body",
					Author:  jira.User{DisplayName: "Original Author"},
					Created: "2026-08-06T00:00:00.000+0000",
				},
			},
		},
	}
	c := newTestJiraClient(t, fc, "https://acme.atlassian.net")

	comments, err := c.ListComments(context.Background(), "PROJ", 42)
	if err != nil {
		t.Fatalf("ListComments returned error: %v", err)
	}
	if len(comments) != 2 {
		t.Fatalf("len(comments) = %d, want 2", len(comments))
	}
	if comments[0].Author != "Editor" {
		t.Errorf("edited comment Author = %q, want %q (the editor, not the original author)", comments[0].Author, "Editor")
	}
	if comments[1].Author != "Original Author" {
		t.Errorf("unedited comment Author = %q, want %q (UpdateAuthor unset, so Author stands)", comments[1].Author, "Original Author")
	}
}

func TestJiraClient_ListComments_UpdateAuthorIgnoredWithoutLaterUpdatedTimestamp(t *testing.T) {
	// UpdateAuthor.AccountID alone isn't a reliable edit signal — mirror
	// jirapoll/discover.go's defense-in-depth gate of also requiring
	// Updated to parse and be after Created before trusting it.
	fc := &FakeJiraClient{
		Comments: map[string][]jira.Comment{
			"PROJ-42": {
				{
					ID:           "1",
					Body:         "not actually edited",
					Author:       jira.User{DisplayName: "Original Author"},
					UpdateAuthor: jira.User{AccountID: "acct-2", DisplayName: "Editor"},
					Created:      "2026-08-06T00:00:00.000+0000",
					Updated:      "2026-08-06T00:00:00.000+0000",
				},
				{
					ID:           "2",
					Body:         "unparseable updated timestamp",
					Author:       jira.User{DisplayName: "Original Author"},
					UpdateAuthor: jira.User{AccountID: "acct-2", DisplayName: "Editor"},
					Created:      "2026-08-06T00:00:00.000+0000",
					Updated:      "not-a-timestamp",
				},
			},
		},
	}
	c := newTestJiraClient(t, fc, "https://acme.atlassian.net")

	comments, err := c.ListComments(context.Background(), "PROJ", 42)
	if err != nil {
		t.Fatalf("ListComments returned error: %v", err)
	}
	if len(comments) != 2 {
		t.Fatalf("len(comments) = %d, want 2", len(comments))
	}
	if comments[0].Author != "Original Author" {
		t.Errorf("Updated == Created comment Author = %q, want %q (not after Created, so not treated as edited)", comments[0].Author, "Original Author")
	}
	if comments[1].Author != "Original Author" {
		t.Errorf("unparseable Updated comment Author = %q, want %q (can't confirm edit, so not treated as edited)", comments[1].Author, "Original Author")
	}
}

func TestJiraClient_CreateComment(t *testing.T) {
	fc := &FakeJiraClient{}
	c := newTestJiraClient(t, fc, "https://acme.atlassian.net")

	comment, err := c.CreateComment(context.Background(), "PROJ", 42, "**hello** there")
	if err != nil {
		t.Fatalf("CreateComment returned error: %v", err)
	}
	if fc.CreatedBody != "**hello** there" {
		t.Errorf("underlying jira client received body %q, want %q", fc.CreatedBody, "**hello** there")
	}
	if _, ok := fc.Comments["PROJ-42"]; !ok {
		t.Fatal("expected comment to be created against issue key PROJ-42")
	}
	if comment.Body != "**hello** there" {
		t.Errorf("comment.Body = %q, want %q (Markdown, consistent with ListComments)", comment.Body, "**hello** there")
	}
}

func TestJiraClient_UpdateComment(t *testing.T) {
	fc := &FakeJiraClient{}
	c := newTestJiraClient(t, fc, "https://acme.atlassian.net")

	if err := c.UpdateComment(context.Background(), "PROJ", 42, "50001", "updated text"); err != nil {
		t.Fatalf("UpdateComment returned error: %v", err)
	}
	if fc.UpdatedIssueKey != "PROJ-42" {
		t.Errorf("underlying jira client received issue key %q, want %q", fc.UpdatedIssueKey, "PROJ-42")
	}
	if fc.UpdatedCommentID != "50001" {
		t.Errorf("underlying jira client received comment ID %q, want %q", fc.UpdatedCommentID, "50001")
	}
	if fc.UpdatedBody != "updated text" {
		t.Errorf("underlying jira client received body %q, want %q", fc.UpdatedBody, "updated text")
	}
}

func TestJiraClient_NotFoundWrapping(t *testing.T) {
	// JiraClient must wrap forge.ErrNotFound into tracker.ErrNotFound so
	// callers using tracker.IsNotFound get the expected result. Verify
	// that the wrapper also preserves the underlying forge.ErrNotFound.
	fc := &FakeJiraClient{Issues: map[string]*jira.Issue{}}
	c := newTestJiraClient(t, fc, "https://acme.atlassian.net")

	_, err := c.GetIssue(context.Background(), "PROJ", 999)
	if err == nil {
		t.Fatal("GetIssue on missing issue: got nil error, want not-found")
	}
	if !IsNotFound(err) {
		t.Errorf("GetIssue error does not satisfy tracker.IsNotFound: %v", err)
	}
	// The underlying forge.ErrNotFound should still be reachable for debug.
	if !errors.Is(err, forge.ErrNotFound) {
		t.Errorf("GetIssue error does not unwrap to forge.ErrNotFound: %v", err)
	}
}

var _ Client = (*JiraClient)(nil)
