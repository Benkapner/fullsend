package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fullsend-ai/fullsend/internal/dispatch"
)

func TestBuildRouter_NoConfigFile(t *testing.T) {
	router, err := buildRouter(t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if router == nil {
		t.Fatal("expected non-nil router")
	}

	// Scaffold defaults should be routable.
	stages, err := router.Route(&dispatch.NormalizedEvent{
		Entity:     dispatch.Entity{Kind: "work_item", ID: 1},
		Transition: dispatch.Transition{Kind: "comment_added", Comment: &dispatch.TransitionComment{Command: "/fs-triage", Body: "/fs-triage"}},
		Actor:      dispatch.Actor{ID: "alice", Role: "write"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stages) != 1 || stages[0] != "triage" {
		t.Fatalf("expected [triage] from scaffold defaults, got %v", stages)
	}
}

func TestBuildRouter_WithConfigAgents(t *testing.T) {
	dir := t.TempDir()
	configYAML := `agents:
  - name: my-custom-agent
  - name: code
    enabled: false
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(configYAML), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	router, err := buildRouter(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if router == nil {
		t.Fatal("expected non-nil router")
	}

	// Custom agent should be routable via slash command.
	stages, err := router.Route(&dispatch.NormalizedEvent{
		Entity:     dispatch.Entity{Kind: "work_item", ID: 1},
		Transition: dispatch.Transition{Kind: "comment_added", Comment: &dispatch.TransitionComment{Command: "/fs-my-custom-agent", Body: "/fs-my-custom-agent"}},
		Actor:      dispatch.Actor{ID: "alice", Role: "write"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stages) != 1 || stages[0] != "my-custom-agent" {
		t.Fatalf("expected [my-custom-agent], got %v", stages)
	}

	// Disabled agent (code) should not be routable.
	stages, err = router.Route(&dispatch.NormalizedEvent{
		Entity:     dispatch.Entity{Kind: "work_item", ID: 1},
		Transition: dispatch.Transition{Kind: "label_changed", Label: &dispatch.TransitionLabel{Name: "ready-to-code", Action: "added"}},
		Actor:      dispatch.Actor{ID: "alice", Role: "write"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stages) != 0 {
		t.Fatalf("expected no stages for disabled code agent, got %v", stages)
	}
}

func TestValidateJiraPollArgs(t *testing.T) {
	fullsendDir := t.TempDir()

	tests := []struct {
		name           string
		envVars        map[string]string
		jiraURL        string
		jiraProject    string
		jqlOverride    string
		targetRepo     string
		wantErrContain string
		wantOK         bool
	}{
		{
			name:           "missing jira-url and JIRA_BASE_URL",
			envVars:        map[string]string{},
			jiraURL:        "",
			targetRepo:     "acme/platform",
			jiraProject:    "PROJ",
			wantErrContain: "jira-url",
		},
		{
			name:           "missing target-repo and GITHUB_REPOSITORY",
			envVars:        map[string]string{},
			jiraURL:        "https://acme.atlassian.net",
			targetRepo:     "",
			jiraProject:    "PROJ",
			wantErrContain: "target-repo",
		},
		{
			name:           "missing both jira-project and jql",
			envVars:        map[string]string{},
			jiraURL:        "https://acme.atlassian.net",
			targetRepo:     "acme/platform",
			jiraProject:    "",
			jqlOverride:    "",
			wantErrContain: "jira-project",
		},
		{
			name:        "valid minimal config",
			envVars:     map[string]string{},
			jiraURL:     "https://acme.atlassian.net",
			targetRepo:  "acme/platform",
			jiraProject: "PROJ",
			wantOK:      true,
		},
		{
			name:        "env var fallback for jira-url",
			envVars:     map[string]string{"JIRA_BASE_URL": "https://acme.atlassian.net"},
			jiraURL:     "",
			targetRepo:  "acme/platform",
			jiraProject: "PROJ",
			wantOK:      true,
		},
		{
			name:        "jql without jira-project is valid",
			envVars:     map[string]string{},
			jiraURL:     "https://acme.atlassian.net",
			targetRepo:  "acme/platform",
			jiraProject: "",
			jqlOverride: "project = PROJ ORDER BY updated DESC",
			wantOK:      true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Clear env vars that the function checks as fallbacks.
			t.Setenv("JIRA_BASE_URL", "")
			t.Setenv("GITHUB_REPOSITORY", "")

			for k, v := range tc.envVars {
				t.Setenv(k, v)
			}

			args, err := validateJiraPollArgs(tc.jiraURL, tc.jiraProject, tc.jqlOverride, tc.targetRepo, "", fullsendDir)

			if tc.wantOK {
				if err != nil {
					t.Fatalf("expected no error, got: %v", err)
				}
				// Verify resolved values are populated.
				if args.jiraURL == "" {
					t.Error("expected jiraURL to be resolved")
				}
				if args.targetRepo == "" {
					t.Error("expected targetRepo to be resolved")
				}
				return
			}

			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErrContain)
			}
			if !strings.Contains(err.Error(), tc.wantErrContain) {
				t.Errorf("expected error containing %q, got: %v", tc.wantErrContain, err)
			}
		})
	}
}
