package jira

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fullsend-ai/fullsend/internal/forge"
)

// LiveClient is an HTTP client for the Jira Cloud REST API v3.
type LiveClient struct {
	httpClient *http.Client
	baseURL    string
	email      string // for Basic auth (Cloud)
	token      string

	groupMemberCacheMu sync.Mutex
	groupMemberCache   map[string]groupMemberCacheEntry
}

// groupMemberCacheTTL bounds how long a group's resolved member list is
// reused across GetProjectRoleMembership calls, so that a poll cycle
// touching many issues in the same project doesn't re-fetch group
// membership from the group/member endpoint for every issue.
const groupMemberCacheTTL = 5 * time.Minute

type groupMemberCacheEntry struct {
	accountIDs []string
	fetchedAt  time.Time
}

// Option configures the Jira client.
type Option func(*LiveClient)

// WithBaseURL sets a custom base URL for the Jira instance.
func WithBaseURL(rawURL string) Option {
	return func(c *LiveClient) {
		c.baseURL = strings.TrimRight(rawURL, "/")
	}
}

// WithEmail sets the email address for Basic auth (Jira Cloud).
// When set, the client uses Authorization: Basic base64(email:token).
// When empty, the client uses Authorization: Bearer token (Data Center/PAT).
func WithEmail(email string) Option {
	return func(c *LiveClient) {
		c.email = email
	}
}

// WithHTTPClient sets a custom HTTP client for API calls.
func WithHTTPClient(client *http.Client) Option {
	return func(c *LiveClient) {
		c.httpClient = client
	}
}

// validateBaseURL checks that the base URL uses https, unless it points to a
// loopback address (for httptest servers).
func validateBaseURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid base URL: %w", err)
	}
	if u.Scheme == "https" {
		return nil
	}
	host := u.Hostname()
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return nil
	}
	return fmt.Errorf("base URL %q uses insecure scheme %q; only https is allowed for non-loopback hosts", rawURL, u.Scheme)
}

// New creates a new Jira client with the given API token.
func New(token string, opts ...Option) (*LiveClient, error) {
	if token == "" {
		return nil, fmt.Errorf("jira: token must not be empty")
	}
	c := &LiveClient{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return fmt.Errorf("stopped after 10 redirects")
				}
				if len(via) > 0 {
					prev := via[len(via)-1]
					crossOrigin := req.URL.Host != prev.URL.Host
					tlsDowngrade := prev.URL.Scheme == "https" && req.URL.Scheme != "https"
					if crossOrigin || tlsDowngrade {
						req.Header.Del("Authorization")
					}
				}
				return nil
			},
		},
		token: token,
	}
	for _, o := range opts {
		o(c)
	}
	if c.baseURL == "" {
		return nil, fmt.Errorf("jira: base URL must be set via WithBaseURL")
	}
	if err := validateBaseURL(c.baseURL); err != nil {
		return nil, err
	}
	return c, nil
}

// APIError represents an error response from the Jira API.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("jira api: %d %s", e.StatusCode, e.Message)
}

func (e *APIError) Unwrap() error {
	if e.StatusCode == http.StatusNotFound {
		return forge.ErrNotFound
	}
	if e.StatusCode == http.StatusForbidden {
		return forge.ErrForbidden
	}
	return nil
}

const maxRetries = 5

func (c *LiveClient) apiURL(path string) string {
	return c.baseURL + "/rest/api/3" + path
}

func (c *LiveClient) setAuth(req *http.Request) {
	if c.email != "" {
		cred := base64.StdEncoding.EncodeToString([]byte(c.email + ":" + c.token))
		req.Header.Set("Authorization", "Basic "+cred)
	} else {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}

// do executes an HTTP request with auth, error handling, and retry with backoff.
func (c *LiveClient) do(ctx context.Context, method, path string, body io.Reader, result any) error {
	reqURL := c.apiURL(path)

	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = io.ReadAll(body)
		if err != nil {
			return fmt.Errorf("read request body: %w", err)
		}
	}

	for attempt := range maxRetries {
		var reqBody io.Reader
		if bodyBytes != nil {
			reqBody = bytes.NewReader(bodyBytes)
		}

		req, err := http.NewRequestWithContext(ctx, method, reqURL, reqBody)
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}

		c.setAuth(req)
		req.Header.Set("Accept", "application/json")
		if bodyBytes != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			if isTransientError(err) && isIdempotent(method, path) && attempt < maxRetries-1 {
				delay := retryDelay(nil, attempt)
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					return ctx.Err()
				}
				continue
			}
			return fmt.Errorf("http %s %s: %w", method, path, err)
		}

		if isRetryable(resp, method, path) {
			_ = resp.Body.Close()
			if attempt == maxRetries-1 {
				return &APIError{
					StatusCode: resp.StatusCode,
					Message:    fmt.Sprintf("retryable error after %d attempts on %s %s", maxRetries, method, path),
				}
			}
			delay := retryDelay(resp, attempt)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			}
			continue
		}

		// For responses with no expected body (204, 200/201 with no result target).
		if result == nil {
			defer resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
			return parseErrorResponse(resp)
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			defer resp.Body.Close()
			return json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(result)
		}

		return parseErrorResponse(resp)
	}

	return fmt.Errorf("exhausted retries for %s %s", method, path)
}

