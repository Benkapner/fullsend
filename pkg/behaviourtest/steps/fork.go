package steps

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/fullsend-ai/fullsend/pkg/behaviourtest/world"
)

func registerForkSteps(sc *godog.ScenarioContext) {
	sc.Step(`^a fork "([^"]+)" of the enrolled test repository$`, func(ctx context.Context, forkName string) (context.Context, error) {
		return ctx, givenFork(world.FromContext(ctx), forkName)
	})
	sc.Step(`^a fork pull request is opened$`, func(ctx context.Context) (context.Context, error) {
		return ctx, whenForkPullRequestOpened(world.FromContext(ctx))
	})
	sc.Step(`^a commit is pushed to the fork pull request$`, func(ctx context.Context) (context.Context, error) {
		return ctx, whenCommitPushedToForkPR(world.FromContext(ctx))
	})
	sc.Step(`^the fork pull request is labeled "([^"]+)"$`, func(ctx context.Context, label string) (context.Context, error) {
		return ctx, whenForkPullRequestLabeled(world.FromContext(ctx), label)
	})
}

// forkReadyMaxAttempts is how many times awaitForkReady polls
// GetDefaultBranch before giving up. GitHub's fork API returns
// before Git data is fully replicated; the default-branch ref
// may not be readable immediately.
const forkReadyMaxAttempts = 30

// forkReadyPoll is the delay between GetDefaultBranch polls.
const forkReadyPoll = 2 * time.Second

// givenFork creates a fork of the enrolled test repository if absent, or
// reuses it if it already exists. The fork is created within the same
// organization as the source repository.
//
// After creation, givenFork polls GetDefaultBranch until the fork's
// default branch ref is readable. GitHub's fork API returns before the
// fork's Git data is fully replicated; without this poll, subsequent
// steps (e.g. CreateBranch) can fail with a 409 "Git Repository is
// empty" error under parallel godog concurrency.
//
// When the world uses a leased repo (w.LeasedRepoName is set), the
// logical fork name from the Gherkin feature file is mapped to
// {RepoName}-suffix so the fork targets the correct parent. For example,
// Gherkin "test-repo-fork" with leased "test-repo-07" resolves to
// "test-repo-07-fork". See resolveForkName.
func givenFork(w *world.World, forkName string) error {
	if w.RepoOwner == "" || w.RepoName == "" {
		w.RepoOwner = w.Org
		w.RepoName = w.Install.TestRepo()
		w.RepoFull = w.Org + "/" + w.RepoName
	}

	resolved := resolveForkName(w, forkName)

	ctx := context.Background()
	forkRepo, err := w.SCM.CreateFork(ctx, w.RepoOwner, w.RepoName, resolved)
	if err != nil {
		return fmt.Errorf("creating fork %q: %w", resolved, err)
	}
	w.ForkOwner = w.RepoOwner
	w.ForkRepo = forkRepo

	if err := awaitForkReady(ctx, w, w.RepoOwner, forkRepo, forkReadyMaxAttempts, forkReadyPoll); err != nil {
		return fmt.Errorf("waiting for fork %q readiness: %w", forkRepo, err)
	}

	return nil
}

// awaitForkReady polls GetDefaultBranch until the fork's default branch
// ref is readable, or until the attempt limit is exhausted. This handles
// the race between GitHub's fork API returning and the fork's Git data
// being fully replicated.
//
// maxAttempts and poll are explicit parameters so that unit tests can
// pass small values to avoid real sleeps.
func awaitForkReady(ctx context.Context, w *world.World, owner, repo string, maxAttempts int, poll time.Duration) error {
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		_, err := w.SCM.GetDefaultBranch(ctx, owner, repo)
		if err == nil {
			return nil
		}

		if attempt < maxAttempts {
			select {
			case <-ctx.Done():
				return fmt.Errorf(
					"context cancelled waiting for default branch on %s/%s: %w",
					owner, repo, ctx.Err(),
				)
			case <-time.After(poll):
			}
		}
	}

	return fmt.Errorf(
		"fork %s/%s default branch not readable after %d attempts",
		owner, repo, maxAttempts,
	)
}

