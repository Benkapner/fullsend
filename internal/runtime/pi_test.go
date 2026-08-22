package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fullsend-ai/fullsend/internal/sandbox"
)

func TestPiRuntimeMetadata(t *testing.T) {
	t.Parallel()
	rt := PiRuntime{}
	assert.Equal(t, "pi", rt.Name())
	assert.Equal(t, "pi", rt.System())
	assert.Equal(t, sandbox.SandboxPiConfig, rt.ConfigDir())
	assert.Equal(t, sandbox.SandboxWorkspace, rt.WorkspaceDir())
	// Config dir must be outside the agent-writable workspace tree.
	assert.False(t, strings.HasPrefix(rt.ConfigDir(), sandbox.SandboxWorkspace))
}

func TestPiRuntimeEnvExports(t *testing.T) {
	t.Parallel()
	exports := strings.Join(PiRuntime{}.EnvExports(), "\n")
	assert.Contains(t, exports, "PI_CODING_AGENT_DIR="+sandbox.SandboxPiConfig)
	assert.Contains(t, exports, "PI_CODING_AGENT_SESSION_DIR="+sandbox.SandboxPiConfig+"/sessions")
	assert.Contains(t, exports, "PI_OFFLINE=1")
	assert.Contains(t, exports, "PI_SKIP_VERSION_CHECK=1")
}

func TestPiRuntimeRun_NotImplemented(t *testing.T) {
	t.Parallel()
	exit, err := PiRuntime{}.Run(context.Background(), RunParams{}, nil, time.Now(), nil)
	assert.Equal(t, -1, exit)
	require.ErrorContains(t, err, "not yet implemented")
}

func TestPiRuntimeBootstrap_NotImplemented(t *testing.T) {
	t.Parallel()
	require.ErrorContains(t, PiRuntime{}.Bootstrap(bootstrapInput{}), "not yet implemented")
}

func TestPiRuntimeExtractStubs_NotImplemented(t *testing.T) {
	t.Parallel()
	rt := PiRuntime{}
	require.ErrorContains(t, rt.ExtractTranscripts("sb", "agent", t.TempDir()), "not implemented")
	require.ErrorContains(t, rt.ExtractDebugLog("sb", "/tmp/x", "*"), "not implemented")
}

func TestPiRuntimeNoopMethods(t *testing.T) {
	t.Parallel()
	rt := PiRuntime{}
	assert.NoError(t, rt.ClearIterationArtifacts("sb"))
	assert.Nil(t, rt.ParseTranscriptErrors(t.TempDir()))
	_, ok := rt.ParseTranscriptFile("/nonexistent")
	assert.False(t, ok)
	var sb strings.Builder
	rt.EmitTranscriptErrors(&sb, nil)
}

func TestPiRuntimeCapabilities(t *testing.T) {
	t.Parallel()
	// pi reads AGENTS.md natively — no CLAUDE.md bridge.
	assert.False(t, WantsClaudeMDBridge(PiRuntime{}))
	// No DebugLogNamer yet — falls back to the runtime-neutral default.
	assert.Equal(t, DefaultDebugLogName, DebugLogNameFor(PiRuntime{}))
}