// parseErrorResponse reads a Jira error response and returns an APIError.
func parseErrorResponse(resp *http.Response) error {
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))

	var errResp struct {
		ErrorMessages []string          `json:"errorMessages"`
		Errors        map[string]string `json:"errors"`
	}
	if json.Unmarshal(data, &errResp) == nil {
		var parts []string
		parts = append(parts, errResp.ErrorMessages...)
		for k, v := range errResp.Errors {
			parts = append(parts, fmt.Sprintf("%s: %s", k, v))
		}
		if len(parts) > 0 {
			return &APIError{StatusCode: resp.StatusCode, Message: strings.Join(parts, "; ")}
		}
	}
	return &APIError{StatusCode: resp.StatusCode, Message: http.StatusText(resp.StatusCode)}
}

func isRetryable(resp *http.Response, method, path string) bool {
	if resp.StatusCode == http.StatusTooManyRequests {
		return true
	}
	if resp.StatusCode >= 500 && resp.StatusCode <= 504 && isIdempotent(method, path) {
		return true
	}
	return false
}

func isTransientError(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	return false
}

func isIdempotent(method, path string) bool {
	if method == http.MethodGet || method == http.MethodHead ||
		method == http.MethodPut || method == http.MethodDelete {
		return true
	}
	// /search/jql is a read-only JQL search issued as a POST because the
	// query is passed in the request body; it's safe to retry like a GET.
	return method == http.MethodPost && path == "/search/jql"
}

func retryDelay(resp *http.Response, attempt int) time.Duration {
	const maxRetryAfterSecs = 300
	if resp != nil {
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if secs, err := strconv.Atoi(ra); err == nil && secs > 0 {
				if secs > maxRetryAfterSecs {
					secs = maxRetryAfterSecs
				}
				return time.Duration(secs) * time.Second
			}
		}
	}
	base := time.Duration(math.Pow(2, float64(attempt))) * time.Second
	half := base / 2
	return half + time.Duration(rand.Int64N(int64(half)+1))
}

// ---------------------------------------------------------------------------
// API methods
// ---------------------------------------------------------------------------

// searchRequest is the POST body for /rest/api/3/search/jql.
type searchRequest struct {
	JQL           string   `json:"jql"`
	MaxResults    int      `json:"maxResults"`
	Fields        []string `json:"fields"`
	Expand        string   `json:"expand,omitempty"`
	NextPageToken string   `json:"nextPageToken,omitempty"`
}

// maxSearchPages limits pagination to prevent unbounded memory growth
// from overly broad JQL queries.
const maxSearchPages = 200

// SearchIssues executes a JQL search and returns up to limit matching
// issues, using the POST /rest/api/3/search/jql endpoint with cursor-based
// pagination (nextPageToken + isLast). Pagination stops as soon as limit
// issues have been collected, so a limit smaller than the full match set
// bounds API cost rather than fetching everything and truncating
// afterward. A limit <= 0 fetches all matching issues, capped at
// maxSearchPages pages (10,000 issues at 50 per page) to prevent unbounded
// memory growth.
func (c *LiveClient) SearchIssues(ctx context.Context, jql string, limit int) ([]Issue, error) {
	var all []Issue
	var nextPageToken string
	for page := 0; page < maxSearchPages; page++ {
		body := searchRequest{
			JQL:           jql,
			MaxResults:    50,
			Fields:        []string{"*all"},
			Expand:        "changelog",
			NextPageToken: nextPageToken,
		}
		bodyJSON, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal search request: %w", err)
		}
		var result SearchResult
		if err := c.do(ctx, http.MethodPost, "/search/jql", bytes.NewReader(bodyJSON), &result); err != nil {
			return nil, fmt.Errorf("search issues: %w", err)
		}
		all = append(all, result.Issues...)
		if limit > 0 && len(all) >= limit {
			all = all[:limit]
			break
		}
		if result.IsLast || len(result.Issues) == 0 || result.NextPageToken == "" {
			break
		}
		nextPageToken = result.NextPageToken
	}
	return all, nil
}

