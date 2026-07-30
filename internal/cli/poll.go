package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fullsend-ai/fullsend/internal/config"
	"github.com/fullsend-ai/fullsend/internal/dispatch"
	"github.com/fullsend-ai/fullsend/internal/forge/gitlab"
	"github.com/fullsend-ai/fullsend/internal/forge/jira"
	"github.com/fullsend-ai/fullsend/internal/jirapoll"
	"github.com/fullsend-ai/fullsend/internal/poll"
)

func newPollCmd() *cobra.Command {
	var (
		forgeFlag    string
		inputDriver  string
		projectPath  string
		gitlabURL    string
		outputPath   string
		pollModeFlag string
		fullsendDir  string
		jiraURL      string
		jiraProject  string
		jqlOverride  string
		targetRepo   string
	)

	cmd := &cobra.Command{
		Use:   "poll",
		Short: "Poll forge or external tracker APIs for new events and dispatch agent stages",
		RunE: func(cmd *cobra.Command, args []string) error {
			if inputDriver == "jira-poll" {
				return runJiraPoll(cmd, jiraURL, jiraProject, jqlOverride, targetRepo, outputPath, fullsendDir)
			}

			if forgeFlag != "gitlab" {
				return fmt.Errorf("poll command supports --forge gitlab or --input-driver jira-poll (got forge=%q, input-driver=%q)", forgeFlag, inputDriver)
			}

			forgeToken := os.Getenv("FULLSEND_FORGE_TOKEN")
			if forgeToken == "" {
				return fmt.Errorf("FULLSEND_FORGE_TOKEN is required")
			}

			if projectPath == "" {
				projectPath = os.Getenv("CI_PROJECT_PATH")
			}
			if projectPath == "" {
				return fmt.Errorf("--project or CI_PROJECT_PATH is required")
			}

			slashCommandsOnly := pollModeFlag == "fast" || os.Getenv("FULLSEND_POLL_MODE") == "fast"

			glClient, err := gitlab.New(forgeToken, gitlab.WithBaseURL(gitlabURL))
			if err != nil {
				return fmt.Errorf("create GitLab client: %w", err)
			}
			pollClient := gitlab.NewPollClient(glClient)

			botUserID, err := pollClient.GetAuthenticatedUserID(cmd.Context())
			if err != nil {
				return fmt.Errorf("resolve bot user ID: %w", err)
			}

			// Build the event router from config + agents-repo known agents.
			router, err := buildRouter(fullsendDir)
			if err != nil {
				return fmt.Errorf("build event router: %w", err)
			}

			pipelineRef := os.Getenv("CI_COMMIT_REF_NAME")
			if pipelineRef == "" {
				pipelineRef = os.Getenv("CI_DEFAULT_BRANCH")
			}
			if pipelineRef == "" {
				return fmt.Errorf("CI_COMMIT_REF_NAME or CI_DEFAULT_BRANCH is required for pipeline dispatch")
			}

			opts := poll.Options{
				SlashCommandsOnly: slashCommandsOnly,
				BotUserID:         botUserID,
				GitLabURL:         gitlabURL,
				PipelineRef:       pipelineRef,
				PollJobURL:        os.Getenv("CI_JOB_URL"),
			}

			poller := poll.New(pollClient, router, projectPath, opts)
			return poller.Run(cmd.Context())
		},
	}

	cmd.Flags().StringVar(&forgeFlag, "forge", "", "Forge platform (gitlab)")
	cmd.Flags().StringVar(&inputDriver, "input-driver", "", "Poll input driver (jira-poll)")
	cmd.Flags().StringVar(&projectPath, "project", "", "GitLab project path (default: $CI_PROJECT_PATH)")
	cmd.Flags().StringVar(&gitlabURL, "gitlab-url", "https://gitlab.com", "GitLab instance URL")
	cmd.Flags().StringVar(&outputPath, "output", "", "Path to write dispatches JSON")
	cmd.Flags().StringVar(&pollModeFlag, "poll-mode", "", "Poll mode: fast (slash commands only) or full")
	cmd.Flags().StringVar(&fullsendDir, "fullsend-dir", "", "base directory containing the .fullsend layout")
	_ = cmd.MarkFlagRequired("fullsend-dir")
	cmd.Flags().StringVar(&jiraURL, "jira-url", "", "Jira instance base URL (default: $JIRA_BASE_URL)")
	cmd.Flags().StringVar(&jiraProject, "jira-project", "", "Jira project key for JQL scoping")
	cmd.Flags().StringVar(&jqlOverride, "jql", "", "Custom JQL override")
	cmd.Flags().StringVar(&targetRepo, "target-repo", "", "GitHub repo slug where agents run (default: $GITHUB_REPOSITORY)")

	cmd.Hidden = true
	return cmd
}

