package jirapoll

import (
	"strconv"
	"strings"

	"github.com/fullsend-ai/fullsend/internal/dispatch"
)

// toNormalizedEvent converts a JiraEvent to a dispatch.NormalizedEvent
// per the jira-poll-adapter.md spec.
func (p *Poller) toNormalizedEvent(event JiraEvent) dispatch.NormalizedEvent {
	ne := dispatch.NormalizedEvent{
		Repo: p.opts.TargetRepo,
		Source: dispatch.Source{
			System:    "jira",
			RawType:   mapRawType(event),
			RawAction: mapRawAction(event),
		},
		Entity: dispatch.Entity{
			Kind: "work_item",
			ID:   parseIssueID(event.IssueID),
			Key:  event.IssueKey,
			URL:  event.IssueURL,
		},
		Transition: dispatch.Transition{
			Kind: event.Type,
		},
		Actor: dispatch.Actor{
			ID:             actorID(event),
			Kind:           actorKind(event),
			Role:           p.resolveRole(event),
			IsEntityAuthor: isEntityAuthor(event),
		},
		State: dispatch.State{
			Labels: event.Labels,
		},
	}

	switch event.Type {
	case "comment_added":
		cmd, instruction := extractCommand(event.CommentBody)
		ne.Transition.Comment = &dispatch.TransitionComment{
			Body:        truncate(event.CommentBody, 4096),
			Command:     cmd,
			Instruction: truncate(instruction, 4096),
		}
	case "label_changed":
		ne.Transition.Label = &dispatch.TransitionLabel{
			Name:   event.ChangedLabel,
			Action: event.LabelAction,
		}
	}

	return ne
}

// mapRawType maps event type to the Jira source.raw_type.
func mapRawType(event JiraEvent) string {
	switch event.Type {
	case "comment_added":
		return "comment"
	case "label_changed", "edited", "reopened", "closed":
		return "changelog"
	case "opened":
		return "issue"
	default:
		return "issue"
	}
}

// mapRawAction maps event type to the Jira source.raw_action.
func mapRawAction(event JiraEvent) string {
	switch event.Type {
	case "comment_added", "opened":
		return "created"
	case "label_changed", "edited", "reopened", "closed":
		return "updated"
	default:
		return ""
	}
}

// parseIssueID converts a Jira issue ID string to int.
func parseIssueID(id string) int {
	n, _ := strconv.Atoi(id)
	return n
}

// actorID returns the actor identifier for the event.
func actorID(event JiraEvent) string {
	switch event.Type {
	case "comment_added":
		return event.CommentAuthor.AccountID
	case "label_changed", "edited", "reopened", "closed":
		return event.ChangeAuthor.AccountID
	case "opened":
		return event.Reporter.AccountID
	default:
		return ""
	}
}

// actorKind returns "bot" or "human" based on the actor's account type.
func actorKind(event JiraEvent) string {
	var accountType string
	switch event.Type {
	case "comment_added":
		accountType = event.CommentAuthor.AccountType
	case "label_changed", "edited", "reopened", "closed":
		accountType = event.ChangeAuthor.AccountType
	case "opened":
		accountType = event.Reporter.AccountType
	}
	if accountType == "app" {
		return "bot"
	}
	return "human"
}

// isEntityAuthor checks if the actor is the issue reporter.
func isEntityAuthor(event JiraEvent) bool {
	aid := actorID(event)
	if aid == "" {
		return false
	}
	return aid == event.Reporter.AccountID
}

// extractCommand parses a comment body for a slash command.
// Returns the command token and the remaining instruction text.
func extractCommand(body string) (command, instruction string) {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return "", ""
	}

	firstLine := strings.SplitN(trimmed, "\n", 2)[0]
	tokens := strings.Fields(firstLine)
	if len(tokens) == 0 {
		return "", ""
	}

	if !strings.HasPrefix(tokens[0], "/") {
		return "", ""
	}

	command = tokens[0]
	if len(tokens) > 1 {
		return command, strings.Join(tokens[1:], " ")
	}
	return command, ""
}

// resolveRole maps the event actor's Jira project role to an ADR 0054 role.
func (p *Poller) resolveRole(event JiraEvent) string {
	aid := actorID(event)
	if aid == "" {
		return "external"
	}
	roleName, ok := p.roleMembership[aid]
	if !ok {
		return "external"
	}
	return mapJiraRole(roleName)
}

// mapJiraRole maps a Jira project role name to an ADR 0054 role.
func mapJiraRole(roleName string) string {
	switch strings.ToLower(roleName) {
	case "administrators":
		return "admin"
	case "developers":
		return "write"
	default:
		return "read"
	}
}

// truncate truncates a string to maxLen runes.
func truncate(s string, maxLen int) string {
	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}
	return string(r[:maxLen])
}
