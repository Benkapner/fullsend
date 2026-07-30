package jirapoll

import (
	"context"
	"fmt"
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

// detectChanges finds all changes on an issue since lastCheck.
func (p *Poller) detectChanges(ctx context.Context, issue jira.Issue, lastCheck time.Time) ([]JiraEvent, error) {
	var events []JiraEvent

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
	}

	// Discover new comments.
	comments, err := p.client.ListComments(ctx, issue.Key)
	if err != nil {
		return nil, fmt.Errorf("list comments for %s: %w", issue.Key, err)
	}
	for _, comment := range comments {
		createdAt, err := parseJiraTimestamp(comment.Created)
		if err != nil {
			continue
		}
		if !lastCheck.IsZero() && !createdAt.After(lastCheck) {
			continue
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
		return nil, fmt.Errorf("list changelog for %s: %w", issue.Key, err)
	}
	for _, entry := range changelog {
		createdAt, err := parseJiraTimestamp(entry.Created)
		if err != nil {
			continue
		}
		if !lastCheck.IsZero() && !createdAt.After(lastCheck) {
			continue
		}
		for _, item := range entry.Items {
			changeEvents := mapChangelogItem(item, issue, entry, issueURL, createdAt)
			events = append(events, changeEvents...)
		}
	}

	return events, nil
}

// mapChangelogItem maps a single changelog item to zero or more JiraEvents.
func mapChangelogItem(item jira.ChangeItem, issue jira.Issue, entry jira.ChangelogEntry, issueURL string, createdAt time.Time) []JiraEvent {
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
		evt.Type = mapStatusTransition(item.ToString)
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

// mapStatusTransition maps a Jira status name to a transition kind using the
// destination status name from the changelog entry. We cannot use the issue's
// current statusCategory because changelog entries are historical — the
// current category only reflects the latest status, not earlier transitions.
func mapStatusTransition(toStatus string) string {
	lower := strings.ToLower(toStatus)
	switch {
	case lower == "done" || lower == "closed" || lower == "resolved" ||
		strings.Contains(lower, "complete"):
		return "closed"
	case strings.Contains(lower, "reopen"):
		return "reopened"
	default:
		return ""
	}
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
