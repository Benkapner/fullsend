package evalmeasure

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// PlatformTelemetryFile is the host recorder's JSONL at the top of runDir.
// It matches internal/telemetry.TelemetryFile. Nested copies under
// iteration-N/output/ are agent-writable and must not be scored.
const PlatformTelemetryFile = "run-telemetry.jsonl"

// hostRunDirPattern matches agent-<name>-<pid>-<unix> from fullsend run.
// name is lowercased when the sandbox is created; charset matches run
// (ToLower only), so pid/unix must be the trailing numeric pair.
var hostRunDirPattern = regexp.MustCompile(`^agent-(.+)-([0-9]+)-([0-9]+)$`)

// FindPlatformTelemetry returns run-telemetry.jsonl files that sit at the
// top of outputDir itself (when outputDir is a runDir) or at the top of
// a host-created child runDir (when outputDir is the CI output base).
//
// Host runDirs are named agent-<name>-<pid>-<unix> (see fullsend run).
// When agent is non-empty, only children matching that exact shape for
// the lowercased agent name are considered (so agent-code does not match
// agent-code-review-…). If several match, only the newest platform file
// is scored — leftover sibling directories from a previous job are ignored.
//
// Matching child runDirs outrank a root-level run-telemetry.jsonl under
// outputDir so an agent-planted file at the CI output base cannot displace
// the real host recorder. If any matching child runDir exists (even
// without a platform file yet), the root file is ignored. Nested
// iteration-N/output/ copies are never walked.
func FindPlatformTelemetry(outputDir, agent string) ([]string, error) {
	wantAgent := strings.ToLower(agent)
	paths, sawMatch, err := findChildPlatformTelemetry(outputDir, wantAgent)
	if err != nil {
		return nil, err
	}
	if sawMatch {
		return paths, nil
	}

	direct := filepath.Join(outputDir, PlatformTelemetryFile)
	if st, err := os.Stat(direct); err == nil && !st.IsDir() {
		// outputDir is a runDir (or has only a root-level file and no
		// matching child): score only the platform file at the top.
		return []string{direct}, nil
	}
	return nil, nil
}

// findChildPlatformTelemetry looks for agent-<name>-<pid>-<unix> children.
// sawMatch is true when at least one directory matched the pattern (and
// agent filter), whether or not PlatformTelemetryFile was present.
func findChildPlatformTelemetry(outputDir, wantAgent string) (paths []string, sawMatch bool, err error) {
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	var bestPath string
	var bestMod time.Time
	foundFile := false
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m := hostRunDirPattern.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		if wantAgent != "" && m[1] != wantAgent {
			continue
		}
		sawMatch = true
		p := filepath.Join(outputDir, e.Name(), PlatformTelemetryFile)
		st, err := os.Stat(p)
		if err != nil || st.IsDir() {
			continue
		}
		if !foundFile || st.ModTime().After(bestMod) {
			bestPath = p
			bestMod = st.ModTime()
			foundFile = true
		}
	}
	if !foundFile {
		return nil, sawMatch, nil
	}
	return []string{bestPath}, sawMatch, nil
}
