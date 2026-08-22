package runtime

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/fullsend-ai/fullsend/internal/sandbox"
	"github.com/fullsend-ai/fullsend/internal/ui"
)

// Model selection for pi. Harness `model:` is validated by validModelName
// (no "/"), so the Claude-style aliases the fleet uses are mapped onto pi's
// `provider/id` form here. The ids are pi 0.84.2's Anthropic catalog
// (packages/ai/src/providers/data/anthropic.json), which the vendored
// anthropic-vertex extension registers verbatim; whether Vertex accepts each
// id is a lifecycle-test item (docs/runtimes.md). Both the provider and the
// final model string can be overridden from the runner environment.
const (
	piDefaultProvider = "anthropic-vertex"
	piDefaultModel    = "opus"
	// piModelEnv replaces the whole model argument (e.g. "anthropic/claude-opus-4-6").
	piModelEnv = "FULLSEND_PI_MODEL"
	// piProviderEnv replaces the provider prefix applied to bare model ids.
	piProviderEnv = "FULLSEND_PI_PROVIDER"
)

var piModelAliases = map[string]string{
	"opus":   "claude-opus-4-6",
	"sonnet": "claude-sonnet-4-6",
	"haiku":  "claude-haiku-4-5",
}

// translatePiModel resolves the harness/agent model into pi's --model value.
func translatePiModel(model string) string {
	if v := strings.TrimSpace(os.Getenv(piModelEnv)); v != "" {
		return v
	}
	model = strings.TrimSpace(model)
	if model == "" {
		model = piDefaultModel
	}
	if strings.Contains(model, "/") {
		return model
	}
	if id, ok := piModelAliases[model]; ok {
		model = id
	}
	provider := strings.TrimSpace(os.Getenv(piProviderEnv))
	if provider == "" {
		provider = piDefaultProvider
	}
	return provider + "/" + model
}

// piBareModelID strips the provider prefix from a pi model spec.
func piBareModelID(spec string) string {
	if i := strings.LastIndexByte(spec, '/'); i >= 0 {
		return spec[i+1:]
	}
	return spec
}

// piThinkingLevels are pi's --thinking values; the harness effort values are
// a subset, so the mapping is identity (docs/runtimes.md config-key table).
var piThinkingLevels = map[string]bool{
	"off": true, "minimal": true, "low": true, "medium": true, "high": true, "xhigh": true, "max": true,
}

func piThinkingFor(effort string) (string, bool) {
	effort = strings.TrimSpace(effort)
	if effort == "" {
		return "", false
	}
	return effort, piThinkingLevels[effort]
}

// buildPiRunCommand renders the in-sandbox command line. Security-relevant
// flags: --no-approve and defaultProjectTrust "never" keep repo-owned .pi/
// out; --no-extensions with explicit -e means only the runner-vetted
// extensions load; --tools is pi's strict allowlist across built-in and
// extension tools.
func buildPiRunCommand(params RunParams, m *piManifest) string {
	r := PiRuntime{}
	envFile := sandbox.SandboxWorkspace + "/.env"

	parts := []string{
		fmt.Sprintf("cd %s && . %s", shellQuote(params.RepoDir), shellQuote(envFile)),
		"&& export " + piManifestEnv + "=" + shellQuote(r.piManifestPath()),
		"&& pi",
		"--print",
		"--mode json",
		"--no-approve",
		"--no-extensions",
		"--no-prompt-templates",
		"--no-themes",
		"--session-dir " + shellQuote(r.piSessionsDir()),
		"-e " + shellQuote(PiVertexExtensionPath),
	}
	if m.Hooks != nil {
		parts = append(parts, "-e "+shellQuote(r.ConfigDir()+"/"+piHooksExtensionFile))
	}
	if m.Tools != nil {
		tools := m.Tools
		if len(tools) == 0 {
			// An agent that lists only tools pi cannot provide (or only
			// Skill) gets no built-in tools rather than pi's defaults.
			parts = append(parts, "--no-builtin-tools")
		} else {
			parts = append(parts, "--tools "+shellQuote(strings.Join(tools, ",")))
		}
	}

	model := params.Model
	if model == "" {
		model = m.Model
	}
	parts = append(parts, "--model "+shellQuote(translatePiModel(model)))

	if level, ok := piThinkingFor(params.Effort); ok {
		parts = append(parts, "--thinking "+shellQuote(level))
	}

	parts = append(parts, shellQuote("Run the agent task"))

	if params.Debug != "" {
		// pi has no debug-file flag; in debug mode its stderr goes to the
		// artifact ExtractDebugLog downloads instead of the console.
		parts = append(parts, "2>>"+shellQuote(sandbox.SandboxWorkspace+"/"+piDebugLog))
	}
	return strings.Join(parts, " ")
}

