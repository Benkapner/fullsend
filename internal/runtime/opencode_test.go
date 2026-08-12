package runtime

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/fullsend-ai/fullsend/internal/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenCodeRuntimeMetadata(t *testing.T) {
	t.Parallel()

	rt := OpenCodeRuntime{}
	assert.Equal(t, "opencode", rt.Name())
	assert.Equal(t, "opencode", rt.System())
	assert.Equal(t, sandbox.SandboxWorkspace+"/.opencode", rt.ConfigDir()) // provisional — see ConfigDir() comment
	assert.Equal(t, sandbox.SandboxWorkspace, rt.WorkspaceDir())
	assert.Nil(t, rt.EnvExports())
}

func TestOpenCodeRuntimeRun_NotImplemented(t *testing.T) {
	t.Parallel()

	rt := OpenCodeRuntime{}
	exit, err := rt.Run(context.Background(), RunParams{}, nil, time.Now(), nil)
	assert.Equal(t, -1, exit)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not yet implemented")
}

func TestOpenCodeRuntimeBootstrap_NotImplemented(t *testing.T) {
	t.Parallel()

	rt := OpenCodeRuntime{}
	err := rt.Bootstrap(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not yet implemented")
}

func TestOpenCodeRuntimeExtractStubs_NotImplemented(t *testing.T) {
	t.Parallel()

	rt := OpenCodeRuntime{}
	err := rt.ExtractTranscripts("", "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "#1935")

	err = rt.ExtractDebugLog("", "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "#1935")
}

func TestOpenCodeRuntimeNoopMethods(t *testing.T) {
	t.Parallel()

	rt := OpenCodeRuntime{}
	assert.Nil(t, rt.ParseTranscriptErrors(""))
	assert.NoError(t, rt.ClearIterationArtifacts(""))

	te, ok := rt.ParseTranscriptFile("")
	assert.False(t, ok)
	assert.Equal(t, TranscriptError{}, te)

	var buf bytes.Buffer
	rt.EmitTranscriptErrors(&buf, nil)
}
