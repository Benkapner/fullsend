package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/fullsend-ai/fullsend/internal/config"
	"github.com/fullsend-ai/fullsend/internal/harness"
	agentruntime "github.com/fullsend-ai/fullsend/internal/runtime"
	"github.com/fullsend-ai/fullsend/internal/ui"
	"github.com/fullsend-ai/fullsend/internal/urlutil"
)

// warnOpenAISubagentWithoutProvider prints one warning when `agent set
// --subagent` routes a pi child to the openai provider and the agent's
// local harness declares no openai provider (#7981). Such a run fails
// before its sandbox is created; config validation cannot see the harness,
// so this is the earliest place to say so. It is a warning, not an error:
// setting the config first and editing the harness second is a valid order.
//
// It stays silent whenever it cannot tell: a runtime other than pi, an
// agent whose harness is a URL (fleet harnesses declare openai) or is not
// in this config, a harness that does not load, or a provider entry that is
// a URL.
func warnOpenAISubagentWithoutProvider(cfg config.ConfigReader, absDir, agentName, runtimeName string, subagents map[string]*string, printer *ui.Printer) {
	pr, ok := cfg.(config.PerRepoConfigReader)
	if !ok {
		return
	}
	if runtimeName == "" {
		runtimeName = pr.ConfigRuntime()
	}
	if runtimeName != "pi" {
		return
	}
	// Only the subagents values: discovering personas would mean resolving
	// the harness's skills, and the run's own check covers frontmatter.
	children := agentruntime.OpenAIChildren("pi", subagents, nil, agentName, pr.ConfigModelAliases())
	if len(children) == 0 {
		return
	}
	source := agentHarnessSource(cfg.AgentEntries(), agentName)
	if source == "" || urlutil.IsURL(source) {
		return
	}
	h, err := harness.Load(filepath.Join(absDir, source))
	if err != nil {
		return
	}
	declares, known := harnessDeclaresOpenAIProvider(absDir, h.Providers)
	if declares || !known {
		return
	}
	printer.StepWarn(fmt.Sprintf("%s resolves to the openai provider, but %s declares no openai provider; runs will fail until you add \"openai\" to its providers list",
		strings.Join(children, ", "), source))
}

// agentHarnessSource returns the harness source of the named agent's
// registration, or "" when no entry carries one.
func agentHarnessSource(entries []config.AgentEntry, name string) string {
	lower := strings.ToLower(name)
	for i := len(entries) - 1; i >= 0; i-- {
		if strings.ToLower(entries[i].DerivedName()) == lower && entries[i].Source != "" {
			return entries[i].Source
		}
	}
	return ""
}

// harnessDeclaresOpenAIProvider reports whether any declared provider is
// openai-typed, resolving each entry the way the runner does: a path to a
// definition file, a bare name defined under providers/, or a bare name the
// scaffold ships. known is false when an entry is a URL, which this does
// not fetch.
func harnessDeclaresOpenAIProvider(absDir string, providers []string) (declares, known bool) {
	providersDir := filepath.Join(absDir, "providers")
	for _, p := range providers {
		var defs []harness.ProviderDef
		switch {
		case harness.IsURL(p):
			return false, false
		case harness.IsProviderPath(p):
			data, err := os.ReadFile(filepath.Join(absDir, p))
			if err != nil {
				continue
			}
			if def, err := harness.ParseProviderDef(data); err == nil {
				defs = append(defs, def)
			}
		default:
			local, err := harness.LoadProviderDefs(providersDir, map[string]struct{}{p: {}})
			if err != nil {
				continue
			}
			defs = appendEmbeddedProviderDefs(local, nil, []string{p}, ui.New(io.Discard))
		}
		for _, d := range defs {
			if strings.EqualFold(d.Type, openAIProviderType) {
				return true, true
			}
		}
	}
	return false, true
}
