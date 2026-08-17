package evalmeasure

import (
	"os"
	"path/filepath"
)

// PlatformTelemetryFile is the host recorder's JSONL at the top of runDir.
// It matches internal/telemetry.TelemetryFile. Nested copies under
// iteration-N/output/ are agent-writable and must not be scored.
const PlatformTelemetryFile = "run-telemetry.jsonl"

// FindPlatformTelemetry returns run-telemetry.jsonl files that sit at the
// top of outputDir itself (when outputDir is a runDir) or at the top of
// each immediate child directory (when outputDir is the CI output base).
// It never walks deeper, so an agent-planted
// iteration-N/output/run-telemetry.jsonl is ignored.
func FindPlatformTelemetry(outputDir string) ([]string, error) {
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
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(outputDir, e.Name(), PlatformTelemetryFile)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			out = append(out, p)
		}
	}
	return out, nil
}