// piManifestEnv tells the hook extension where the manifest is.
const piManifestEnv = "FULLSEND_PI_MANIFEST"

// Run executes one agent iteration and normalizes pi's --mode json stream
// into AgentEvents. pi exits 0 on model error in json mode, so the stream's
// verdict overrides the exit code (#2786/#5361).
func (r PiRuntime) Run(ctx context.Context, params RunParams, printer *ui.Printer, start time.Time, metrics *RunMetrics) (int, error) {
	m, err := readPiManifest(params.SandboxName, r.piManifestPath())
	if err != nil {
		return -1, err
	}
	cmd := buildPiRunCommand(params, m)

	stdout, execCmd, cancel, err := sandbox.ExecStreamReader(ctx, params.SandboxName, cmd, params.Timeout, os.Stderr)
	if err != nil {
		return -1, err
	}
	defer cancel()

	var reader io.Reader = stdout
	if params.OutputPath != "" {
		f, ferr := os.Create(params.OutputPath)
		if ferr != nil {
			printer.StepWarn(fmt.Sprintf("Failed to create %s: ", params.OutputPath) + ferr.Error())
		} else {
			defer f.Close()
			reader = io.TeeReader(stdout, f)
		}
	}

	handler := params.OnEvent
	if handler == nil {
		renderer := NewEventRenderer(printer)
		handler = renderer.Handle
	}

	model := params.Model
	if model == "" {
		model = m.Model
	}
	modelSpec := translatePiModel(model)
	// Telemetry and the renderer get the bare model id, as they do for
	// Claude Code, so runs group by model across runtimes; the provider is
	// gen_ai.system's job and stays visible on the command line.
	metrics.Model = piBareModelID(modelSpec)
	// The wire carries no CLI version and the model only on the first
	// assistant message; Bootstrap's preflight and the resolved model are
	// known up front, so emit the InitEvent here and drop the parser's.
	handler(InitEvent{Model: metrics.Model, Version: m.PiVersion})

	var lastResult *ResultEvent
	innerHandler := handler
	handler = func(evt AgentEvent) {
		switch e := evt.(type) {
		case InitEvent:
			return
		case ResultEvent:
			lastResult = &e
			metrics.NumTurns = e.NumTurns
			metrics.TotalCostUSD = e.TotalCostUSD
			metrics.InputTokens = e.InputTokens
			metrics.OutputTokens = e.OutputTokens
			metrics.ReasoningTokens = e.ReasoningTokens
			metrics.CacheCreationInputTokens = e.CacheCreationInputTokens
			metrics.CacheReadInputTokens = e.CacheReadInputTokens
		case ToolUseEvent:
			metrics.ToolCalls.Add(1)
		}
		innerHandler(evt)
	}

	if _, parseErr := parsePiStream(reader, handler); parseErr != nil {
		fmt.Fprintf(os.Stderr, "  progress parser: %v\n", sanitizeOutput(parseErr.Error()))
		cancel()
		io.Copy(io.Discard, reader)
	}

	waitErr := execCmd.Wait()
	exitCode := -1
	if execCmd.ProcessState != nil {
		exitCode = execCmd.ProcessState.ExitCode()
	}
	if waitErr != nil && execCmd.ProcessState == nil {
		return exitCode, fmt.Errorf("openshell exec failed: %w", waitErr)
	}

	if exitCode == 0 && lastResult != nil && lastResult.IsError {
		msg := lastResult.ErrorMessage
		if msg == "" {
			msg = "stopReason " + lastResult.Subtype
		}
		printer.StepWarn("pi exited 0 but the stream reports an error: " + sanitizeOutput(msg))
		return 1, nil
	}
	return exitCode, nil
}

// ClearIterationArtifacts removes the previous iteration's outputs and
// sessions so transcripts and output files are per-iteration.
func (r PiRuntime) ClearIterationArtifacts(sandboxName string) error {
	clearCmd := fmt.Sprintf("rm -rf %s/output/* %s/* %s",
		r.WorkspaceDir(), r.piSessionsDir(), shellQuote(r.WorkspaceDir()+"/"+piDebugLog))
	_, _, _, err := sandbox.Exec(sandboxName, clearCmd, 10*time.Second)
	return err
}

// DebugLogName implements DebugLogNamer.
func (PiRuntime) DebugLogName() string { return piDebugLog }
