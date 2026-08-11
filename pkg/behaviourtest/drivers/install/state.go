package install

// PerRepoState is the read-only install state for per-repo mode.
// All fields are set at construction and never modified. Sharing a
// single instance across goroutines is safe by design.
type PerRepoState struct {
	org     string
	repo    string
	mintURL string // mint URL used during install
}

// NewPerRepoState constructs a PerRepoState. Used by sub-package drivers.
func NewPerRepoState(org, repo, mintURL string) *PerRepoState {
	return &PerRepoState{org: org, repo: repo, mintURL: mintURL}
}

func (s *PerRepoState) Mode() string               { return "per-repo" }
func (s *PerRepoState) TestRepo() string           { return s.repo }
func (s *PerRepoState) ConfigOwner() string        { return s.org }
func (s *PerRepoState) ConfigRepo() string         { return s.repo }
func (s *PerRepoState) ConfigPathPrefix() string   { return ".fullsend" }
func (s *PerRepoState) TriageWorkflowRepo() string { return s.repo }
func (s *PerRepoState) TriageWorkflowFile() string { return PerRepoTriageWorkflow }
func (s *PerRepoState) AgentWorkflowFile() string  { return PerRepoAgentWorkflow }
func (s *PerRepoState) AgentArtifactName() string  { return PerRepoAgentArtifact }

// MintURL returns the mint URL used during install.
func (s *PerRepoState) MintURL() string { return s.mintURL }

// Compile-time checks.
var (
	_ State           = (*PerRepoState)(nil)
	_ MintURLProvider = (*PerRepoState)(nil)
)
