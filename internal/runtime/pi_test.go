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

func TestPiRuntimeMetadata(t *testing.T) {
	t.Parallel()

	rt := PiRuntime{}
	assert.Equal(t, "pi", rt.Name())
	assert.Equal(t, "pi", rt.System())
	assert.Equal(t, sandbox.SandboxWorkspace+"/.pi", rt.ConfigDir())
	assert.Equal(t, sandbox.SandboxWorkspace, rt.WorkspaceDir())
	assert.Nil(t, rt.EnvExports())
}

func TestPiRuntimeRun_NotImplemented(t *testing.T) {
	t.Parallel()

	rt := PiRuntime{}
	exit, err := rt.Run(context.Background(), RunParams{}, nil, time.Now(), nil)
	assert.Equal(t, -1, exit)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not yet implemented")
	assert.Contains(t, err.Error(), "#6464")
}

func TestPiRuntimeBootstrap_NotImplemented(t *testing.T) {
	t.Parallel()

	rt := PiRuntime{}
	err := rt.Bootstrap(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not yet implemented")
	assert.Contains(t, err.Error(), "#6464")
}

func TestPiRuntimeExtractStubs_NotImplemented(t *testing.T) {
	t.Parallel()

	rt := PiRuntime{}
	err := rt.ExtractTranscripts("", "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "#6464")

	err = rt.ExtractDebugLog("", "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "#6464")
}

func TestPiRuntimeNoopMethods(t *testing.T) {
	t.Parallel()

	rt := PiRuntime{}
	assert.Nil(t, rt.ParseTranscriptErrors(""))
	assert.NoError(t, rt.ClearIterationArtifacts(""))

	te, ok := rt.ParseTranscriptFile("")
	assert.False(t, ok)
	assert.Equal(t, TranscriptError{}, te)

	var buf bytes.Buffer
	rt.EmitTranscriptErrors(&buf, nil)
}
