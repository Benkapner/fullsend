package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvalMeasureCmd_ScoresFixture(t *testing.T) {
	out := t.TempDir()
	telemetry := filepath.Join("..", "evalmeasure", "testdata", "complete.jsonl")
	registry := filepath.Join("..", "evalmeasure", "testdata", "sample-registry.yaml")

	cmd := newRootCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{
		"eval-measure",
		"--telemetry", telemetry,
		"--registry", registry,
		"--out-dir", out,
	})
	err := cmd.Execute()
	require.NoError(t, err)

	b, err := os.ReadFile(filepath.Join(out, "eval-measurements.jsonl"))
	require.NoError(t, err)
	assert.Contains(t, string(b), `"name":"trace_fitness"`)
}

func TestRootCommand_HasEvalMeasureSubcommand(t *testing.T) {
	cmd := newRootCmd()
	found := false
	for _, sub := range cmd.Commands() {
		if sub.Use == "eval-measure" {
			found = true
			break
		}
	}
	assert.True(t, found, "expected eval-measure subcommand")
}

func TestEvalMeasureCmd_MissingRequiredFlags(t *testing.T) {
	cmd := newRootCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"eval-measure"})
	err := cmd.Execute()
	require.Error(t, err)
}
