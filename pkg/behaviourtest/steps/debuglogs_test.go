package steps

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fullsend-ai/fullsend/internal/forge"
	"github.com/fullsend-ai/fullsend/pkg/behaviourtest/world"
)

func TestSaveWorkflowRunLogs_NilRun(t *testing.T) {
	t.Parallel()
	var logged []string
	w := &world.World{
		Logf: func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) },
	}
	// Should be a no-op — no panic, no log.
	saveWorkflowRunLogs(w, "triage", nil)
	assert.Empty(t, logged)
}

func TestSaveWorkflowRunLogs_WritesLogs(t *testing.T) {
	artifactDir := t.TempDir()
	t.Setenv("BEHAVIOUR_ARTIFACT_DIR", artifactDir)

	var logged []string
	ci := &fakeDebugCI{logs: "=== triage run logs ==="}
	w := &world.World{
		Org:      "org",
		RepoName: "repo",
		CI:       ci,
		Logf:     func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) },
	}

	run := &forge.WorkflowRun{ID: 42}
	saveWorkflowRunLogs(w, "triage", run)

	// Verify the log file was written.
	logPath := filepath.Join(artifactDir, "debug-triage-run-42", "workflow-logs.txt")
	data, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.Equal(t, "=== triage run logs ===", string(data))

	// Verify success was logged.
	require.Len(t, logged, 1)
	assert.Contains(t, logged[0], "logs saved to")
}

func TestSaveWorkflowRunLogs_GetRunLogsError(t *testing.T) {
	artifactDir := t.TempDir()
	t.Setenv("BEHAVIOUR_ARTIFACT_DIR", artifactDir)

	var logged []string
	ci := &fakeDebugCI{logsErr: fmt.Errorf("API error")}
	w := &world.World{
		Org:      "org",
		RepoName: "repo",
		CI:       ci,
		Logf:     func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) },
	}

	run := &forge.WorkflowRun{ID: 99}
	saveWorkflowRunLogs(w, "agent", run)

	// Should log the error, not panic or fail.
	require.Len(t, logged, 1)
	assert.Contains(t, logged[0], "fetch logs")
	assert.Contains(t, logged[0], "API error")

	// No log file should exist.
	logPath := filepath.Join(artifactDir, "debug-agent-run-99", "workflow-logs.txt")
	_, err := os.Stat(logPath)
	assert.True(t, os.IsNotExist(err))
}

func TestSaveWorkflowRunLogs_NilLogf(t *testing.T) {
	artifactDir := t.TempDir()
	t.Setenv("BEHAVIOUR_ARTIFACT_DIR", artifactDir)

	ci := &fakeDebugCI{logs: "log content"}
	w := &world.World{
		Org:      "org",
		RepoName: "repo",
		CI:       ci,
		// Logf deliberately nil — worldLogf guards it.
	}

	run := &forge.WorkflowRun{ID: 1}
	// Should not panic even with nil Logf.
	saveWorkflowRunLogs(w, "triage", run)

	logPath := filepath.Join(artifactDir, "debug-triage-run-1", "workflow-logs.txt")
	data, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.Equal(t, "log content", string(data))
}

func TestPrepareDebugDir_WithArtifactDir(t *testing.T) {
	artifactDir := t.TempDir()
	t.Setenv("BEHAVIOUR_ARTIFACT_DIR", artifactDir)

	dir, err := prepareDebugDir("triage", 123)
	require.NoError(t, err)

	expected := filepath.Join(artifactDir, "debug-triage-run-123")
	assert.Equal(t, expected, dir)
	assert.DirExists(t, dir)
}

func TestPrepareDebugDir_WithoutArtifactDir(t *testing.T) {
	t.Setenv("BEHAVIOUR_ARTIFACT_DIR", "")

	dir, err := prepareDebugDir("agent", 456)
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	assert.DirExists(t, dir)
	assert.Contains(t, filepath.Base(dir), "debug-agent-run-456")
}

// fakeDebugCI implements ci.Driver for debug log tests.
type fakeDebugCI struct {
	logs    string
	logsErr error
}

func (f *fakeDebugCI) GetRunLogs(_ context.Context, _, _ string, _ int) (string, error) {
	return f.logs, f.logsErr
}

func (f *fakeDebugCI) WaitForWorkflow(_ context.Context, _, _, _ string, _ time.Time, _ string) (*forge.WorkflowRun, error) {
	return nil, nil
}

func (f *fakeDebugCI) FindCompletedWorkflowRun(_ context.Context, _, _, _ string, _ time.Time) (*forge.WorkflowRun, error) {
	return nil, nil
}

func (f *fakeDebugCI) AssertNoWorkflow(_ context.Context, _, _, _ string, _ time.Time) error {
	return nil
}

func (f *fakeDebugCI) DownloadArtifacts(_ context.Context, _, _ string, _ int, _ string) error {
	return nil
}

func (f *fakeDebugCI) DownloadNamedArtifactFromRun(_ context.Context, _, _ string, _ int, _, _ string) error {
	return nil
}

func (f *fakeDebugCI) DownloadNamedArtifactAfter(_ context.Context, _, _, _ string, _ time.Time, _ string) error {
	return nil
}

func (f *fakeDebugCI) WaitForHarnessAgent(_ context.Context, _, _, _ string, _ time.Time) (*forge.WorkflowRun, error) {
	return nil, nil
}

func (f *fakeDebugCI) WaitForFailedHarnessAgent(_ context.Context, _, _, _ string, _ time.Time) (*forge.WorkflowRun, error) {
	return nil, nil
}

func (f *fakeDebugCI) AssertNoHarnessAgentArtifact(_ context.Context, _, _, _ string, _ time.Time) error {
	return nil
}

func (f *fakeDebugCI) CountHarnessDispatches(_ context.Context, _, _, _ string, _ time.Time) (int, error) {
	return 0, nil
}
