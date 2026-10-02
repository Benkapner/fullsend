package runtime

// OpenAICredentialSeeder is implemented by runtimes that read the OpenAI
// credential placeholder from a runner-owned file the runner re-seeds after
// every refresh (ADR 0092). OpenAIAuthSeed returns the POSIX sh fragment
// that writes the sandbox's current OPENAI_API_KEY placeholder into that
// file, failing closed when the value is not a gateway placeholder;
// OpenAIAuthFile returns the file's absolute sandbox path, used to verify
// the seed after a refresh.
//
// The placeholder cannot simply be read from the process environment: it is
// revision-scoped on OpenShell 0.0.110+ and pinned to the credential
// generation it was issued for, so a process started before a refresh holds
// a placeholder the gateway will no longer resolve. A file the agent process
// re-reads per request is what lets a running iteration follow a refresh.
//
// A runtime that returns "" from OpenAIAuthSeed is treated as having no
// seeder: the run-scoped provider is still created and refreshed, but no
// in-sandbox re-seed is attempted. Runtimes with no OpenAI path at all
// (Claude Code, dummy) do not implement the interface.
type OpenAICredentialSeeder interface {
	OpenAIAuthSeed() string
	OpenAIAuthFile() string
}

// NeedsOpenAIProvider reports whether a run on the named backend with the
// given effective model needs the OpenAI run-scoped provider — the runner
// creates one only then, so a harness may declare the openai provider for
// every runtime without forcing an OpenAI credential on runs that never
// call OpenAI (#6920).
//
// codex speaks only the OpenAI Responses API, so it always needs one. pi is
// multi-provider: it needs one exactly when the effective model resolves to
// its openai provider, which is the same resolution buildPiRunCommand gates
// on (provider prefix, the FULLSEND_PI_PROVIDER default for a bare id).
// Every other backend — Claude Code on Vertex, the test runtimes — needs
// none.
//
// runModel is what the runner resolved (flag > env > agents: entry >
// harness `model:`) and agentModel the agent definition's frontmatter
// `model:`; both are passed rather than one pre-resolved value so a caller
// cannot resolve them differently from the runtime's own launch path — see
// EffectiveModel. configAliases is the repo's models.aliases (nil if unset),
// threaded through so an alias remapped to an openai id resolves to the
// same provider here as it does in buildPiRunCommand.
func NeedsOpenAIProvider(backend, runModel, agentModel string, configAliases map[string]string) bool {
	switch backend {
	case "codex":
		return true
	case "pi":
		return piModelProvider(EffectiveModel(runModel, agentModel), configAliases) == piOpenAIProvider
	default:
		return false
	}
}

// OpenAIChildren lists the pre-configured pi children whose model resolves
// to the openai provider: a repo `subagents.<persona>` entry,
// `subagents.default`, or a discovered persona's own frontmatter `model:`,
// each resolved through models.aliases (#7981). Each entry names where the
// model came from and the spec it resolves to, sorted, for an error message.
//
// NeedsOpenAIProvider answers only for the parent. pi is multi-provider per
// child, so a Vertex parent with an OpenAI persona needs the run-scoped
// OpenAI provider too; the runner creates it when this list is non-empty,
// and fails before the sandbox exists when the harness declares no openai
// provider to create. piAgentManifestFor admits the same specs into the
// children's openai allowlist, so the gate and the allowlist cannot drift.
//
// A model an Agent call names at dispatch time is not covered: it is not
// known before the sandbox starts. Every backend other than pi has no
// per-child provider (codex always needs the provider; the rest never do),
// so the list is empty for them.
func OpenAIChildren(backend string, subagentsCfg map[string]*string, skillDirs []string, agentName string, configAliases map[string]string) []string {
	if backend != "pi" {
		return nil
	}
	// discoverPersonas never returns a non-nil error; Bootstrap runs the
	// same discovery on the same directories.
	personas, _, _ := discoverPersonas(skillDirs, agentName)
	var out []string
	for _, c := range piConfiguredOpenAIChildren(personas, subagentsCfg, configAliases) {
		out = append(out, c.source+" → "+c.spec)
	}
	return out
}
