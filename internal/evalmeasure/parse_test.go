package evalmeasure

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTelemetryFile_Complete(t *testing.T) {
	t.Parallel()
	traces, err := ParseTelemetryFile(filepath.Join("testdata", "complete.jsonl"))
	require.NoError(t, err)
	require.Len(t, traces, 1)
	tr := traces[0]
	assert.Equal(t, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", tr.TraceID)
	run, ok := tr.SpanByName("run")
	require.True(t, ok)
	got, ok := run.AttrString("fullsend.agent")
	require.True(t, ok)
	assert.Equal(t, "triage", got)
	cost, ok := run.AttrFloat("fullsend.cost_usd")
	require.True(t, ok)
	assert.InDelta(t, 0.54, cost, 1e-9)
	assert.Len(t, tr.SpansByName("agent"), 1)
	assert.InDelta(t, 6.0, run.DurationSeconds(), 1e-9)
}

func TestParseTelemetryFile_MergesLinesSameTrace(t *testing.T) {
	t.Parallel()
	traces, err := ParseTelemetryFile(filepath.Join("testdata", "split.jsonl"))
	require.NoError(t, err)
	require.Len(t, traces, 1)
	assert.Len(t, traces[0].Spans, 3)
	_, ok := traces[0].SpanByName("sandbox_create")
	assert.True(t, ok)
}

func TestParseTelemetryFile_InvalidLine(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.jsonl")
	require.NoError(t, os.WriteFile(path, []byte("not-json\n"), 0o644))
	_, err := ParseTelemetryFile(path)
	require.Error(t, err)
}

func TestParseTelemetryFile_MissingFile(t *testing.T) {
	t.Parallel()
	_, err := ParseTelemetryFile(filepath.Join(t.TempDir(), "missing.jsonl"))
	require.Error(t, err)
}
