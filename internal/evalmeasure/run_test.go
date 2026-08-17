package evalmeasure

import (
	"bytes"
	"context"
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

	traces, _, err := ParseTelemetryFile(filepath.Join("testdata", "complete.jsonl"))
	require.NoError(t, err)
	results := ScoreTrace(traces[0], reg)
	require.Len(t, results, 1)
	assert.Equal(t, "trace_fitness", results[0].Name)
	assert.Equal(t, "em-001@1", results[0].Version)
	assert.Equal(t, "pass", results[0].Label)
}

func TestMeasureFile_Idempotent(t *testing.T) {
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

func TestMeasureFile_AppendBeforeLedger(t *testing.T) {
	out := t.TempDir()
	telemetry := filepath.Join("testdata", "complete.jsonl")
	registry := filepath.Join("testdata", "sample-registry.yaml")
	ledgerPath := filepath.Join(out, LedgerFile)

	first, err := MeasureFile(telemetry, registry, out)
	require.NoError(t, err)
	require.Len(t, first, 1)

	require.NoError(t, os.Remove(ledgerPath))

	second, err := MeasureFile(telemetry, registry, out)
	require.NoError(t, err)
	require.Len(t, second, 1, "retry after missing ledger should re-score")

	lines, err := os.ReadFile(filepath.Join(out, MeasurementsFile))
	require.NoError(t, err)
	assert.Equal(t, 2, bytes.Count(lines, []byte("\n")), "missing ledger may duplicate JSONL lines; consumers dedupe on trace_id+version")
}

func TestMeasureFile_BadRegistry(t *testing.T) {
	_, err := MeasureFile(
		filepath.Join("testdata", "complete.jsonl"),
		filepath.Join(t.TempDir(), "missing.yaml"),
		t.TempDir(),
	)
	require.Error(t, err)
}

func TestMeasureFile_BadTelemetry(t *testing.T) {
	_, err := MeasureFile(
		filepath.Join(t.TempDir(), "missing.jsonl"),
		filepath.Join("testdata", "sample-registry.yaml"),
		t.TempDir(),
	)
	require.Error(t, err)
}

func TestMeasureAndExport_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := MeasureAndExport(
		ctx,
		filepath.Join("testdata", "complete.jsonl"),
		filepath.Join("testdata", "sample-registry.yaml"),
		t.TempDir(),
	)
	require.Error(t, err)
}

func TestMeasureFile_PrescriptSkippedRecordsSkip(t *testing.T) {
	out := t.TempDir()
	results, err := MeasureFile(
		filepath.Join("testdata", "prescript-skipped.jsonl"),
		filepath.Join("testdata", "sample-registry.yaml"),
		out,
	)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, LabelSkip, results[0].Label)
	assert.NotEqual(t, LabelFail, results[0].Label)

	b, err := os.ReadFile(filepath.Join(out, MeasurementsFile))
	require.NoError(t, err)
	assert.Contains(t, string(b), `"label":"skip"`)
	assert.NotContains(t, string(b), `"label":"fail"`)
}

func TestMeasureFile_EmptyIdentityPersistsFailRow(t *testing.T) {
	dir := t.TempDir()
	telem := filepath.Join(dir, "run-telemetry.jsonl")
	// Minimal OTLP line: run span with no agent identity.
	line := `{"resourceSpans":[{"scopeSpans":[{"spans":[{"traceId":"dddddddddddddddddddddddddddddddd","spanId":"1111111111111111","name":"run","startTimeUnixNano":"1","endTimeUnixNano":"2","attributes":[{"key":"fullsend.work_item_id","value":{"stringValue":"acme/demo#1"}},{"key":"exit_code","value":{"intValue":"0"}}]},{"traceId":"dddddddddddddddddddddddddddddddd","spanId":"2222222222222222","name":"sandbox_create","startTimeUnixNano":"1","endTimeUnixNano":"2"},{"traceId":"dddddddddddddddddddddddddddddddd","spanId":"3333333333333333","name":"agent","startTimeUnixNano":"1","endTimeUnixNano":"2","attributes":[{"key":"gen_ai.system","value":{"stringValue":"anthropic"}}]}]}]}]}` + "\n"
	require.NoError(t, os.WriteFile(telem, []byte(line), 0o644))

	out := t.TempDir()
	results, err := MeasureFile(telem, filepath.Join("testdata", "sample-registry.yaml"), out)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, LabelFail, results[0].Label)
	assert.Contains(t, results[0].Explanation, "identity=fail")

	b, err := os.ReadFile(filepath.Join(out, MeasurementsFile))
	require.NoError(t, err)
	assert.Contains(t, string(b), `"label":"fail"`)
	assert.Contains(t, string(b), "identity=fail")
}