// jiraPollArgs holds resolved arguments for runJiraPoll after env-var fallbacks.
type jiraPollArgs struct {
	jiraURL     string
	jiraProject string
	jqlOverride string
	targetRepo  string
	outputPath  string
	fullsendDir string
}

// validateJiraPollArgs resolves env-var fallbacks and validates required
// arguments for the jira-poll input driver. It returns the resolved args
// or a validation error.
func validateJiraPollArgs(jiraURL, jiraProject, jqlOverride, targetRepo, outputPath, fullsendDir string) (jiraPollArgs, error) {
	if jiraURL == "" {
		jiraURL = os.Getenv("JIRA_BASE_URL")
	}
	if jiraURL == "" {
		return jiraPollArgs{}, fmt.Errorf("--jira-url or JIRA_BASE_URL is required")
	}

	if targetRepo == "" {
		targetRepo = os.Getenv("GITHUB_REPOSITORY")
	}
	if targetRepo == "" {
		return jiraPollArgs{}, fmt.Errorf("--target-repo or GITHUB_REPOSITORY is required")
	}

	if jiraProject == "" && jqlOverride == "" {
		return jiraPollArgs{}, fmt.Errorf("--jira-project or --jql is required")
	}

	return jiraPollArgs{
		jiraURL:     jiraURL,
		jiraProject: jiraProject,
		jqlOverride: jqlOverride,
		targetRepo:  targetRepo,
		outputPath:  outputPath,
		fullsendDir: fullsendDir,
	}, nil
}

func runJiraPoll(cmd *cobra.Command, jiraURL, jiraProject, jqlOverride, targetRepo, outputPath, fullsendDir string) error {
	args, err := validateJiraPollArgs(jiraURL, jiraProject, jqlOverride, targetRepo, outputPath, fullsendDir)
	if err != nil {
		return err
	}

	jiraClient, err := buildJiraClient(args.jiraURL)
	if err != nil {
		return fmt.Errorf("create Jira client: %w", err)
	}

	router, err := buildRouter(args.fullsendDir)
	if err != nil {
		return fmt.Errorf("build event router: %w", err)
	}

	opts := jirapoll.Options{
		TargetRepo:  args.targetRepo,
		JiraBaseURL: args.jiraURL,
		JiraProject: args.jiraProject,
		JQL:         args.jqlOverride,
		OutputPath:  args.outputPath,
	}

	poller := jirapoll.New(jiraClient, router, opts)
	return poller.Run(cmd.Context())
}

// buildJiraClient creates a Jira client using either OAuth 2.0 client credentials
// or Basic/Bearer auth depending on JIRA_AUTH_METHOD.
func buildJiraClient(jiraURL string) (*jira.Client, error) {
	authMethod := os.Getenv("JIRA_AUTH_METHOD")

	if authMethod == "oauth2" {
		clientID := os.Getenv("JIRA_CLIENT_ID")
		clientSecret := os.Getenv("JIRA_CLIENT_SECRET")
		if clientID == "" || clientSecret == "" {
			return nil, fmt.Errorf("JIRA_CLIENT_ID and JIRA_CLIENT_SECRET are required when JIRA_AUTH_METHOD=oauth2")
		}
		return jira.NewOAuth2(clientID, clientSecret, jira.WithBaseURL(jiraURL))
	}

	jiraToken := os.Getenv("JIRA_TOKEN")
	if jiraToken == "" {
		return nil, fmt.Errorf("JIRA_TOKEN environment variable is required (or set JIRA_AUTH_METHOD=oauth2)")
	}
	var opts []jira.Option
	opts = append(opts, jira.WithBaseURL(jiraURL))
	if email := os.Getenv("JIRA_USER_EMAIL"); email != "" {
		opts = append(opts, jira.WithEmail(email))
	}
	return jira.New(jiraToken, opts...)
}

// buildRouter constructs a HarnessRouter from config-registered agents
// and the known first-party agents available via agents-repo fallback.
func buildRouter(fullsendDir string) (*dispatch.HarnessRouter, error) {
	cfg, err := config.LoadConfig(fullsendDir, config.LoadOpts{MissingOK: true})
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	seen := make(map[string]bool)
	var names []string

	entries := cfg.AgentEntries()
	for i := len(entries) - 1; i >= 0; i-- {
		name := entries[i].DerivedName()
		lower := strings.ToLower(name)
		if !seen[lower] {
			seen[lower] = true
			if entries[i].IsEnabled() {
				names = append(names, name)
			}
		}
	}

	for name := range defaultAgentsRepoKnownAgents {
		if !seen[name] && !config.IsAgentExplicitlyDisabled(entries, name) {
			seen[name] = true
			names = append(names, name)
		}
	}

	return dispatch.NewHarnessRouter(names), nil
}
