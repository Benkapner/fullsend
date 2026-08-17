package evalmeasure

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindPlatformTelemetry_IgnoresNestedIterationCopy(t *testing.T) {
	t.Parallel()
	outputDir := t.TempDir()
	runDir := filepath.Join(outputDir, "agent-review-3311-1")
	nested := filepath.Join(runDir, "iteration-1", "output")
	require.NoError(t, os.MkdirAll(nested, 0o755))

	platform := filepath.Join(runDir, PlatformTelemetryFile)
	planted := filepath.Join(nested, PlatformTelemetryFile)
	require.NoError(t, os.WriteFile(platform, []byte("platform\n"), 0o644))
	require.NoError(t, os.WriteFile(planted, []byte("planted\n"), 0o644))

	got, err := FindPlatformTelemetry(outputDir, "")
	require.NoError(t, err)
	require.Equal(t, []string{platform}, got)
}

func TestFindPlatformTelemetry_RunDirIgnoresImmediateChild(t *testing.T) {
	t.Parallel()
	runDir := t.TempDir()
	child := filepath.Join(runDir, "planted-child")
	require.NoError(t, os.MkdirAll(child, 0o755))
	platform := filepath.Join(runDir, PlatformTelemetryFile)
	require.NoError(t, os.WriteFile(platform, []byte("platform\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(child, PlatformTelemetryFile), []byte("planted\n"), 0o644))

	got, err := FindPlatformTelemetry(runDir, "")
	require.NoError(t, err)
	require.Equal(t, []string{platform}, got)
}

func TestFindPlatformTelemetry_RunDirDirect(t *testing.T) {
	t.Parallel()
	runDir := t.TempDir()
	nested := filepath.Join(runDir, "iteration-1", "output")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	platform := filepath.Join(runDir, PlatformTelemetryFile)
	require.NoError(t, os.WriteFile(platform, []byte("platform\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(nested, PlatformTelemetryFile), []byte("planted\n"), 0o644))

	got, err := FindPlatformTelemetry(runDir, "")
	require.NoError(t, err)
	require.Equal(t, []string{platform}, got)
}

func TestFindPlatformTelemetry_MissingDir(t *testing.T) {
	t.Parallel()
	got, err := FindPlatformTelemetry(filepath.Join(t.TempDir(), "nope"), "")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestFindPlatformTelemetry_EmptyWhenOnlyNested(t *testing.T) {
	t.Parallel()
	outputDir := t.TempDir()
	nested := filepath.Join(outputDir, "agent-x", "iteration-1", "output")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(nested, PlatformTelemetryFile), []byte("planted\n"), 0o644))

	got, err := FindPlatformTelemetry(outputDir, "")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestFindPlatformTelemetry_IgnoresSiblingLeftoverRunDir(t *testing.T) {
	t.Parallel()
	outputDir := t.TempDir()
	leftover := filepath.Join(outputDir, "agent-triage-1-1")
	current := filepath.Join(outputDir, "agent-review-9-9")
	require.NoError(t, os.MkdirAll(leftover, 0o755))
	require.NoError(t, os.MkdirAll(current, 0o755))
	leftFile := filepath.Join(leftover, PlatformTelemetryFile)
	curFile := filepath.Join(current, PlatformTelemetryFile)
	require.NoError(t, os.WriteFile(leftFile, []byte("old\n"), 0o644))
	require.NoError(t, os.WriteFile(curFile, []byte("new\n"), 0o644))

	got, err := FindPlatformTelemetry(outputDir, "review")
	require.NoError(t, err)
	require.Equal(t, []string{curFile}, got)

	gotAllNewest, err := FindPlatformTelemetry(outputDir, "")
	require.NoError(t, err)
	require.Len(t, gotAllNewest, 1)
}