// GetIssue fetches a single issue by ID or key.
func (c *LiveClient) GetIssue(ctx context.Context, issueIDOrKey string) (*Issue, error) {
	var issue Issue
	path := "/issue/" + url.PathEscape(issueIDOrKey)
	if err := c.do(ctx, http.MethodGet, path, nil, &issue); err != nil {
		return nil, fmt.Errorf("get issue %s: %w", issueIDOrKey, err)
	}
	return &issue, nil
}

// GetStatus fetches a single status by ID or name, including its
// statusCategory. Used to classify changelog status transitions by category
// rather than by matching locale/workflow-specific status name substrings.
func (c *LiveClient) GetStatus(ctx context.Context, idOrName string) (*Status, error) {
	var status Status
	path := "/status/" + url.PathEscape(idOrName)
	if err := c.do(ctx, http.MethodGet, path, nil, &status); err != nil {
		return nil, fmt.Errorf("get status %s: %w", idOrName, err)
	}
	return &status, nil
}

// ListComments fetches all comments on an issue, exhausting pagination.
func (c *LiveClient) ListComments(ctx context.Context, issueIDOrKey string) ([]Comment, error) {
	var all []Comment
	startAt := 0
	for {
		path := fmt.Sprintf("/issue/%s/comment?orderBy=created&maxResults=100&startAt=%d",
			url.PathEscape(issueIDOrKey), startAt)
		var page CommentPage
		if err := c.do(ctx, http.MethodGet, path, nil, &page); err != nil {
			return nil, fmt.Errorf("list comments for %s (startAt=%d): %w", issueIDOrKey, startAt, err)
		}
		all = append(all, page.Comments...)
		if startAt+len(page.Comments) >= page.Total || len(page.Comments) == 0 {
			break
		}
		startAt += len(page.Comments)
	}
	return all, nil
}

// ListChangelog fetches all changelog entries for an issue, exhausting pagination.
func (c *LiveClient) ListChangelog(ctx context.Context, issueIDOrKey string) ([]ChangelogEntry, error) {
	var all []ChangelogEntry
	startAt := 0
	for {
		path := fmt.Sprintf("/issue/%s/changelog?maxResults=100&startAt=%d",
			url.PathEscape(issueIDOrKey), startAt)
		var page changelogPage
		if err := c.do(ctx, http.MethodGet, path, nil, &page); err != nil {
			return nil, fmt.Errorf("list changelog for %s (startAt=%d): %w", issueIDOrKey, startAt, err)
		}
		all = append(all, page.Values...)
		if page.IsLast || len(page.Values) == 0 {
			break
		}
		startAt += len(page.Values)
	}
	return all, nil
}

// GetEntityProperty retrieves the value of an entity property on an issue.
// Returns forge.ErrNotFound (wrapped) if the property does not exist.
func (c *LiveClient) GetEntityProperty(ctx context.Context, issueIDOrKey, propertyKey string) (json.RawMessage, error) {
	path := fmt.Sprintf("/issue/%s/properties/%s",
		url.PathEscape(issueIDOrKey), url.PathEscape(propertyKey))
	var prop EntityPropertyValue
	if err := c.do(ctx, http.MethodGet, path, nil, &prop); err != nil {
		return nil, fmt.Errorf("get entity property %s on %s: %w", propertyKey, issueIDOrKey, err)
	}
	return prop.Value, nil
}

// SetEntityProperty sets (creates or updates) an entity property on an issue.
func (c *LiveClient) SetEntityProperty(ctx context.Context, issueIDOrKey, propertyKey string, value any) error {
	path := fmt.Sprintf("/issue/%s/properties/%s",
		url.PathEscape(issueIDOrKey), url.PathEscape(propertyKey))
	body, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal entity property value: %w", err)
	}
	if err := c.do(ctx, http.MethodPut, path, bytes.NewReader(body), nil); err != nil {
		return fmt.Errorf("set entity property %s on %s: %w", propertyKey, issueIDOrKey, err)
	}
	return nil
}

// DeleteEntityProperty removes an entity property from an issue.
func (c *LiveClient) DeleteEntityProperty(ctx context.Context, issueIDOrKey, propertyKey string) error {
	path := fmt.Sprintf("/issue/%s/properties/%s",
		url.PathEscape(issueIDOrKey), url.PathEscape(propertyKey))
	if err := c.do(ctx, http.MethodDelete, path, nil, nil); err != nil {
		return fmt.Errorf("delete entity property %s on %s: %w", propertyKey, issueIDOrKey, err)
	}
	return nil
}

