package install

import (
	"context"

	"github.com/fullsend-ai/fullsend/internal/forge"
)

// MintDriver provisions and tears down fullsend in an acquired pool org.
// MintDriver is used only during suite setup (single-threaded) and is not
// shared across concurrent scenarios.
//
// Renamed from Driver to free the Driver name for the unified surface
// that owns both mint lifecycle and repo allocation (#6169). Transitional
// until #6170 folds pool/ensurer into concrete driver types.
type MintDriver interface {
	Install(ctx context.Context, org string) (State, error)
	Teardown(ctx context.Context, org string, state State) error
}

// Factory constructs a unified Driver for a given org. Driver-specific
// inputs (PEMs, allowlists, pool size) are closed over by the factory
// function. Factory performs suite setup (e.g. preview mint deploy)
// before returning so setup failures fail the suite before scenarios run.
type Factory func(
	org string,
	client forge.Client,
	token, binary, gcpProjectID string,
	logf func(string, ...any),
) (Driver, error)

// Driver owns mint/environment lifecycle and test-repo allocation for
// behaviour tests. The suite constructs exactly one Driver via a Factory
// and threads it through World; scenarios call AllocateRepo to lease a
// ready repo and DeallocateRepo to return it. Finalize tears down
// suite-scoped resources and reclaims any outstanding leases.
//
// Implementations must be safe for concurrent use by multiple godog
// scenarios (GODOG_CONCURRENCY > 1).
type Driver interface {
	// AllocateRepo leases a slot and makes that repo ready (create if
	// missing, install if needed). Blocks until a slot is free or ctx
	// is cancelled. Returns the repo name only (org is fixed for the
	// driver / World).
	AllocateRepo(ctx context.Context) (repoName string, err error)

	// DeallocateRepo returns a previously allocated repo. Errors on
	// unknown name or double-release.
	DeallocateRepo(ctx context.Context, repoName string) error

	// Finalize always tears down suite-scoped resources (e.g. preview
	// mint). If leases are still outstanding, it reclaims them (logging
	// the names), completes teardown, and returns an error so leaked
	// After-hooks fail CI without stranding resources.
	Finalize(ctx context.Context) error

	// Capacity is the max concurrent outstanding allocations (the
	// driver's real parallelism ceiling). Suite may default concurrency
	// to Capacity() or honor GODOG_CONCURRENCY. If concurrency exceeds
	// Capacity(), excess workers block in AllocateRepo — the suite
	// emits an advisory warning but does not fail.
	Capacity() int
}

// State describes where behaviour tests find fullsend configuration after install.
//
// Concurrency: the PerRepoState implementation is a read-only snapshot
// whose fields (org, repo, mintURL) are set at construction and never modified.
// All accessor methods return derived constants. Sharing a single State
// across goroutines via World.Clone is safe by design for
// GODOG_CONCURRENCY>1. TestConcurrentStateAccess in this package
// exercises concurrent reads under -race.
//
// If a future implementation adds mutable state, it must synchronize
// access or be deep-copied per scenario in World.Clone.
type State interface {
	Mode() string
	// ConfigOwner and ConfigRepo locate commits for behaviour scripts and config reads.
	ConfigOwner() string
	ConfigRepo() string
	// ConfigPathPrefix is "" for per-org (.fullsend repo root) or ".fullsend" for per-repo.
	ConfigPathPrefix() string
	// TriageWorkflowRepo is the repository polled for triage workflow runs.
	TriageWorkflowRepo() string
	// TriageWorkflowFile is the workflow path passed to ListWorkflowRuns.
	TriageWorkflowFile() string
	// AgentWorkflowFile is the reusable workflow that runs the agent and uploads artifacts.
	AgentWorkflowFile() string
	// AgentArtifactName is the upload-artifact name for triage agent output.
	AgentArtifactName() string
}

// MintURLProvider is optionally implemented by State values that carry
// the effective mint URL. The suite uses this to thread the mint URL
// from the install driver to the RepoEnsurer.
type MintURLProvider interface {
	MintURL() string
}

// CLIRunnerFunc is the signature for running a fullsend CLI command.
// The default implementation is e2etest.TryRunCLI. Inject a custom
// function in tests to avoid shelling out.
type CLIRunnerFunc func(binary, token string, args ...string) (string, error)

const (
	// PerRepoTriageWorkflow is the workflow path for per-repo triage.
	PerRepoTriageWorkflow = "fullsend.yaml"

	// PerRepoAgentWorkflow is the reusable workflow for the triage agent.
	PerRepoAgentWorkflow = "reusable-triage.yml"

	// PerRepoAgentArtifact is the upload-artifact name for triage output.
	PerRepoAgentArtifact = "fullsend-triage"
)
