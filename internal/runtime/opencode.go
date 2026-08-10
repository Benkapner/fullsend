package runtime

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/fullsend-ai/fullsend/internal/sandbox"
	"github.com/fullsend-ai/fullsend/internal/ui"
)

// OpenCodeRuntime is a stub implementation of the Runtime and TranscriptHandler
// interfaces for the OpenCode agent runtime. All methods are no-ops or return
// not-implemented errors. Subsequent PRs will fill in stream parsing, bootstrap,
// run execution, and transcript extraction.
type OpenCodeRuntime struct{}

func (OpenCodeRuntime) Name() string { return "opencode" }

// System returns the OTEL GenAI gen_ai.system value. OpenCode is multi-provider
// (Anthropic, OpenAI, Google, etc.), so the system is the runtime itself rather
// than a single model vendor. The actual model vendor may be capturable from
// opencode's stream/export events in a future PR once the event schema is
// confirmed (see #1935).
func (OpenCodeRuntime) System() string { return "opencode" }

func (OpenCodeRuntime) ConfigDir() string { return sandbox.SandboxWorkspace + "/.opencode" }

func (OpenCodeRuntime) WorkspaceDir() string { return sandbox.SandboxWorkspace }

func (OpenCodeRuntime) EnvExports() []string { return nil }

func (OpenCodeRuntime) Bootstrap(_ BootstrapInput) error {
	return fmt.Errorf("opencode runtime is not yet implemented")
}

func (OpenCodeRuntime) Run(_ context.Context, _ RunParams, _ *ui.Printer, _ time.Time, _ *RunMetrics) (int, error) {
	return -1, fmt.Errorf("opencode runtime is not yet implemented")
}

func (OpenCodeRuntime) ClearIterationArtifacts(_ string) error { return nil }

// TranscriptHandler stub methods — all no-ops until opencode export integration
// is implemented.

func (OpenCodeRuntime) ExtractTranscripts(_, _, _ string) error { return nil }

func (OpenCodeRuntime) ExtractDebugLog(_, _, _ string) error { return nil }

func (OpenCodeRuntime) ParseTranscriptErrors(_ string) []TranscriptError { return nil }

func (OpenCodeRuntime) ParseTranscriptFile(_ string) (TranscriptError, bool) {
	return TranscriptError{}, false
}

func (OpenCodeRuntime) EmitTranscriptErrors(w io.Writer, summaries []TranscriptError) {
	emitTranscriptErrors(w, summaries)
}

// Compile-time interface assertions.
var (
	_ Runtime           = OpenCodeRuntime{}
	_ TranscriptHandler = OpenCodeRuntime{}
)
