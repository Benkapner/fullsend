package agentnew

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestGeneratedPostScriptFlattensAnInvalidStatusBeforeLogging feeds a status
// that carries a line break (LF, CR alone, or CRLF) and a workflow-command
// prefix. The rejection message must stay on one line and must not reproduce
// the injected line, because the script's stderr lands in the runner log
// where `::` at line start is interpreted as a command.
func TestGeneratedPostScriptFlattensAnInvalidStatusBeforeLogging(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not installed; the generated post-script needs it")
	}
	script := renderPostScriptTo(t, t.TempDir())
	for name, brk := range map[string]string{"LF": "\n", "CR": "\r", "CRLF": "\r\n"} {
		t.Run(name, func(t *testing.T) {
			runDir := writeRunDir(t, map[string]any{
				"iteration-1": map[string]any{
					"status":  "bogus" + brk + "::error::injected",
					"summary": "s",
					"comment": "c",
				},
			})
			_, stderr, err := runPostScript(t, script, runDir)
			if err == nil {
				t.Fatal("expected the script to reject an invalid status")
			}
			if !strings.Contains(stderr, "status must be ok, findings or error") {
				t.Fatalf("expected the status rejection, got stderr:\n%s", stderr)
			}
			for _, line := range strings.FieldsFunc(stderr, func(r rune) bool { return r == '\n' || r == '\r' }) {
				if strings.HasPrefix(line, "::") {
					t.Fatalf("model-supplied status reached the log as its own line: %q", line)
				}
			}
			if strings.ContainsRune(stderr, '\r') || strings.Count(strings.TrimSpace(stderr), "\n") != 0 {
				t.Fatalf("rejection must be a single log line, got: %q", stderr)
			}
		})
	}
}

// TestGeneratedPostScriptDoesNotExpandEscapesUnderXpgEcho runs the script
// with bash's xpg_echo option on, where `echo` interprets backslash escapes.
// A status carrying a literal backslash-n must still be logged on one line.
func TestGeneratedPostScriptDoesNotExpandEscapesUnderXpgEcho(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not installed; the generated post-script needs it")
	}
	script := renderPostScriptTo(t, t.TempDir())
	runDir := writeRunDir(t, map[string]any{
		"iteration-1": map[string]any{
			"status":  `bogus\n::error::injected`,
			"summary": "s",
			"comment": "c",
		},
	})
	cmd := exec.Command("bash", "-O", "xpg_echo", script)
	cmd.Dir = runDir
	cmd.Env = append(os.Environ(),
		"ISSUE_URL=https://github.com/fullsend-ai/demo/pull/99",
		"GH_TOKEN=test-token",
		DryRunEnvVar("lint-docs")+"=1",
	)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		t.Fatal("expected the script to reject an invalid status")
	}
	out := strings.TrimSpace(stderr.String())
	if strings.Count(out, "\n") != 0 {
		t.Fatalf("backslash escapes were expanded into a new log line:\n%s", out)
	}
	if !strings.Contains(out, `bogus\n::error::injected`) {
		t.Fatalf("expected the literal value on the single line, got: %q", out)
	}
}

// TestGeneratedPostScriptCapsALongInvalidStatus pins the 40-character cap on
// the logged value, so a long model-supplied status cannot flood the log.
func TestGeneratedPostScriptCapsALongInvalidStatus(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not installed; the generated post-script needs it")
	}
	script := renderPostScriptTo(t, t.TempDir())
	runDir := writeRunDir(t, map[string]any{
		"iteration-1": map[string]any{
			"status":  strings.Repeat("x", 200),
			"summary": "s",
			"comment": "c",
		},
	})
	_, stderr, err := runPostScript(t, script, runDir)
	if err == nil {
		t.Fatal("expected the script to reject an invalid status")
	}
	if !strings.Contains(stderr, "(got '"+strings.Repeat("x", 40)+"')") {
		t.Fatalf("expected the status capped at 40 characters, got: %q", stderr)
	}
}