// GetProjectRoleMembership returns a map of accountID → role name for all
// members of the given Jira project. Role names are the Jira project role
// names (e.g. "Administrators", "Developers"). When a user appears in
// multiple roles, the highest-priority role wins (Administrators > Developers
// > everything else).
func (c *LiveClient) GetProjectRoleMembership(ctx context.Context, projectKey string) (map[string]string, error) {
	path := fmt.Sprintf("/project/%s/role", url.PathEscape(projectKey))
	var roleList ProjectRoleList
	if err := c.do(ctx, http.MethodGet, path, nil, &roleList); err != nil {
		return nil, fmt.Errorf("list project roles for %s: %w", projectKey, err)
	}

	membership := make(map[string]string)
	for roleName, roleURL := range roleList {
		// Extract role ID from URL (last path segment).
		idx := strings.LastIndex(roleURL, "/")
		if idx < 0 || idx == len(roleURL)-1 {
			continue
		}
		roleID := roleURL[idx+1:]

		detailPath := fmt.Sprintf("/project/%s/role/%s",
			url.PathEscape(projectKey), url.PathEscape(roleID))
		var detail ProjectRoleDetail
		if err := c.do(ctx, http.MethodGet, detailPath, nil, &detail); err != nil {
			return nil, fmt.Errorf("get project role %s (id %s): %w", roleName, roleID, err)
		}

		for _, actor := range detail.Actors {
			var aids []string
			switch {
			case actor.Type == "atlassian-group-role-actor":
				if actor.ActorGroup == nil || actor.ActorGroup.GroupID == "" {
					continue
				}
				members, err := c.groupMembers(ctx, actor.ActorGroup.GroupID)
				if err != nil {
					return nil, fmt.Errorf("list members of group %s (role %s): %w", actor.ActorGroup.Name, roleName, err)
				}
				aids = members
			case actor.ActorUser != nil && actor.ActorUser.AccountID != "":
				aids = []string{actor.ActorUser.AccountID}
			default:
				continue
			}
			for _, aid := range aids {
				existing, ok := membership[aid]
				if !ok || rolePriority(roleName) > rolePriority(existing) {
					membership[aid] = roleName
				}
			}
		}
	}
	return membership, nil
}

// groupMembers returns the account IDs of all members of the given Jira
// group, exhausting pagination. Results are cached for groupMemberCacheTTL
// to avoid repeatedly re-fetching membership for the same group within a
// single poll cycle (or across issues in the same project).
func (c *LiveClient) groupMembers(ctx context.Context, groupID string) ([]string, error) {
	c.groupMemberCacheMu.Lock()
	entry, ok := c.groupMemberCache[groupID]
	c.groupMemberCacheMu.Unlock()
	if ok && time.Since(entry.fetchedAt) < groupMemberCacheTTL {
		return entry.accountIDs, nil
	}

	var accountIDs []string
	startAt := 0
	for {
		path := fmt.Sprintf("/group/member?groupId=%s&maxResults=100&startAt=%d",
			url.QueryEscape(groupID), startAt)
		var page groupMemberPage
		if err := c.do(ctx, http.MethodGet, path, nil, &page); err != nil {
			return nil, fmt.Errorf("list group members for %s (startAt=%d): %w", groupID, startAt, err)
		}
		for _, member := range page.Values {
			if member.AccountID != "" {
				accountIDs = append(accountIDs, member.AccountID)
			}
		}
		if page.IsLast || len(page.Values) == 0 {
			break
		}
		startAt += len(page.Values)
	}

	c.groupMemberCacheMu.Lock()
	if c.groupMemberCache == nil {
		c.groupMemberCache = make(map[string]groupMemberCacheEntry)
	}
	c.groupMemberCache[groupID] = groupMemberCacheEntry{accountIDs: accountIDs, fetchedAt: time.Now()}
	c.groupMemberCacheMu.Unlock()

	return accountIDs, nil
}

// rolePriority returns the priority of a Jira project role name.
// Higher values take precedence when a user appears in multiple roles.
//
// KNOWN LIMITATION (intentional for the MVP): matches by role name, not by
// the project's permission scheme. See mapJiraRole in internal/jirapoll and
// docs/guides/user/jira-integration.md#actor-role-resolution.
func rolePriority(roleName string) int {
	switch strings.ToLower(roleName) {
	case "administrators":
		return 2
	case "developers":
		return 1
	default:
		return 0
	}
}

// GetMyself returns the currently authenticated user.
func (c *LiveClient) GetMyself(ctx context.Context) (*User, error) {
	var user User
	if err := c.do(ctx, http.MethodGet, "/myself", nil, &user); err != nil {
		return nil, fmt.Errorf("get myself: %w", err)
	}
	return &user, nil
}
