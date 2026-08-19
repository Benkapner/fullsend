package mintcore

// StatusGitHubGroup and StatusGitHubClientID are stamped into the
// deployed binary at build/deploy time, the same mechanism used for
// Version and Commit. In development and tests they default to empty
// strings.
//
// StatusGitHubGroup is an ORG/TEAM slug. When the github build tag
// is active, the GitHub status validator checks that the caller is a
// member of this team. When the tag is absent (stub), these values
// are unused.
//
// StatusGitHubClientID is the GitHub OAuth App client ID. The client
// secret is a platform secret (GCF env / Worker secret) accessed
// at runtime inside the real GitHub validator via mintEnv.
var (
	StatusGitHubGroup    string
	StatusGitHubClientID string
)
