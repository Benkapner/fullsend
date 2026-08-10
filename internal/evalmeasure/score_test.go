package evalmeasure

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScoreFitness_CompletePass(t *testing.T) {
	t.Parallel()
	traces, err := ParseTelemetryFile(filepath.Join("testdata", "complete.jsonl"))
	require.NoError(t, err)
	r := ScoreFitness(traces[0])
	assert.Equal(t, ScorerFitness, r.Name)
	assert.Equal(t, "em-001@1", r.Version)
	assert.Equal(t, "pass", r.Label)
	assert.Equal(t, 1.0, r.Value)
	assert.Equal(t, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", r.TraceID)
	assert.Contains(t, r.Explanation, "span_tree=pass")
	assert.Contains(t, r.Explanation, "cost_tools_turns=pass")
	assert.Contains(t, r.Explanation, "exit=pass")
	assert.NotContains(t, r.Explanation, "=fail")
}

func TestScoreFitness_MissingCostFails(t *testing.T) {
	t.Parallel()
	traces, err := ParseTelemetryFile(filepath.Join("testdata", "missing-cost.jsonl"))
	require.NoError(t, err)
	r := ScoreFitness(traces[0])
	assert.Equal(t, "fail", r.Label)
	assert.Less(t, r.Value, 1.0)
	assert.Contains(t, r.Explanation, "cost_tools_turns")
	assert.Contains(t, r.Explanation, "cost_tools_turns=fail")
}

func TestScoreFitness_ReviewUnknownWorkItemFails(t *testing.T) {
	t.Parallel()
	traces, err := ParseTelemetryFile(filepath.Join("testdata", "review-unknown-workitem.jsonl"))
	require.NoError(t, err)
	r := ScoreFitness(traces[0])
	assert.Equal(t, "review", r.Agent)
	assert.Equal(t, "fail", r.Label)
	assert.Contains(t, r.Explanation, "identity=pass")
	assert.Contains(t, r.Explanation, "work_item=fail")
	assert.Contains(t, r.Explanation, "missing: work_item")
}
