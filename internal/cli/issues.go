package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fullsend-ai/fullsend/internal/sticky"
	"github.com/fullsend-ai/fullsend/internal/tracker"
	"github.com/fullsend-ai/fullsend/internal/ui"
)

func newIssuesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "issues",
		Short: "Read and write issue content across trackers",
		Long: `Commands for reading and writing issue content (title, body,
comments, labels) across GitHub, GitLab, and Jira.

Use --tracker to select the tracker backend. For GitHub and GitLab,
--project is "owner/repo". For Jira, --project is the project key
(e.g. "PROJ") and the issue is addressed as PROJ-<number>.`,
	}
	cmd.AddCommand(newIssuesGetCmd())
	cmd.AddCommand(newIssuesPostCommentCmd())
	return cmd
}

// issueGetResult is the JSON output of "fullsend issues get".
type issueGetResult struct {
	Number   int                     `json:"number"`
	Title    string                  `json:"title"`
	Body     string                  `json:"body"`
	URL      string                  `json:"url"`
	Labels   []string                `json:"labels"`
	Comments []issueCommentGetResult `json:"comments"`
}

type issueCommentGetResult struct {
	ID        string `json:"id"`
	Author    string `json:"author"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
	HTMLURL   string `json:"html_url,omitempty"`
}

func newIssuesGetCmd() *cobra.Command {
	var (
		trackerName string
		project     string
		number      int
		token       string
		jiraURL     string
		jiraEmail   string
	)

	cmd := &cobra.Command{
		Use:   "get",
		Short: "Read issue content from a tracker",
		Long: `Reads an issue's title, body, labels, and comments from the
specified tracker (GitHub, GitLab, or Jira) and prints them as JSON.

For GitHub/GitLab, --project is "owner/repo". For Jira, --project is
the Jira project key (e.g. "PROJ") and the issue number maps to
PROJ-<number>.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			tc, err := newTrackerClient(trackerName, token, jiraURL, jiraEmail)
			if err != nil {
				return err
			}
			ctx := cmd.Context()

			issue, err := tc.GetIssue(ctx, project, number)
			if err != nil {
				return fmt.Errorf("getting issue: %w", err)
			}

			comments, err := tc.ListComments(ctx, project, number)
			if err != nil {
				return fmt.Errorf("listing comments: %w", err)
			}

			result := issueGetResult{
				Number:   issue.Number,
				Title:    issue.Title,
				Body:     string(issue.Body),
				URL:      issue.URL,
				Labels:   issue.Labels,
				Comments: make([]issueCommentGetResult, len(comments)),
			}
			for i, c := range comments {
				result.Comments[i] = issueCommentGetResult{
					ID:        c.ID,
					Author:    c.Author,
					Body:      string(c.Body),
					CreatedAt: c.CreatedAt,
					HTMLURL:   c.HTMLURL,
				}
			}

			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(result)
		},
	}

	cmd.Flags().StringVar(&trackerName, "tracker", "", "tracker backend: github, gitlab, or jira (required)")
	cmd.Flags().StringVar(&project, "project", "", "project identifier: owner/repo (GitHub/GitLab) or project key (Jira) (required)")
	cmd.Flags().IntVar(&number, "number", 0, "issue number (required)")
	cmd.Flags().StringVar(&token, "token", "", "API token (default: env var per tracker)")
	cmd.Flags().StringVar(&jiraURL, "jira-url", "", "Jira instance URL (default: $JIRA_BASE_URL)")
	cmd.Flags().StringVar(&jiraEmail, "jira-email", "", "Jira user email for Basic auth (default: $JIRA_USER_EMAIL)")
	_ = cmd.MarkFlagRequired("tracker")
	_ = cmd.MarkFlagRequired("project")
	_ = cmd.MarkFlagRequired("number")

	return cmd
}

