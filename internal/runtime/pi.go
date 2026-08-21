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
// interfaces for the pi agent runtime (earendil-works/pi). All methods are
// no-ops or return not-implemented errors. Subsequent PRs will fill in
// bootstrap, run execution, and transcript extraction once upstream
// dependencies (#608, #6445, #6358) land.
type PiRuntime struct{}

func (PiRuntime) Name() string { return "pi" }

// System returns the OTEL GenAI gen_ai.system value. Pi is multi-provider
// (anthropic, google-vertex, community extensions), so the system is the
// runtime itself rather than a single model vendor. The actual model vendor
// is on AssistantMessage.provider once Bootstrap/Run consume the stream.
func (PiRuntime) System() string { return "pi" }

// ConfigDir returns the pi config directory inside the sandbox.
// Host default for PI_CODING_AGENT_DIR is ~/.pi/agent (config, skills,
// settings). Session JSONL storage is a separate path:
// PI_CODING_AGENT_SESSION_DIR, overridden by --session-dir. This returns
// a sandbox-local placeholder until Bootstrap wires those env vars.
func (PiRuntime) ConfigDir() string { return sandbox.SandboxWorkspace + "/.pi" }

func (PiRuntime) WorkspaceDir() string { return sandbox.SandboxWorkspace }

func (PiRuntime) EnvExports() []string { return nil }

func (PiRuntime) Bootstrap(_ BootstrapInput) error {
	return fmt.Errorf("pi runtime is not yet implemented (see #6464)")
}

func (PiRuntime) Run(_ context.Context, _ RunParams, _ *ui.Printer, _ time.Time, _ *RunMetrics) (int, error) {
	return -1, fmt.Errorf("pi runtime is not yet implemented (see #6464)")
}

func (PiRuntime) ClearIterationArtifacts(_ string) error { return nil }

// TranscriptHandler stub methods — return not-implemented errors for extract
// methods (to avoid silent success claims in CI logs) and no-ops for parse
// methods (which correctly indicate "nothing found"). See #6464.

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
