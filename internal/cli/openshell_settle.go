package cli

import (
	"fmt"
	"time"

	"github.com/fullsend-ai/fullsend/internal/ui"
)

// openShellFirstPollSettle is how long after a sandbox becomes Ready the
// runner waits before the sandbox's first network request.
//
// OpenShell 0.1.2's supervisor polls its settings every 10 s
// (OPENSHELL_POLICY_POLL_INTERVAL_SECS, default 10). Its first poll treats the
// unchanged provider environment as changed, reloads the policy and
// terminates connections in flight (NVIDIA/OpenShell#3809, fixed by #3819
// after 0.1.2). A request that is open at that moment fails: the GitHub
// pre-flight check sees EOF, an agent's first model request sees
// "terminated". Waiting past that first poll keeps both clear of it; the 2 s
// over the poll interval covers the supervisor's startup before the loop.
//
// Remove this once fullsend pins an OpenShell release that includes #3819.
const openShellFirstPollSettle = 12 * time.Second

// Test seams.
var (
	settleNow   = time.Now
	settleSleep = time.Sleep
)

// waitForOpenShellFirstPoll sleeps for whatever remains of
// openShellFirstPollSettle since readyAt and returns how long it waited.
// It returns immediately when that much time has already passed, so slow
// bootstraps pay nothing.
func waitForOpenShellFirstPoll(readyAt time.Time, printer *ui.Printer) time.Duration {
	remaining := openShellFirstPollSettle - settleNow().Sub(readyAt)
	if remaining <= 0 {
		return 0
	}
	if printer != nil {
		printer.StepInfo(fmt.Sprintf("Waiting %.1fs for the sandbox's first policy poll (OpenShell #3809)", remaining.Seconds()))
	}
	settleSleep(remaining)
	return remaining
}