func newIssuesPostCommentCmd() *cobra.Command {
	var (
		trackerName string
		project     string
		number      int
		marker      string
		result      string
		token       string
		jiraURL     string
		jiraEmail   string
		dryRun      bool
	)

	cmd := &cobra.Command{
		Use:   "post-comment",
		Short: "Post or update a sticky comment on an issue",
		Long: `Posts a comment with a hidden HTML marker on an issue. On first
run, creates a new comment. On re-runs, finds the existing comment
by its marker and edits in-place, collapsing old content into
<details> blocks. This prevents comment flooding on re-runs.

Works across GitHub, GitLab, and Jira via --tracker.

The --marker flag identifies this agent's comments. Each agent
should use a unique marker (e.g. "<!-- fullsend:triage-agent -->").

The --result flag accepts a file path or "-" for stdin.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			printer := ui.New(os.Stdout)

			if number <= 0 {
				return fmt.Errorf("--number must be a positive integer, got %d", number)
			}
			if strings.TrimSpace(marker) == "" {
				return fmt.Errorf("--marker must not be empty")
			}

			body, err := readBody(result)
			if err != nil {
				return fmt.Errorf("reading comment body: %w", err)
			}

			tc, err := newTrackerClient(trackerName, token, jiraURL, jiraEmail)
			if err != nil {
				return err
			}

			printer.Header("Post Comment")

			cfg := sticky.Config{
				Marker: marker,
				DryRun: dryRun,
			}
			_, err = postTrackerStickyComment(cmd.Context(), tc, project, number, body, cfg, printer)
			return err
		},
	}

	cmd.Flags().StringVar(&trackerName, "tracker", "", "tracker backend: github, gitlab, or jira (required)")
	cmd.Flags().StringVar(&project, "project", "", "project identifier: owner/repo (GitHub/GitLab) or project key (Jira) (required)")
	cmd.Flags().IntVar(&number, "number", 0, "issue number (required)")
	cmd.Flags().StringVar(&marker, "marker", "", "hidden HTML marker to identify this agent's comments (required)")
	cmd.Flags().StringVar(&result, "result", "-", "path to comment body file, or '-' for stdin")
	cmd.Flags().StringVar(&token, "token", "", "API token (default: env var per tracker)")
	cmd.Flags().StringVar(&jiraURL, "jira-url", "", "Jira instance URL (default: $JIRA_BASE_URL)")
	cmd.Flags().StringVar(&jiraEmail, "jira-email", "", "Jira user email for Basic auth (default: $JIRA_USER_EMAIL)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print what would be posted without making API calls")
	_ = cmd.MarkFlagRequired("tracker")
	_ = cmd.MarkFlagRequired("project")
	_ = cmd.MarkFlagRequired("number")
	_ = cmd.MarkFlagRequired("marker")

	return cmd
}

// postTrackerStickyComment implements the sticky comment lifecycle using
// tracker.Client instead of forge.Client. It mirrors the behavior of
// sticky.Post: find an existing comment bearing the marker, collapse
// old content into history, and create or update in-place.
//
// Unlike sticky.Post, this function does not perform bot-user
// verification for marker spoofing protection (tracker.Client has no
// GetAuthenticatedUser method). This is acceptable because the new
// command is used by agents in trusted CI environments, not by
// untrusted external callers.
func postTrackerStickyComment(ctx context.Context, tc tracker.Client, project string, number int, body string, cfg sticky.Config, printer *ui.Printer) (string, error) {
	if strings.TrimSpace(body) == "" {
		return "", fmt.Errorf("comment body is empty")
	}
	if strings.TrimSpace(cfg.Marker) == "" {
		return "", fmt.Errorf("marker is empty")
	}

	comments, err := tc.ListComments(ctx, project, number)
	if err != nil {
		return "", fmt.Errorf("listing comments: %w", err)
	}

	existing := findMarkedTrackerComment(comments, cfg.Marker)
	markedBody := cfg.Marker + "\n" + body

	if existing != nil {
		printer.StepStart("Found existing comment, updating in-place")

		newBody := sticky.BuildUpdatedBody(string(existing.Body), markedBody, cfg)

		if cfg.DryRun {
			printer.StepInfo("Dry run — would update comment " + existing.ID)
			printer.StepInfo(fmt.Sprintf("Body length: %d", len(newBody)))
			return "", nil
		}

		if err := tc.UpdateComment(ctx, project, number, existing.ID, tracker.Body(newBody)); err != nil {
			return "", fmt.Errorf("updating comment: %w", err)
		}
		printer.StepDone("Comment updated")
		return existing.HTMLURL, nil
	}

	printer.StepStart("No existing comment found, creating new one")

	if cfg.DryRun {
		printer.StepInfo("Dry run — would create new comment")
		printer.StepInfo(fmt.Sprintf("Body length: %d", len(markedBody)))
		return "", nil
	}

	created, err := tc.CreateComment(ctx, project, number, tracker.Body(markedBody))
	if err != nil {
		return "", fmt.Errorf("creating comment: %w", err)
	}
	printer.StepDone("Comment created")
	return created.HTMLURL, nil
}

// findMarkedTrackerComment returns the first tracker comment whose body
// contains the given marker string, or nil if none is found. This is
// the tracker.Comment equivalent of sticky.FindMarkedComment.
func findMarkedTrackerComment(comments []tracker.Comment, marker string) *tracker.Comment {
	for i := range comments {
		if strings.Contains(string(comments[i].Body), marker) {
			return &comments[i]
		}
	}
	return nil
}
