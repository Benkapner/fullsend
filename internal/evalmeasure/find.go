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
// It never walks deeper, so an agent-planted
// iteration-N/output/run-telemetry.jsonl is ignored.
func FindPlatformTelemetry(outputDir, agent string) ([]string, error) {
	direct := filepath.Join(outputDir, PlatformTelemetryFile)
	if st, err := os.Stat(direct); err == nil && !st.IsDir() {
		// outputDir is a runDir: score only the platform file at the top.
		return []string{direct}, nil
	}

	entries, err := os.ReadDir(outputDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	wantAgent := strings.ToLower(agent)
	var bestPath string
	var bestMod time.Time
	found := false
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
		p := filepath.Join(outputDir, e.Name(), PlatformTelemetryFile)
		st, err := os.Stat(p)
		if err != nil || st.IsDir() {
			continue
		}
		if !found || st.ModTime().After(bestMod) {
			bestPath = p
			bestMod = st.ModTime()
			found = true
		}
	}
	if !found {
		return nil, nil
	}
	return []string{bestPath}, nil
}
