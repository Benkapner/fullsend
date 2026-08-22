package runtime

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/fullsend-ai/fullsend/internal/sandbox"
	"github.com/fullsend-ai/fullsend/internal/ui"
)

// PiRuntime is a stub implementation of the Runtime and TranscriptHandler
// interfaces for the pi agent runtime (earendil-works/pi, CLI `pi`). All
// methods are no-ops or return not-implemented errors. Subsequent PRs will
// fill in stream parsing (`pi --mode json`, per docs/json.md — confirm at
// implementation time whether `--print` is required alongside it), bootstrap,
// run execution, and transcript extraction. Tracked in #6464.
type PiRuntime struct{}

func (PiRuntime) Name() string { return "pi" }

// System returns the OTEL GenAI gen_ai.system value. Like OpenCode, pi is
// multi-provider (Anthropic, OpenAI, Google, ...), so the system is the
// runtime itself rather than a single model vendor (same precedent as
// OpenCodeRuntime; the per-message `provider` field in pi's event stream may
// allow capturing the actual vendor in a future PR).
func (PiRuntime) System() string { return "pi" }

// ConfigDir returns the pi config directory inside the sandbox. It is
// exported to the agent process as PI_CODING_AGENT_DIR (see EnvExports) and
// lives outside the agent-writable workspace so the target repo cannot
// rewrite the runtime's settings, extensions, or skills.
func (PiRuntime) ConfigDir() string { return sandbox.SandboxPiConfig }

func (PiRuntime) WorkspaceDir() string { return sandbox.SandboxWorkspace }

// EnvExports pins pi's config and session locations to runner-owned paths
// and disables all startup network traffic (update checks, package update
// checks, telemetry). PI_OFFLINE does not affect the inference call itself.
// Var names/semantics per earendil-works/pi docs/environment-variables.md
// (PI_CODING_AGENT_DIR, PI_CODING_AGENT_SESSION_DIR, PI_OFFLINE,
// PI_SKIP_VERSION_CHECK) — re-verify against that doc when PI_VERSION moves.
func (r PiRuntime) EnvExports() []string {
	return []string{
		fmt.Sprintf("export PI_CODING_AGENT_DIR=%s", r.ConfigDir()),
		fmt.Sprintf("export PI_CODING_AGENT_SESSION_DIR=%s/sessions", r.ConfigDir()),
		"export PI_OFFLINE=1",
		"export PI_SKIP_VERSION_CHECK=1",
	}
}

func (PiRuntime) Bootstrap(_ BootstrapInput) error {
	return fmt.Errorf("pi runtime is not yet implemented")
}

func (PiRuntime) Run(_ context.Context, _ RunParams, _ *ui.Printer, _ time.Time, _ *RunMetrics) (int, error) {
	return -1, fmt.Errorf("pi runtime is not yet implemented")
}

func (PiRuntime) ClearIterationArtifacts(_ string) error { return nil }

// TranscriptHandler stub methods — return not-implemented errors for extract
// methods (to avoid silent success claims in CI logs) and no-ops for parse
// methods (which correctly indicate "nothing found"). pi writes JSONL session
// files under PI_CODING_AGENT_SESSION_DIR, so extraction will follow the
// Claude find-and-download shape rather than OpenCode's export path (#6464).

func (PiRuntime) ExtractTranscripts(_, _, _ string) error {
	return fmt.Errorf("pi transcript extraction not implemented (see #6464)")
}

func (PiRuntime) ExtractDebugLog(_, _, _ string) error {
	return fmt.Errorf("pi debug log extraction not implemented (see #6464)")
}

func (PiRuntime) ParseTranscriptErrors(_ string) []TranscriptError { return nil }

func (PiRuntime) ParseTranscriptFile(_ string) (TranscriptError, bool) {
	return TranscriptError{}, false
}

func (PiRuntime) EmitTranscriptErrors(w io.Writer, summaries []TranscriptError) {
	emitTranscriptErrors(w, summaries)
}

// Compile-time interface assertions.
var (
	_ Runtime           = PiRuntime{}
	_ TranscriptHandler = PiRuntime{}
)
