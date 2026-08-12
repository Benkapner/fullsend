package install

import (
	"sync"
	"testing"
)

// TestConcurrentStateAccess exercises a real PerRepoState under the race
// detector. PerRepoState is a read-only snapshot: its fields (org, repo,
// mintURL) are set at construction and never modified, and all accessor
// methods return derived constants. Sharing one instance across goroutines
// via World.Clone is safe by design. This test verifies that property: any
// unsynchronized mutable field added in the future would cause -race to
// fire.
func TestConcurrentStateAccess(t *testing.T) {
	t.Parallel()

	st := NewPerRepoState("test-org", "test-repo", "https://mint.test")

	const goroutines = 12

	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()

			// Exercise every State accessor concurrently.
			_ = st.Mode()
			_ = st.ConfigOwner()
			_ = st.ConfigRepo()
			_ = st.ConfigPathPrefix()
			_ = st.TriageWorkflowRepo()
			_ = st.TriageWorkflowFile()
			_ = st.AgentWorkflowFile()
			_ = st.AgentArtifactName()
			_ = st.MintURL()
		}()
	}
	wg.Wait()
}
