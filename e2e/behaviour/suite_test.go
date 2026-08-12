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
	"github.com/fullsend-ai/fullsend/pkg/behaviourtest/drivers/install"
	"github.com/fullsend-ai/fullsend/pkg/behaviourtest/drivers/install/cfmint"
	scmgh "github.com/fullsend-ai/fullsend/pkg/behaviourtest/drivers/scm/github"
	"github.com/fullsend-ai/fullsend/pkg/behaviourtest/suite"
	"github.com/fullsend-ai/fullsend/pkg/behaviourtest/world"
	"github.com/fullsend-ai/fullsend/pkg/e2etest"
)

// poolSize is the number of enrolled test-repo-NN repos in the pool org.
// GODOG_CONCURRENCY must not exceed this — extra workers would block in
// pool.Acquire with no warning because the pool org only has test-repo-01
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
	if concurrency > poolSize {
		t.Fatalf("GODOG_CONCURRENCY=%d exceeds repo pool size %d", concurrency, poolSize)
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

	// Construct the CF mint driver with caller-provided parameters.
	// The driver does not hardcode pool size or test-repo-NN assumptions;
	// the calling code passes allowed orgs, per-repo WIF repos, and
	// workflow host repos.
	//
	// AllowedOrgs is explicitly empty ("") — this is per-repo mode, so
	// we must not dual-enroll the pool org as an allowed org. The CLI's
	// explicit-empty semantics clear ALLOWED_ORGS on the Worker.
	//
	// WorkflowHostRepos registers the pool repos whose vendored workflows
	// need to mint tokens. Without this, the mint rejects
	// job_workflow_ref values from pool repos → 401.
	installDriver, err := cfmint.NewDriver(client, token, binary, e2eCfg.GCPProjectID, t.Logf, cfmint.Config{
		PEMDir:            e2eCfg.CFMintPEMDir,
		SuiteName:         suiteName,
		AllowedOrgs:       "",
		PerRepoWIFRepos:   buildPerRepoWIFRepos(org),
		WorkflowHostRepos: buildWorkflowHostRepos(org),
		AppSet:            "fullsend-test",
	})
	if err != nil {
		t.Fatalf("creating install driver: %v", err)
	}

	e2etest.CleanupStaleResources(ctx, client, token, org, t)

	// Register teardown before Install so that a partially deployed
	// preview mint is cleaned up even if Install fails partway through.
	var installState install.State
	t.Cleanup(func() {
		if installState == nil {
			return
		}
		teardownCtx := context.Background()
		if teardownErr := installDriver.Teardown(teardownCtx, org, installState); teardownErr != nil {
			t.Logf("install teardown: %v", teardownErr)
		}
	})

	installState, err = installDriver.Install(ctx, org)
	if err != nil {
		t.Fatalf("installing fullsend on %s: %v", org, err)
	}

	// The install state carries the mint URL from the selected driver.
	// Thread it to the ensurer so additional pool repos use the same
	// mint endpoint.
	if m, ok := installState.(install.MintURLProvider); ok && m.MintURL() != "" {
		t.Logf("using mint URL for ensurer: %s", m.MintURL())
		e2eCfg.MintURL = m.MintURL()
	}

	pool, err := world.NewRepoPool(poolSize)
	if err != nil {
		t.Fatalf("creating repo pool: %v", err)
	}

	ensurer := install.NewRepoEnsurer(e2eCfg, client, token, binary, t.Logf)

	// The install driver only manages the mint lifecycle — it does not
	// install on any specific repo. RepoName and RepoFull are set per-
	// scenario by the ensurer when "Given the enrolled test repository"
	// acquires a leased pool repo.
	template := &world.World{
		Config:       cfg,
		SCM:          scmgh.New(client),
		CI:           gaci.New(client, token),
		Install:      installState,
		Ensurer:      ensurer,
		Org:          org,
		Token:        token,
		Logf:         t.Logf,
		FixturesRoot: "e2e/behaviour",
		RepoOwner:    org,
	}

	suiteRunner := godog.TestSuite{
		Name:                "behaviour",
		ScenarioInitializer: func(sc *godog.ScenarioContext) { suite.InitScenario(sc, template, pool) },
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
