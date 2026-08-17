package tracker

import (
	"context"
	"fmt"

	"github.com/fullsend-ai/fullsend/internal/forge"
	"github.com/fullsend-ai/fullsend/internal/forge/jira"
)

// FakeJiraClient is an in-memory fake implementing the jiraClient
// interface, exported (unlike its ecosystem forge.FakeClient
// counterpart, which lives alongside it) so tests in other packages can
// exercise tracker.Client's Jira path — including its real
// jira.MarkdownToADF/ADFToMarkdown round-trip — without a live Jira
// instance. CreateComment/UpdateComment store the body as real ADF
// (mirroring Jira echoing back what it stored), so ListComments/GetIssue
// read it back through the same escaping/decoding logic a live Jira
// instance would apply.
type FakeJiraClient struct {
	Issues   map[string]*jira.Issue
	Comments map[string][]jira.Comment

	CreatedBody      string
	UpdatedIssueKey  string
	UpdatedCommentID string
	UpdatedBody      string
}

func (f *FakeJiraClient) GetIssue(_ context.Context, issueIDOrKey string) (*jira.Issue, error) {
	issue, ok := f.Issues[issueIDOrKey]
	if !ok {
		return nil, fmt.Errorf("get issue %s: %w", issueIDOrKey, forge.ErrNotFound)
	}
	return issue, nil
}

func (f *FakeJiraClient) ListComments(_ context.Context, issueIDOrKey string) ([]jira.Comment, error) {
	return f.Comments[issueIDOrKey], nil
}

func (f *FakeJiraClient) CreateComment(_ context.Context, issueIDOrKey, body string) (*jira.Comment, error) {
	f.CreatedBody = body
	adf, err := jira.MarkdownToADF(body) // mirrors Jira echoing back the ADF it stored
	if err != nil {
		return nil, err
	}
	comment := jira.Comment{
		ID:      fmt.Sprintf("%d", len(f.Comments[issueIDOrKey])+1),
		Body:    adf,
		Author:  jira.User{DisplayName: "fullsend-bot"},
		Created: "2026-08-06T00:00:00.000+0000",
	}
	if f.Comments == nil {
		f.Comments = make(map[string][]jira.Comment)
	}
	f.Comments[issueIDOrKey] = append(f.Comments[issueIDOrKey], comment)
	return &comment, nil
}

func (f *FakeJiraClient) UpdateComment(_ context.Context, issueIDOrKey, commentID, body string) error {
	f.UpdatedIssueKey = issueIDOrKey
	f.UpdatedCommentID = commentID
	f.UpdatedBody = body
	adf, err := jira.MarkdownToADF(body) // mirrors Jira echoing back the ADF it stored
	if err != nil {
		return err
	}
	for i, c := range f.Comments[issueIDOrKey] {
		if c.ID == commentID {
			f.Comments[issueIDOrKey][i].Body = adf
			break
		}
	}
	return nil
}

var _ jiraClient = (*FakeJiraClient)(nil)

// NewFakeJiraClient returns a tracker.Client backed by an in-memory fake
// that round-trips comment bodies through the real
// jira.MarkdownToADF/ADFToMarkdown conversions, for tests that need to
// exercise Jira-specific markdown quirks (e.g. mdEscaper) without a live
// Jira instance.
func NewFakeJiraClient(baseURL string) (*JiraClient, error) {
	return NewJiraClient(&FakeJiraClient{}, baseURL)
}
