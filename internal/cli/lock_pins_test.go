package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fullsend-ai/fullsend/internal/fetch"
	"github.com/fullsend-ai/fullsend/internal/harness"
	"github.com/fullsend-ai/fullsend/internal/lock"
	"github.com/fullsend-ai/fullsend/internal/ui"
)

// TestLockedDependencyHashes_PinsURLBaseFiles locks a harness with a URL
// base, moves the URL index for the base's relative files to other content,
// and checks that an offline load with the lock's pins still resolves the
// locked content (#8979).
func TestLockedDependencyHashes_PinsURLBaseFiles(t *testing.T) {
	agentContent := []byte("locked agent")
	policyContent := []byte("locked: policy")
	srvFiles := map[string][]byte{
		"/agents/triage.md":      agentContent,
		"/policies/sandbox.yaml": policyContent,
	}
	baseContent := []byte("agent: agents/triage.md\npolicy: policies/sandbox.yaml\nrole: test\n")
	srvFiles["/harness/triage.yaml"] = baseContent
	srv, policy := newLockTestServer(t, srvFiles)

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "harness"), 0o755))
	harnessPath := filepath.Join(dir, "harness", "code.yaml")
	require.NoError(t, os.WriteFile(harnessPath, []byte(fmt.Sprintf(
		"role: test\nbase: %s/harness/triage.yaml#sha256=%s\n", srv.URL, fetch.ComputeSHA256(baseContent))), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(fmt.Sprintf(
		"allowed_remote_resources:\n  - \"%s/\"\n", srv.URL)), 0o644))

	fetch.DefaultPolicy = policy
	defer func() { fetch.DefaultPolicy = fetch.FetchPolicy{} }()

	require.NoError(t, runLock(context.Background(), "code", dir, "", false, resolveFlags{}, ui.New(os.Stdout)))

	agentURL := srv.URL + "/agents/triage.md"
	policyURL := srv.URL + "/policies/sandbox.yaml"
	pins := lockedDependencyHashes(dir, "code", harnessPath)
	assert.Equal(t, fetch.ComputeSHA256(agentContent), pins[agentURL])
	assert.Equal(t, fetch.ComputeSHA256(policyContent), pins[policyURL])

	// The URL index moves after locking, as when another harness re-fetches
	// a branch-ref base online.
	indexPath := filepath.Join(dir, ".fullsend-cache", "url-index.json")
	for u, content := range map[string][]byte{agentURL: []byte("moved agent"), policyURL: []byte("moved: policy")} {
		require.NoError(t, fetch.CachePut(dir, u, content))
		moveURLIndexEntry(t, indexPath, u, fetch.ComputeSHA256(content))
	}

	h, _, err := harness.LoadWithBase(context.Background(), harnessPath, harness.ComposeOpts{
		WorkspaceRoot: dir,
		FetchPolicy:   fetch.FetchPolicy{Offline: true},
		OrgAllowlist:  []string{srv.URL + "/"},
		LockedHashes:  pins,
	})
	require.NoError(t, err)
	agentData, err := os.ReadFile(h.Agent)
	require.NoError(t, err)
	assert.Equal(t, agentContent, agentData)
	policyData, err := os.ReadFile(h.Policy)
	require.NoError(t, err)
	assert.Equal(t, policyContent, policyData)
}

// moveURLIndexEntry repoints one url-index.json entry.
func moveURLIndexEntry(t *testing.T, indexPath, rawURL, hash string) {
	t.Helper()
	data, err := os.ReadFile(indexPath)
	require.NoError(t, err)
	var index map[string]string
	require.NoError(t, json.Unmarshal(data, &index))
	require.Contains(t, index, rawURL)
	index[rawURL] = hash
	out, err := json.Marshal(index)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(indexPath, out, 0o600))
}

func TestLockedDependencyHashes_NoPins(t *testing.T) {
	harnessContent := []byte("role: test\n")
	harnessHash := fetch.ComputeSHA256(harnessContent)

	setup := func(t *testing.T, lf *lock.LockFile) (string, string) {
		t.Helper()
		dir := t.TempDir()
		harnessPath := filepath.Join(dir, "harness", "code.yaml")
		require.NoError(t, os.MkdirAll(filepath.Dir(harnessPath), 0o755))
		require.NoError(t, os.WriteFile(harnessPath, harnessContent, 0o644))
		if lf != nil {
			require.NoError(t, lock.Save(filepath.Join(dir, "lock.yaml"), lf))
		}
		return dir, harnessPath
	}
	entry := func(sha string) *lock.LockFile {
		return &lock.LockFile{Harnesses: map[string]lock.HarnessLock{"code": {
			SHA256: sha,
			Dependencies: []lock.DependencyEntry{
				{Field: "agent", URL: "https://example.com/agents/triage.md", SHA256: fetch.ComputeSHA256([]byte("a"))},
				{Field: "policy", URL: "https://example.com/policies/sandbox.yaml"},
			},
		}}}
	}

	t.Run("no lock file", func(t *testing.T) {
		dir, harnessPath := setup(t, nil)
		assert.Nil(t, lockedDependencyHashes(dir, "code", harnessPath))
	})

	t.Run("unreadable lock file", func(t *testing.T) {
		dir, harnessPath := setup(t, nil)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "lock.yaml"), []byte("version: [\n"), 0o644))
		assert.Nil(t, lockedDependencyHashes(dir, "code", harnessPath))
	})

	t.Run("no entry for the agent", func(t *testing.T) {
		dir, harnessPath := setup(t, entry(harnessHash))
		assert.Nil(t, lockedDependencyHashes(dir, "other", harnessPath))
	})

	t.Run("stale entry", func(t *testing.T) {
		dir, harnessPath := setup(t, entry(fetch.ComputeSHA256([]byte("older harness"))))
		assert.Nil(t, lockedDependencyHashes(dir, "code", harnessPath))
	})

	t.Run("missing harness file", func(t *testing.T) {
		dir, _ := setup(t, entry(harnessHash))
		assert.Nil(t, lockedDependencyHashes(dir, "code", filepath.Join(dir, "harness", "missing.yaml")))
	})

	t.Run("current entry skips entries without a hash", func(t *testing.T) {
		dir, harnessPath := setup(t, entry(harnessHash))
		assert.Equal(t, map[string]string{
			"https://example.com/agents/triage.md": fetch.ComputeSHA256([]byte("a")),
		}, lockedDependencyHashes(dir, "code", harnessPath))
	})
}
