// Package jira implements an HTTP client for the Jira Cloud REST API v3.
package jira

import "encoding/json"

// Issue represents a Jira issue.
type Issue struct {
	ID     string      `json:"id"`
	Key    string      `json:"key"`
	Self   string      `json:"self"`
	Fields IssueFields `json:"fields"`
}

// IssueFields contains the standard fields of a Jira issue.
type IssueFields struct {
	Summary     string       `json:"summary"`
	Description any          `json:"description"` // ADF object or string
	Status      Status       `json:"status"`
	Labels      []string     `json:"labels"`
	Reporter    User         `json:"reporter"`
	Created     string       `json:"created"`
	Updated     string       `json:"updated"`
	Comment     *CommentPage `json:"comment,omitempty"`
}

// Status represents the status of a Jira issue.
type Status struct {
	Name           string         `json:"name"`
	StatusCategory StatusCategory `json:"statusCategory"`
}

// StatusCategory groups statuses into broad categories.
// Key is one of "new", "indeterminate", or "done".
type StatusCategory struct {
	Key string `json:"key"`
}

// Comment represents a single comment on a Jira issue.
type Comment struct {
	ID      string `json:"id"`
	Body    any    `json:"body"` // ADF object or string
	Author  User   `json:"author"`
	Created string `json:"created"`
	Updated string `json:"updated"`
}

// CommentPage is a paginated list of comments.
type CommentPage struct {
	Comments   []Comment `json:"comments"`
	Total      int       `json:"total"`
	MaxResults int       `json:"maxResults"`
	StartAt    int       `json:"startAt"`
}

// ChangelogEntry represents a single changelog entry on a Jira issue.
type ChangelogEntry struct {
	ID      string       `json:"id"`
	Author  User         `json:"author"`
	Created string       `json:"created"`
	Items   []ChangeItem `json:"items"`
}

// ChangeItem describes a single field change within a changelog entry.
type ChangeItem struct {
	Field      string `json:"field"`
	FromString string `json:"fromString"`
	ToString   string `json:"toString"`
}

// User represents a Jira user account.
type User struct {
	AccountID   string `json:"accountId"`
	DisplayName string `json:"displayName"`
	AccountType string `json:"accountType"` // "atlassian", "app", "customer"
	Active      bool   `json:"active"`
}

// SearchResult is the response from the Jira issue search API.
type SearchResult struct {
	Issues     []Issue `json:"issues"`
	Total      int     `json:"total"`
	MaxResults int     `json:"maxResults"`
	StartAt    int     `json:"startAt"`
}

// EntityPropertyValue wraps a JSON value stored as a Jira entity property.
type EntityPropertyValue struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

// ProjectRoleList is the response from GET /rest/api/3/project/{key}/role.
// It maps role names to their URLs.
// Example: {"Administrators": "https://.../role/10002", "Developers": "https://.../role/10001"}
type ProjectRoleList map[string]string

// ProjectRoleDetail is the response from GET /rest/api/3/project/{key}/role/{id}.
type ProjectRoleDetail struct {
	Name   string      `json:"name"`
	Actors []RoleActor `json:"actors"`
}

// RoleActor represents a member of a project role.
type RoleActor struct {
	ID          int            `json:"id"`
	DisplayName string         `json:"displayName"`
	Type        string         `json:"type"` // "atlassian-user-role-actor", "atlassian-group-role-actor"
	ActorUser   *RoleActorUser `json:"actorUser,omitempty"`
}

// RoleActorUser contains the account ID of a role actor.
type RoleActorUser struct {
	AccountID string `json:"accountId"`
}

// changelogPage is the paginated response from the changelog API.
type changelogPage struct {
	Values     []ChangelogEntry `json:"values"`
	Total      int              `json:"total"`
	MaxResults int              `json:"maxResults"`
	StartAt    int              `json:"startAt"`
	IsLast     bool             `json:"isLast"`
}