// resolveForkName maps a logical fork name from a Gherkin feature file to
// the actual GitHub repository name. When a leased repo is in use
// (w.LeasedRepoName is set), the logical name's suffix (relative to the
// default "test-repo" base) is appended to the leased repo name.
//
// Examples:
//
//	"test-repo-fork" + leased "test-repo-07" → "test-repo-07-fork"
//	"test-repo-fork" + no lease             → "test-repo-fork" (unchanged)
//	"custom-fork"    + leased "test-repo-07" → "custom-fork"   (no match)
func resolveForkName(w *world.World, logicalName string) string {
	if w.LeasedRepoName == "" {
		return logicalName
	}
	const defaultTestRepo = "test-repo"
	suffix := strings.TrimPrefix(logicalName, defaultTestRepo)
	if suffix == logicalName {
		// Logical name doesn't start with the default base — use as-is.
		return logicalName
	}
	return w.RepoName + suffix
}

// whenForkPullRequestOpened commits a file to a new branch on the fork
// and opens a cross-fork pull request against the base repository.
func whenForkPullRequestOpened(w *world.World) error {
	if w.ForkOwner == "" || w.ForkRepo == "" {
		return fmt.Errorf("no fork created: use 'Given a fork' first")
	}

	w.ScenarioStart = time.Now()
	branch := fmt.Sprintf("behaviour-fork-pr-%d", time.Now().UnixNano())

	ctx := context.Background()

	// Create the branch on the fork first — GitHub's Contents API
	// (used by CommitFileToFork → CreateOrUpdateFileOnBranch) requires
	// the target branch to already exist.
	if err := w.SCM.CreateBranch(ctx, w.ForkOwner, w.ForkRepo, branch); err != nil {
		return fmt.Errorf("creating fork branch: %w", err)
	}
	// Record the branch immediately so CleanupScenario can delete it
	// even if CommitFileToFork or CreateForkChangeProposal fails below.
	w.ForkPRBranch = branch

	msg := fmt.Sprintf("behaviour fork pr %s", branch)
	if err := w.SCM.CommitFileToFork(ctx, w.ForkOwner, w.ForkRepo, branch, "behaviour/fork-pr.txt", msg, []byte("behaviour fork test\n")); err != nil {
		return fmt.Errorf("committing to fork branch: %w", err)
	}

	pr, err := w.SCM.CreateForkChangeProposal(ctx, w.RepoOwner, w.RepoName, "Behaviour fork test PR", "behaviour fork", w.ForkOwner, w.ForkRepo, branch, "main")
	if err != nil {
		return fmt.Errorf("creating fork pull request: %w", err)
	}
	w.ForkPRNumber = pr.Number
	return nil
}

// whenForkPullRequestLabeled adds a label to a fork pull request. Fork PRs
// are opened against the base repo, so the label is applied there.
func whenForkPullRequestLabeled(w *world.World, label string) error {
	if w.ForkPRNumber == 0 {
		return fmt.Errorf("no fork pull request opened")
	}
	w.ScenarioStart = time.Now()
	// Fork PRs are opened against the base repo, so label on the base repo.
	return w.SCM.AddIssueLabels(context.Background(), w.RepoOwner, w.RepoName, w.ForkPRNumber, label)
}

// whenCommitPushedToForkPR pushes an additional commit to the head branch
// of an existing fork pull request.
func whenCommitPushedToForkPR(w *world.World) error {
	if w.ForkPRNumber == 0 {
		return fmt.Errorf("no fork pull request opened")
	}

	w.ScenarioStart = time.Now()
	ctx := context.Background()

	msg := fmt.Sprintf("behaviour: push to fork PR #%d", w.ForkPRNumber)
	content := []byte(fmt.Sprintf("pushed at %d\n", time.Now().UnixNano()))
	if err := w.SCM.CommitFileToFork(ctx, w.ForkOwner, w.ForkRepo, w.ForkPRBranch, "behaviour/fork-push.txt", msg, content); err != nil {
		return fmt.Errorf("pushing commit to fork PR: %w", err)
	}
	return nil
}
