package steps

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fullsend-ai/fullsend/internal/forge"
	"github.com/fullsend-ai/fullsend/pkg/behaviourtest/world"
)

func CleanupScenario(w *world.World) {
	ctx := context.Background()

	// --- Issue / PR cleanup ---
	if w.IssueNumber > 0 {
		if err := w.SCM.CloseIssue(ctx, w.RepoOwner, w.RepoName, w.IssueNumber); err != nil {
			worldLogf(w, "behaviour cleanup: close issue #%d: %v", w.IssueNumber, err)
		}
	}
	if w.ForkPRNumber > 0 {
		// Fork PRs are opened against the base repo, so close on base repo.
		if err := w.SCM.CloseIssue(ctx, w.RepoOwner, w.RepoName, w.ForkPRNumber); err != nil {
			worldLogf(w, "behaviour cleanup: close fork PR #%d: %v", w.ForkPRNumber, err)
		}
	}

	// --- Branch-scenario cleanup ---
	// Sweep applier-created PRs by namespace: a code run for this
	// scenario's issue pushes to agent/<issue>-*, but the PR is only
	// registered in CreatedPRNumbers when the head-match assertion ran
	// and succeeded. Issue numbers are unique, so anything left open in
	// the namespace would otherwise be permanent pool-repo debris. Gated
	// on IssueNumber alone (not on CreatedBranches) so it still runs for
	// a code-stage scenario that never seeded a decoy/seed branch.
	if w.IssueNumber > 0 {
		namespacePrefix := fmt.Sprintf("agent/%d-", w.IssueNumber)
		seenPR := make(map[int]bool, len(w.CreatedPRNumbers))
		for _, n := range w.CreatedPRNumbers {
			seenPR[n] = true
		}
		if prs, err := w.SCM.ListOpenChangeProposals(ctx, w.RepoOwner, w.RepoName); err != nil {
			worldLogf(w, "behaviour cleanup: list open PRs for namespace sweep: %v", err)
		} else {
			for _, pr := range prs {
				if !strings.HasPrefix(pr.Head, namespacePrefix) || seenPR[pr.Number] {
					continue
				}
				seenPR[pr.Number] = true
				w.CreatedPRNumbers = append(w.CreatedPRNumbers, pr.Number)
				w.CreatedBranches = append(w.CreatedBranches, pr.Head)
			}
		}
	}

	// Close PRs before deleting their head branches so GitHub does not
	// auto-close them with a confusing "branch deleted" event first.
	closedPR := make(map[int]bool, len(w.CreatedPRNumbers))
	for _, number := range w.CreatedPRNumbers {
		if closedPR[number] {
			continue
		}
		closedPR[number] = true
		if err := w.SCM.CloseIssue(ctx, w.RepoOwner, w.RepoName, number); err != nil {
			worldLogf(w, "behaviour cleanup: close PR #%d: %v", number, err)
		}
	}
	deletedBranch := make(map[string]bool, len(w.CreatedBranches))
	for _, branch := range w.CreatedBranches {
		if deletedBranch[branch] {
			continue
		}
		deletedBranch[branch] = true
		if err := w.SCM.DeleteBranch(ctx, w.RepoOwner, w.RepoName, branch); err != nil {
			if !forge.IsNotFound(err) {
				worldLogf(w, "behaviour cleanup: delete branch %s: %v", branch, err)
			}
		}
	}

	// --- Fork repo cleanup ---
	// Fork repos are ephemeral: created per-scenario and deleted here.
	// Branch and PR cleanup above already ran against the base repo;
	// deleting the fork repo removes the branch implicitly, but we
	// still attempt branch deletion first so partial failures leave
	// less debris.
	if w.ForkPRBranch != "" && w.ForkOwner != "" && w.ForkRepo != "" {
		if err := w.SCM.DeleteBranch(ctx, w.ForkOwner, w.ForkRepo, w.ForkPRBranch); err != nil {
			if !forge.IsNotFound(err) {
				worldLogf(w, "behaviour cleanup: delete fork branch %s: %v", w.ForkPRBranch, err)
			}
		}
	}
	if w.ForkOwner != "" && w.ForkRepo != "" && w.ForkRepo != w.RepoName {
		if err := w.SCM.DeleteRepo(ctx, w.ForkOwner, w.ForkRepo); err != nil {
			if !forge.IsNotFound(err) {
				worldLogf(w, "behaviour cleanup: delete fork repo %s/%s: %v", w.ForkOwner, w.ForkRepo, err)
			}
		}
	}

	// --- URL harness hosting repo cleanup ---
	// Hosting repos are ephemeral: created per-scenario and deleted here
	// (same lifecycle as fork repos). Guard against deleting the enrolled
	// test repo itself.
	if w.URLHarnessRepoOwner != "" && w.URLHarnessRepoName != "" && w.URLHarnessRepoName != w.RepoName {
		if err := w.SCM.DeleteRepo(ctx, w.URLHarnessRepoOwner, w.URLHarnessRepoName); err != nil {
			if !forge.IsNotFound(err) {
				worldLogf(w, "behaviour cleanup: delete harness-hosting repo %s/%s: %v", w.URLHarnessRepoOwner, w.URLHarnessRepoName, err)
			}
		}
	}

	// --- Jira mock cleanup ---
	if w.JiraMockServer != nil {
		w.JiraMockServer.Close()
	}
	if w.JiraConfigDir != "" {
		if err := os.RemoveAll(w.JiraConfigDir); err != nil {
			worldLogf(w, "behaviour cleanup: remove jira config dir: %v", err)
		}
	}

	// --- Artifact cleanup ---
	if w.ArtifactDir != "" && shouldRemoveArtifactDir(w.ArtifactDir, os.Getenv("BEHAVIOUR_ARTIFACT_DIR")) {
		if err := os.RemoveAll(w.ArtifactDir); err != nil {
			worldLogf(w, "behaviour cleanup: remove artifact dir: %v", err)
		}
	}

	// --- Kill switch cleanup ---
	// Deactivate the kill switch so the next scenario on this slot is
	// not blocked by sticky state. Runs before dummy-script cleanup
	// because the kill switch is a repo-level config that affects all
	// harnesses.
	if w.KillSwitchActivated {
		if err := DeactivateKillSwitch(w); err != nil {
			worldLogf(w, "behaviour cleanup: deactivate kill switch: %v", err)
		}
	}

	// --- Dummy script cleanup ---
	if len(w.DummyOps) > 0 {
		if w.Org == "" || w.RepoName == "" {
			worldLogf(w, "behaviour cleanup: clear dummy script: no repo configured; call 'Given the enrolled test repository' first")
		} else {
			empty := []byte("ops: []\n")
			if err := w.SCM.CommitFile(ctx, w.Org, w.RepoName, w.BehaviourScriptPath(), "behaviour: clear dummy agent script", empty); err != nil {
				worldLogf(w, "behaviour cleanup: clear dummy script: %v", err)
			}
		}
	}
}

// shouldRemoveArtifactDir reports whether cleanup may delete artifactDir.
// Dirs under BEHAVIOUR_ARTIFACT_DIR are preserved for CI upload-artifact.
func shouldRemoveArtifactDir(artifactDir, ciArtifactDir string) bool {
	ciArtifactDir = strings.TrimSpace(ciArtifactDir)
	if ciArtifactDir == "" {
		return true
	}
	return !artifactDirUnderCIRoot(artifactDir, ciArtifactDir)
}

func artifactDirUnderCIRoot(dir, ciRoot string) bool {
	cleanDir := filepath.Clean(dir)
	cleanRoot := filepath.Clean(ciRoot)
	if cleanDir == cleanRoot {
		return true
	}
	return strings.HasPrefix(cleanDir, cleanRoot+string(os.PathSeparator))
}

func worldLogf(w *world.World, format string, args ...any) {
	if w.Logf != nil {
		w.Logf(format, args...)
	}
}
