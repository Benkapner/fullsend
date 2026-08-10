package evalmeasure

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadRegistryAndScoreTrace(t *testing.T) {
	t.Parallel()
	reg, err := LoadRegistry(filepath.Join("testdata", "sample-registry.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "triage", reg.Agent)

	traces, err := ParseTelemetryFile(filepath.Join("testdata", "complete.jsonl"))
	require.NoError(t, err)
	results := ScoreTrace(traces[0], reg)
	require.Len(t, results, 1)
	assert.Equal(t, "trace_fitness", results[0].Name)
	assert.Equal(t, "em-001@1", results[0].Version)
	assert.Equal(t, "pass", results[0].Label)
}

func TestMeasureFile_Idempotent(t *testing.T) {
	t.Setenv("MLFLOW_TRACKING_URI", "")
	t.Setenv("MLFLOW_TRACKING_PASSWORD", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")

	out := t.TempDir()
	telemetry := filepath.Join("testdata", "complete.jsonl")
	registry := filepath.Join("testdata", "sample-registry.yaml")

	first, err := MeasureFile(telemetry, registry, out)
	require.NoError(t, err)
	require.Len(t, first, 1)

	second, err := MeasureFile(telemetry, registry, out)
	require.NoError(t, err)
	assert.Empty(t, second)

	b, err := os.ReadFile(filepath.Join(out, MeasurementsFile))
	require.NoError(t, err)
	assert.Contains(t, string(b), `"name":"trace_fitness"`)
}
