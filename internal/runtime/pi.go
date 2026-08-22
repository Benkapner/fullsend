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
// lifecycle methods are no-ops or return not-implemented errors; the
// `pi --mode json` stream parser (parsePiStream, pi_progress.go) is in place
// but not yet wired. Subsequent PRs will fill in bootstrap, run execution,
// and transcript extraction; tool-name translation for the hook adapter
// waits on #608, and hook wiring follows the --settings pattern from #6358.
// Tracked in #6464.
type PiRuntime struct{}

// PiVertexExtensionPath is the interim Claude-on-Vertex provider for pi
// (twoGiants/pi-anthropic-vertex, pinned in the sandbox image by
// PI_ANTHROPIC_VERTEX_VERSION). pi's built-in google-vertex provider is
// Gemini-only and the upstream anthropic-vertex provider
// (earendil-works/pi#5262) is still open. Run will load it with
// `-e` alongside `--no-extensions`; it registers provider "anthropic-vertex".
// Project comes from GOOGLE_CLOUD_PROJECT, GCLOUD_PROJECT,
// ANTHROPIC_VERTEX_PROJECT_ID or GOOGLE_CLOUD_PROJECT_ID (first set wins —
// Run should export GOOGLE_CLOUD_PROJECT explicitly), region from
// CLOUD_ML_REGION or GOOGLE_CLOUD_LOCATION. Its bundled Anthropic SDK also
// honours ANTHROPIC_API_KEY / ANTHROPIC_AUTH_TOKEN / ANTHROPIC_BASE_URL /
// ANTHROPIC_VERTEX_BASE_URL, which Run must not forward. Swap for the
// upstream provider once #5262 ships in a pinned pi release.
const PiVertexExtensionPath = sandbox.SandboxPiExtensionsDir + "/anthropic-vertex"

func (PiRuntime) Name() string { return "pi" }

// System returns the OTEL GenAI gen_ai.system value. Pi is multi-provider
// (anthropic, google-vertex, community extensions), so the system is the
// runtime itself rather than a single model vendor (same precedent as
// OpenCodeRuntime). The actual model vendor is on AssistantMessage.provider
// once Bootstrap/Run consume the stream.
func (PiRuntime) System() string { return "pi" }

// ConfigDir returns the pi config directory inside the sandbox. It is
// exported to the agent process as PI_CODING_AGENT_DIR (see EnvExports) and
// lives outside the agent-writable workspace so the target repo cannot
// rewrite the runtime's settings, extensions, or skills. Session JSONL
// storage is a separate path (PI_CODING_AGENT_SESSION_DIR, overridden by
// --session-dir), pinned under it by EnvExports.
func (PiRuntime) ConfigDir() string { return sandbox.SandboxPiConfig }

func (PiRuntime) WorkspaceDir() string { return sandbox.SandboxWorkspace }

// EnvExports pins pi's config and session locations to runner-owned paths
// and disables all startup network traffic (update checks, package update
// checks, telemetry). PI_OFFLINE does not affect the inference call itself;
// PI_TELEMETRY=0 additionally drops pi's provider attribution headers.
// Var names/semantics per earendil-works/pi docs/environment-variables.md
// (PI_CODING_AGENT_DIR, PI_CODING_AGENT_SESSION_DIR, PI_OFFLINE,
// PI_SKIP_VERSION_CHECK, PI_TELEMETRY) — re-verify against that doc when
// PI_VERSION moves. The sandbox image bakes the same values as ENV defaults
// for ad-hoc invocations (images/sandbox/Containerfile).
func (r PiRuntime) EnvExports() []string {
	return []string{
		fmt.Sprintf("export PI_CODING_AGENT_DIR=%s", r.ConfigDir()),
		fmt.Sprintf("export PI_CODING_AGENT_SESSION_DIR=%s/sessions", r.ConfigDir()),
		"export PI_OFFLINE=1",
		"export PI_SKIP_VERSION_CHECK=1",
		"export PI_TELEMETRY=0",
	}
}

func (PiRuntime) Bootstrap(_ BootstrapInput) error {
	return fmt.Errorf("pi runtime is not yet implemented (see #6464)")
}

func (PiRuntime) Run(_ context.Context, _ RunParams, _ *ui.Printer, _ time.Time, _ *RunMetrics) (int, error) {
	return -1, fmt.Errorf("pi runtime is not yet implemented (see #6464)")
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
