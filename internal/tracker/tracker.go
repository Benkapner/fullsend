// Package tracker defines a narrow, forge-agnostic interface for reading
// and writing issue content (title, body, comments), keyed by
// (project string, number int) rather than the (owner, repo string, number
// int) shape used by forge.Client.
//
// forge.Client already covers this surface for GitHub and GitLab, but it
// stays scoped to git-hosting operations — Jira is explicitly not a forge
// (it has no branches, pull requests, or CI). Keying by a single project
// string lets a future Jira implementation use its natural issue key
// (PROJECT-123) instead of forcing an owner/repo split that Jira doesn't
// have.
//
// This package only defines the interface and thin adapters over
// forge.Client (see ForgeClient). Nothing calls tracker.Client yet.
package tracker

import "context"

// Issue represents an issue's content, independent of the tracker backend.
type Issue struct {
	Number int
	Title  string
	Body   string
	URL    string
	Labels []string
}

// Comment represents a comment on an issue.
//
// ID is a string rather than an int because not every tracker uses numeric
// comment IDs. Callers that need to update or delete a comment pass the ID
// back verbatim via UpdateComment.
type Comment struct {
	ID        string
	HTMLURL   string
	Body      string
	Author    string
	CreatedAt string
}

// Client abstracts issue-content read/write operations across trackers
// (GitHub, GitLab, and eventually Jira). Project identifies the issue's
// container: "owner/repo" for GitHub/GitLab, a Jira project key for Jira.
type Client interface {
	GetIssue(ctx context.Context, project string, number int) (*Issue, error)
	ListComments(ctx context.Context, project string, number int) ([]Comment, error)
	CreateComment(ctx context.Context, project string, number int, body string) (*Comment, error)
	UpdateComment(ctx context.Context, project string, commentID string, body string) error
}
