package tracker

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/fullsend-ai/fullsend/internal/forge"
)

// ForgeClient adapts a forge.Client to the tracker.Client interface. It
// works for both the GitHub and GitLab forge.Client implementations, since
// forge.Client already abstracts over the two — this adapter only needs to
// split the tracker's single "project" string back into the owner/repo
// pair that forge.Client expects, and convert forge.IssueComment's numeric
// ID to the string form tracker.Comment uses.
type ForgeClient struct {
	forge forge.Client
}

// NewForgeClient returns a tracker.Client backed by fc.
func NewForgeClient(fc forge.Client) *ForgeClient {
	return &ForgeClient{forge: fc}
}

// GetIssue implements Client.
func (c *ForgeClient) GetIssue(ctx context.Context, project string, number int) (*Issue, error) {
	owner, repo := splitProject(project)
	issue, err := c.forge.GetIssue(ctx, owner, repo, number)
	if err != nil {
		return nil, err
	}
	return &Issue{
		Number: issue.Number,
		Title:  issue.Title,
		Body:   issue.Body,
		URL:    issue.URL,
		Labels: issue.Labels,
	}, nil
}

// ListComments implements Client.
func (c *ForgeClient) ListComments(ctx context.Context, project string, number int) ([]Comment, error) {
	owner, repo := splitProject(project)
	comments, err := c.forge.ListIssueComments(ctx, owner, repo, number)
	if err != nil {
		return nil, err
	}
	result := make([]Comment, len(comments))
	for i, fc := range comments {
		result[i] = fromForgeComment(fc)
	}
	return result, nil
}

// CreateComment implements Client.
func (c *ForgeClient) CreateComment(ctx context.Context, project string, number int, body string) (*Comment, error) {
	owner, repo := splitProject(project)
	comment, err := c.forge.CreateIssueComment(ctx, owner, repo, number, body)
	if err != nil {
		return nil, err
	}
	result := fromForgeComment(*comment)
	return &result, nil
}

// UpdateComment implements Client.
func (c *ForgeClient) UpdateComment(ctx context.Context, project string, commentID string, body string) error {
	owner, repo := splitProject(project)
	id, err := strconv.Atoi(commentID)
	if err != nil {
		return fmt.Errorf("tracker: comment ID %q is not numeric: %w", commentID, err)
	}
	return c.forge.UpdateIssueComment(ctx, owner, repo, id, body)
}

func fromForgeComment(c forge.IssueComment) Comment {
	return Comment{
		ID:        strconv.Itoa(c.ID),
		HTMLURL:   c.HTMLURL,
		Body:      c.Body,
		Author:    c.Author,
		CreatedAt: c.CreatedAt,
	}
}

// splitProject splits "group/subgroup/project" into owner="group/subgroup"
// and repo="project". GitHub projects are always single-level
// ("owner/repo"), which this also handles correctly since there's only one
// "/". GitLab projects may be nested under subgroups, hence splitting on
// the last "/" rather than the first.
func splitProject(project string) (owner, repo string) {
	idx := strings.LastIndex(project, "/")
	if idx < 0 {
		return "", project
	}
	return project[:idx], project[idx+1:]
}
