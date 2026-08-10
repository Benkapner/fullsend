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
	assert.Equal(t, sandbox.SandboxWorkspace+"/.opencode", rt.ConfigDir())
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

func TestOpenCodeRuntimeNoopMethods(t *testing.T) {
	t.Parallel()

	rt := OpenCodeRuntime{}
	assert.NoError(t, rt.Bootstrap(nil))
	assert.NoError(t, rt.ExtractTranscripts("", "", ""))
	assert.NoError(t, rt.ExtractDebugLog("", "", ""))
	assert.Nil(t, rt.ParseTranscriptErrors(""))
	assert.NoError(t, rt.ClearIterationArtifacts(""))

	te, ok := rt.ParseTranscriptFile("")
	assert.False(t, ok)
	assert.Equal(t, TranscriptError{}, te)

	var buf bytes.Buffer
	rt.EmitTranscriptErrors(&buf, nil)
}
