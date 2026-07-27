package config

// perRepoDefaults implements PerRepoConfigReader with compiled-in
// code defaults. It serves as the terminal node in the parent
// fallback chain (ADR 0069 Decision 2).
//
// Lookup order for all accessors: overlay -> base -> code defaults.
// perRepoDefaults is the "code defaults" layer.
type perRepoDefaults struct{}

// Compile-time assertion that perRepoDefaults satisfies PerRepoConfigReader.
var _ PerRepoConfigReader = (*perRepoDefaults)(nil)

func (d *perRepoDefaults) ConfigVersion() string                    { return "1" }
func (d *perRepoDefaults) ConfigRuntime() string                    { return "claude" }
func (d *perRepoDefaults) IsKillSwitchActive() bool                 { return false }
func (d *perRepoDefaults) ConfigRoles() []string                    { return PerRepoDefaultRoles() }
func (d *perRepoDefaults) AgentEntries() []AgentEntry               { return nil }
func (d *perRepoDefaults) AllowedResources() []string               { return DefaultAllowedRemoteResources() }
func (d *perRepoDefaults) IssueCreationConfig() *CreateIssuesConfig { return nil }
func (d *perRepoDefaults) IsOrgMode() bool                          { return false }
