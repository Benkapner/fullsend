package jirapoll

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/fullsend-ai/fullsend/internal/forge/jira"
)

// jiraTimestampFormats lists the timestamp formats used by Jira Cloud and Server/DC.
var jiraTimestampFormats = []string{
	"2006-01-02T15:04:05.000-0700",
	"2006-01-02T15:04:05.000+0000",
	"2006-01-02T15:04:05.000Z",
	time.RFC3339,
}

// parseJiraTimestamp parses a Jira timestamp string.
func parseJiraTimestamp(s string) (time.Time, error) {
	for _, format := range jiraTimestampFormats {
		if t, err := time.Parse(format, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unparseable Jira timestamp: %q", s)
}

// changeResult bundles the events detected by detectChanges together with the
// latest observed timestamp across all comments and changelog entries,
// regardless of whether they produced routable events.
type changeResult struct {
	events  []JiraEvent
	maxSeen time.Time
}

// detectChanges finds all changes on an issue since lastCheck.
// maxSeen in the returned changeResult tracks the latest timestamp across all
// inspected entries — including unsupported changelog fields — so the caller
// can advance the checkpoint even when no routable events are produced.
func (p *Poller) detectChanges(ctx context.Context, issue jira.Issue, lastCheck time.Time) (changeResult, error) {
	var events []JiraEvent
	var maxSeen time.Time

	issueURL := strings.TrimRight(p.opts.JiraBaseURL, "/") + "/browse/" + issue.Key

	// If lastCheck is zero, this is the first poll for this issue: emit "opened".
	if lastCheck.IsZero() {
		createdAt, err := parseJiraTimestamp(issue.Fields.Created)
		if err != nil {
			createdAt = time.Now()
		}
		events = append(events, JiraEvent{
			Type:      "opened",
			IssueID:   issue.ID,
			IssueKey:  issue.Key,
			IssueURL:  issueURL,
			UpdatedAt: createdAt,
			Labels:    issue.Fields.Labels,
			Reporter:  issue.Fields.Reporter,
		})
		if createdAt.After(maxSeen) {
			maxSeen = createdAt
		}
	}

	// Discover new comments.
	comments, err := p.client.ListComments(ctx, issue.Key)
	if err != nil {
		return changeResult{}, fmt.Errorf("list comments for %s: %w", issue.Key, err)
	}
	for _, comment := range comments {
		createdAt, err := parseJiraTimestamp(comment.Created)
		if err != nil {
			continue
		}
		if !lastCheck.IsZero() && !createdAt.After(lastCheck) {
			continue
		}
		if createdAt.After(maxSeen) {
			maxSeen = createdAt
		}
		events = append(events, JiraEvent{
			Type:          "comment_added",
			IssueID:       issue.ID,
			IssueKey:      issue.Key,
			IssueURL:      issueURL,
			UpdatedAt:     createdAt,
			Labels:        issue.Fields.Labels,
			CommentID:     comment.ID,
			CommentBody:   extractPlainText(comment.Body),
			CommentAuthor: comment.Author,
			Reporter:      issue.Fields.Reporter,
		})
	}

	// Discover changelog entries.
	changelog, err := p.client.ListChangelog(ctx, issue.Key)
	if err != nil {
		return changeResult{}, fmt.Errorf("list changelog for %s: %w", issue.Key, err)
	}
	for _, entry := range changelog {
		createdAt, err := parseJiraTimestamp(entry.Created)
		if err != nil {
			continue
		}
		if !lastCheck.IsZero() && !createdAt.After(lastCheck) {
			continue
		}
		// Track maxSeen for ALL changelog entries that pass the time filter,
		// even if the field type is unsupported. This prevents the poller from
		// stalling when Jira has changes (e.g., assignee) that don't map to
		// routable events.
		if createdAt.After(maxSeen) {
			maxSeen = createdAt
		}
		for _, item := range entry.Items {
			changeEvents := p.mapChangelogItem(ctx, item, issue, entry, issueURL, createdAt)
			events = append(events, changeEvents...)
		}
	}

	return changeResult{events: events, maxSeen: maxSeen}, nil
}

// mapChangelogItem maps a single changelog item to zero or more JiraEvents.
func (p *Poller) mapChangelogItem(ctx context.Context, item jira.ChangeItem, issue jira.Issue, entry jira.ChangelogEntry, issueURL string, createdAt time.Time) []JiraEvent {
	var events []JiraEvent

	base := JiraEvent{
		IssueID:      issue.ID,
		IssueKey:     issue.Key,
		IssueURL:     issueURL,
		UpdatedAt:    createdAt,
		Labels:       issue.Fields.Labels,
		ChangeAuthor: entry.Author,
		Reporter:     issue.Fields.Reporter,
	}

	switch item.Field {
	case "labels":
		labelEvents := diffLabels(item.FromString, item.ToString, base)
		events = append(events, labelEvents...)

	case "status":
		evt := base
		evt.Type = p.mapStatusTransition(ctx, item.FromString, item.ToString)
		if evt.Type != "" {
			events = append(events, evt)
		}

	case "summary", "description":
		evt := base
		evt.Type = "edited"
		events = append(events, evt)
	}

	return events
}

// diffLabels parses Jira label changelog strings and returns label_changed events.
// Jira stores labels as space-separated strings in fromString/toString.
func diffLabels(from, to string, base JiraEvent) []JiraEvent {
	fromSet := parseLabels(from)
	toSet := parseLabels(to)

	var events []JiraEvent

	// Labels added.
	for label := range toSet {
		if !fromSet[label] {
			evt := base
			evt.Type = "label_changed"
			evt.ChangedLabel = label
			evt.LabelAction = "added"
			events = append(events, evt)
		}
	}

	// Labels removed.
	for label := range fromSet {
		if !toSet[label] {
			evt := base
			evt.Type = "label_changed"
			evt.ChangedLabel = label
			evt.LabelAction = "removed"
			events = append(events, evt)
		}
	}

	return events
}

// parseLabels splits a Jira label string into a set.
func parseLabels(s string) map[string]bool {
	m := make(map[string]bool)
	for _, l := range strings.Fields(s) {
		if l != "" {
			m[l] = true
		}
	}
	return m
}

// statusCategory resolves and caches the statusCategory key ("new",
// "indeterminate", "done") for a Jira status name, making at most one
// GetStatus call per unique status name per poll cycle. The cache is reset
// at the start of each Run.
func (p *Poller) statusCategory(ctx context.Context, statusName string) (string, error) {
	if statusName == "" {
		return "", nil
	}
	if cat, ok := p.statusCategoryCache[statusName]; ok {
		return cat, nil
	}
	status, err := p.client.GetStatus(ctx, statusName)
	if err != nil {
		return "", err
	}
	cat := status.StatusCategory.Key
	if p.statusCategoryCache == nil {
		p.statusCategoryCache = make(map[string]string)
	}
	p.statusCategoryCache[statusName] = cat
	return cat, nil
}

// mapStatusTransition classifies a status transition as "closed" or
// "reopened" by resolving the statusCategory of the destination status (and,
// when needed, the origin status) rather than string-matching status names.
// Jira status names are fully customizable per project/locale, so matching
// on name substrings silently misses non-English or custom workflow names;
// statusCategory is a fixed, Jira-defined enum ("new", "indeterminate",
// "done") independent of naming. We cannot use the issue's current
// statusCategory field for this because changelog entries are historical —
// the current category only reflects the latest status, not earlier
// transitions — so we resolve the category for the specific from/to status
// names recorded on the changelog entry.
func (p *Poller) mapStatusTransition(ctx context.Context, fromStatus, toStatus string) string {
	toCat, err := p.statusCategory(ctx, toStatus)
	if err != nil {
		log.Printf("WARNING: resolving statusCategory for %q: %v", toStatus, err)
		return ""
	}
	if toCat == "done" {
		return "closed"
	}

	fromCat, err := p.statusCategory(ctx, fromStatus)
	if err != nil {
		log.Printf("WARNING: resolving statusCategory for %q: %v", fromStatus, err)
		return ""
	}
	if fromCat == "done" {
		return "reopened"
	}
	return ""
}

// extractPlainText extracts plain text from a Jira comment body.
// Handles both ADF (map[string]any) and plain string formats.
func extractPlainText(body any) string {
	switch v := body.(type) {
	case string:
		return v
	case map[string]any:
		return extractADFText(v)
	default:
		return fmt.Sprintf("%v", body)
	}
}

// extractADFText walks an ADF document and concatenates text content.
func extractADFText(node map[string]any) string {
	var sb strings.Builder
	walkADFNode(node, &sb)
	return sb.String()
}

// walkADFNode recursively walks ADF nodes, extracting text.
func walkADFNode(node map[string]any, sb *strings.Builder) {
	if text, ok := node["text"].(string); ok {
		sb.WriteString(text)
	}

	// Certain block types need newlines between them.
	nodeType, _ := node["type"].(string)

	content, ok := node["content"].([]any)
	if !ok {
		return
	}

	for i, child := range content {
		childMap, ok := child.(map[string]any)
		if !ok {
			continue
		}
		walkADFNode(childMap, sb)

		// Add newline after paragraph/heading blocks (except the last one).
		childType, _ := childMap["type"].(string)
		if i < len(content)-1 && isBlockType(childType) && isBlockType(nodeType) {
			sb.WriteString("\n")
		}
	}
}

// isBlockType returns true for ADF block-level types that should be
// separated by newlines.
func isBlockType(nodeType string) bool {
	switch nodeType {
	case "doc", "paragraph", "heading", "blockquote", "codeBlock",
		"bulletList", "orderedList", "listItem", "panel", "rule":
		return true
	default:
		return false
	}
}
