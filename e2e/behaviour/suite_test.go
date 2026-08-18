//go:build behaviour

package behaviour_test

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/cucumber/godog"
	"github.com/google/uuid"

	gaci "github.com/fullsend-ai/fullsend/pkg/behaviourtest/drivers/ci/githubactions"
	"github.com/fullsend-ai/fullsend/pkg/behaviourtest/drivers/env"
	"github.com/fullsend-ai/fullsend/pkg/behaviourtest/drivers/install/cfmint"
	scmgh "github.com/fullsend-ai/fullsend/pkg/behaviourtest/drivers/scm/github"
	"github.com/fullsend-ai/fullsend/pkg/behaviourtest/suite"
	"github.com/fullsend-ai/fullsend/pkg/behaviourtest/world"
	"github.com/fullsend-ai/fullsend/pkg/e2etest"
)

// poolSize is the number of enrolled test-repo-NN repos in the pool org.
// GODOG_CONCURRENCY should not exceed this — extra workers will block in
// AllocateRepo with no warning because the pool org only has test-repo-01
// through test-repo-12 with per-repo mint enrollment.
const poolSize = 12

// suiteName identifies this test suite. It is used to derive the CF Worker
// name so that different suites get different workers.
const suiteName = "bt"

func TestBehaviourSuite(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping behaviour tests in short mode")
	}

	concurrency := poolSize
	if c := os.Getenv("GODOG_CONCURRENCY"); c != "" {
		n, err := strconv.Atoi(c)
		if err != nil || n < 1 {
			t.Fatalf("GODOG_CONCURRENCY must be a positive integer, got %q", c)
		}
		concurrency = n
	}

	cfg := env.LoadRunnerConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("invalid behaviour runner config: %v", err)
	}

	e2eCfg := e2etest.LoadEnvConfig(t)
	ctx := context.Background()

	runID := uuid.New().String()
	org, token, err := e2etest.AcquireOrg(ctx, e2eCfg, runID, e2etest.OrgPool(), e2eCfg.LockTimeout, t.Logf)
	if err != nil {
		t.Fatalf("acquiring org: %v", err)
	}
	client := e2etest.NewLiveClient(token)
	t.Cleanup(func() {
		e2etest.ReleaseLock(context.Background(), client, org, runID, t)
	})

	binary := e2etest.BuildCLIBinary(t)

	// Construct a cfmint Factory that closes over PEM/pool config.
	// When called, the factory deploys the preview mint, creates a
	// RepoEnsurer with the deployed mint URL, and returns a unified
	// install.Driver that owns allocation, deallocation, and teardown.
	factory := cfmint.NewFactory(cfmint.Config{
		PEMDir:            e2eCfg.CFMintPEMDir,
		SuiteName:         suiteName,
		AllowedOrgs:       "",
		PerRepoWIFRepos:   buildPerRepoWIFRepos(org),
		WorkflowHostRepos: buildWorkflowHostRepos(org),
		AppSet:            "fullsend-test",
	}, poolSize)

	e2etest.CleanupStaleResources(ctx, client, token, org, t)

	// Call the factory to get the unified driver. The factory deploys
	// the preview mint and constructs all internal pieces (pool, ensurer).
	driver, err := factory(org, client, token, binary, e2eCfg.GCPProjectID, t.Logf)
	if err != nil {
		t.Fatalf("creating install driver: %v", err)
	}

	// Register Finalize as cleanup so the preview mint is torn down
	// even if the suite fails partway through. Finalize also reclaims
	// any outstanding leases, logging them as errors.
	t.Cleanup(func() {
		if finalizeErr := driver.Finalize(context.Background()); finalizeErr != nil {
			t.Logf("driver finalize: %v", finalizeErr)
		}
	})

	// Advisory warning when concurrency exceeds capacity. Per #6135,
	// this must not fail the run — excess workers block in AllocateRepo.
	if concurrency > driver.Capacity() {
		t.Logf("WARNING: GODOG_CONCURRENCY=%d exceeds driver capacity %d; excess workers will block in AllocateRepo", concurrency, driver.Capacity())
	}

	template := &world.World{
		Config:       cfg,
		SCM:          scmgh.New(client),
		CI:           gaci.New(client, token),
		Driver:       driver,
		Org:          org,
		Token:        token,
		Logf:         t.Logf,
		FixturesRoot: "e2e/behaviour",
		RepoOwner:    org,
	}

	suiteRunner := godog.TestSuite{
		Name:                "behaviour",
		ScenarioInitializer: func(sc *godog.ScenarioContext) { suite.InitScenario(sc, template) },
		Options: &godog.Options{
			Format:      "pretty",
			Paths:       []string{"features"},
			TestingT:    t,
			Tags:        os.Getenv("GODOG_TAGS"),
			Concurrency: concurrency,
		},
	}
	if st := suiteRunner.Run(); st != 0 {
		t.Fatalf("behaviour suite failed with status %d", st)
	}
}

// buildPerRepoWIFRepos constructs the --per-repo-wif-repos value from
// the pool org and the standard BT repo naming convention.
func buildPerRepoWIFRepos(org string) string {
	repos := make([]string, poolSize)
	for i := range poolSize {
		repos[i] = fmt.Sprintf("%s/test-repo-%02d", org, i+1)
	}
	return strings.Join(repos, ",")
}

// buildWorkflowHostRepos constructs the --workflow-host-repos value.
// These are the repos whose vendored workflows are allowed to mint
// tokens. Only numbered pool repos (test-repo-01 … test-repo-12)
// are included — singular test-repo is reserved for admin e2e.
func buildWorkflowHostRepos(org string) string {
	repos := make([]string, 0, poolSize)
	for i := range poolSize {
		repos = append(repos, fmt.Sprintf("%s/test-repo-%02d", org, i+1))
	}
	return strings.Join(repos, ",")
}
