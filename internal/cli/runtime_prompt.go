package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/fullsend-ai/fullsend/internal/config"
	"github.com/fullsend-ai/fullsend/internal/ui"
)

// defaultRuntimeChoice is what setup selects when the user does not choose.
const defaultRuntimeChoice = "claude"

// promptRuntime asks which agent runtime the per-repo config should select
// when --runtime was not given. It only prompts on an interactive terminal;
// otherwise (CI, pipes, --dry-run callers that pass interactive=false) it
// returns the default so setup never blocks. Enter or EOF keep the default.
// The choice is written to `runtime:` in .fullsend/config.yaml — the same
// key `fullsend github setup --runtime` and a per-run `--runtime` override.
func promptRuntime(printer *ui.Printer, in io.Reader, interactive bool) (string, error) {
	if !interactive {
		return "", nil
	}
	printer.Header("Agent Runtime")
	printer.Blank()
	printer.StepInfo("Choose the agent runtime for this repository:")
	printer.StepInfo("  [claude] Claude Code — the default; all fleet agents, concurrent sub-agents")
	printer.StepInfo("  [pi]     pi — opt-in; any provider pi supports (Claude, Gemini, …), no sub-agent tool yet")
	printer.StepInfo("  Change later with `runtime:` in .fullsend/config.yaml or `fullsend github setup --runtime`;")
	printer.StepInfo("  see docs/runtimes.md. Press Enter for the default.")
	printer.Blank()

	reader := bufio.NewReader(in)
	for {
		printer.StepInfo(fmt.Sprintf("Enter runtime [%s]: ", defaultRuntimeChoice))
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return "", fmt.Errorf("reading runtime choice: %w", err)
		}
		choice := strings.ToLower(strings.TrimSpace(line))
		if choice == "" {
			return "", nil // keep the default (not written to the overlay)
		}
		if err := validateRuntimeName(choice); err == nil {
			return choice, nil
		}
		printer.StepWarn(fmt.Sprintf("Invalid runtime: %q (expected one of %s)", choice, strings.Join(config.ValidRuntimes(), ", ")))
		if err == io.EOF {
			return "", nil
		}
	}
}

// stdinIsInteractive reports whether stdin is a terminal.
func stdinIsInteractive() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}
