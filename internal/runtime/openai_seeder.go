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

// SubagentsNeedOpenAIProvider reports whether a pre-configured pi child —
// a repo `subagents.<persona>` entry, `subagents.default`, or a discovered
// persona's own frontmatter `model:` — resolves to the openai provider,
// even when the parent's own model does not (#7981). NeedsOpenAIProvider
// alone answers this only for the parent: pi is multi-provider per child,
// not just per run, so a Vertex parent with an OpenAI persona previously
// never caused the run-scoped OpenAI provider to be created, and
// resolvePersonaModels then rejected the persona because the run never
// requested the credential that would have made it available.
//
// Every backend other than "pi" has no subagents/persona concept of its
// own: codex always needs the provider regardless (NeedsOpenAIProvider
// already covers it), and the rest need none.
//
// This deliberately does not cover a model name chosen at dispatch time by
// an Agent call's `model` argument — that value is not known before the
// sandbox starts, so admitting it here would make the gate depend on what
// the dispatching model decides to type rather than on configuration the
// repo controls.
//
// Resolution uses piChildModelProvider, not piModelProvider: a child
// reference can carry a persona "@suffix", or name a repo models.aliases
// entry case-insensitively or by the alias's own bare id, all of which
// resolvePersonaModels' canonicalise already accepts when Bootstrap resolves
// the same reference. Using the parent-only piModelProvider here would miss
// those forms and skip creating the credential a persona pinned to them
// would then need (#7981).
func SubagentsNeedOpenAIProvider(backend string, subagentsCfg map[string]*string, skillDirs []string, agentName string, configAliases map[string]string) bool {
	if backend != "pi" {
		return false
	}
	for _, v := range subagentsCfg {
		if v != nil && piChildModelProvider(*v, configAliases) == piOpenAIProvider {
			return true
		}
	}
	// discoverPersonas never returns a non-nil error; the shape is kept so
	// it reads the same as its other call site.
	personas, _, _ := discoverPersonas(skillDirs, agentName)
	for _, p := range personas {
		if subagentsCfg[p.Name] != nil {
			// Already checked above; a config override wins over
			// frontmatter and must not be double-counted.
			continue
		}
		if p.Model != "" && piChildModelProvider(p.Model, configAliases) == piOpenAIProvider {
			return true
		}
	}
	return false
}
