package harness

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fullsend-ai/fullsend/internal/fetch"
)

// Tests for ComposeOpts.LockedHashes (#8979): a lock entry pins the
// relative files of a URL base, so a locked load resolves each pinned URL
// to the locked content, not to whatever url-index.json points at.

// seedLockedFile caches content and returns its hash without touching the
// URL index.
func seedLockedFile(t *testing.T, cacheDir, rawURL string, content []byte) string {
	t.Helper()
	require.NoError(t, fetch.CachePut(cacheDir, rawURL, content))
	return fetch.ComputeSHA256(content)
}

// seedLockedTree caches a directory tree and returns its tree hash without
// touching the URL index.
func seedLockedTree(t *testing.T, cacheDir, rawURL string, files map[string][]byte, full bool) string {
	t.Helper()
	treeHash, err := fetch.CachePutDir(cacheDir, rawURL, files, fetch.DirCachePutOpts{FullListing: full})
	require.NoError(t, err)
	return treeHash
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	require.NoError(t, err)
	return string(data)
}

func failingTreeFetcher(t *testing.T) func(context.Context, string, string, string, string) (map[string][]byte, error) {
	return func(context.Context, string, string, string, string) (map[string][]byte, error) {
		t.Error("tree fetcher must not be called for locked content in cache")
		return nil, errors.New("unexpected tree fetch")
	}
}

func TestLoadWithBase_LockedHashes_URLBaseFilesUseLockedContent(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "cache")

	const (
		agentURL    = "https://example.com/agents/triage.md"
		policyURL   = "https://example.com/policies/sandbox.yaml"
		overrideURL = "https://example.com/overrides/review.md"
		skillURL    = "https://example.com/skills/common/SKILL.md"
		pluginURL   = "https://example.com/plugins/gopls-lsp/"
		scriptURL   = "https://example.com/scripts/pre.sh"
		scriptDir   = "https://example.com/scripts"
	)

	baseContent := []byte(`
agent: agents/triage.md
policy: policies/sandbox.yaml
role: test
pre_script: scripts/pre.sh
skills:
  - skills/common:
      sub-agents/review.md: overrides/review.md
plugins:
  - plugins/gopls-lsp
`)
	require.NoError(t, fetch.CachePut(cacheDir, "https://example.com/harness/triage.yaml", baseContent))
	baseURL := "https://example.com/harness/triage.yaml#sha256=" + computeHash(baseContent)

	// The content the lock recorded for every relative file.
	locked := map[string]string{
		agentURL:    seedLockedFile(t, cacheDir, agentURL, []byte("locked agent")),
		policyURL:   seedLockedFile(t, cacheDir, policyURL, []byte("locked: policy")),
		overrideURL: seedLockedFile(t, cacheDir, overrideURL, []byte("locked override")),
		skillURL:    seedLockedTree(t, cacheDir, skillURL, map[string][]byte{"SKILL.md": []byte("# locked skill")}, true),
		pluginURL:   seedLockedTree(t, cacheDir, pluginURL, map[string][]byte{"plugin.json": []byte(`{"name":"locked"}`)}, true),
		scriptURL:   seedLockedTree(t, cacheDir, scriptURL, map[string][]byte{"pre.sh": []byte("#!/bin/sh\necho locked")}, true),
	}

	// Later content that the URL index moved to after locking, for example
	// because a branch-ref base was re-fetched online.
	require.NoError(t, urlIndexPut(cacheDir, agentURL, seedLockedFile(t, cacheDir, agentURL, []byte("moved agent"))))
	require.NoError(t, urlIndexPut(cacheDir, policyURL, seedLockedFile(t, cacheDir, policyURL, []byte("moved: policy"))))
	require.NoError(t, urlIndexPut(cacheDir, overrideURL, seedLockedFile(t, cacheDir, overrideURL, []byte("moved override"))))
	movedSkill := seedLockedTree(t, cacheDir, skillURL, map[string][]byte{"SKILL.md": []byte("# moved skill")}, true)
	require.NoError(t, urlIndexPut(cacheDir, skillURL, movedSkill))
	require.NoError(t, urlIndexPut(cacheDir, "skill:"+skillURL, movedSkill))
	seedPluginTreeCache(t, cacheDir, pluginURL, map[string][]byte{"plugin.json": []byte(`{"name":"moved"}`)})
	movedScript := seedLockedTree(t, cacheDir, scriptURL, map[string][]byte{"pre.sh": []byte("#!/bin/sh\necho moved")}, true)
	require.NoError(t, urlIndexPut(cacheDir, scriptURL, movedScript))
	require.NoError(t, urlIndexPut(cacheDir, "scriptdir:"+scriptDir, movedScript))

	path := writeTestHarness(t, dir, "child.yaml", `
role: test
base: `+baseURL+`
`)

	load := func(t *testing.T, pins map[string]string) *Harness {
		t.Helper()
		h, deps, err := LoadWithBase(context.Background(), path, ComposeOpts{
			WorkspaceRoot: cacheDir,
			FetchPolicy:   fetch.FetchPolicy{Offline: true},
			OrgAllowlist:  []string{"https://example.com/"},
			TreeFetcher:   failingTreeFetcher(t),
			LockedHashes:  pins,
		})
		require.NoError(t, err)
		if pins != nil {
			for _, d := range deps {
				if want, ok := pins[d.URL]; ok {
					assert.Equal(t, want, d.SHA256, "dependency %s (%s) must report the locked hash", d.Field, d.URL)
				}
			}
		}
		require.Len(t, h.Skills, 1)
		require.Len(t, h.Plugins, 1)
		return h
	}

	t.Run("locked load uses the locked content for every file", func(t *testing.T) {
		h := load(t, locked)
		assert.Equal(t, "locked agent", readFile(t, h.Agent))
		assert.Equal(t, "locked: policy", readFile(t, h.Policy))
		assert.Equal(t, "locked override", readFile(t, *h.Skills[0].Overrides["sub-agents/review.md"]))
		assert.Equal(t, "# locked skill", readFile(t, filepath.Join(h.Skills[0].Source, "SKILL.md")))
		assert.Equal(t, `{"name":"locked"}`, readFile(t, filepath.Join(h.Plugins[0].Path, "plugin.json")))
		assert.Equal(t, "#!/bin/sh\necho locked", readFile(t, h.PreScript))
	})

	t.Run("unlocked load still follows the URL index", func(t *testing.T) {
		h := load(t, nil)
		assert.Equal(t, "moved agent", readFile(t, h.Agent))
		assert.Equal(t, "moved: policy", readFile(t, h.Policy))
		assert.Equal(t, "moved override", readFile(t, *h.Skills[0].Overrides["sub-agents/review.md"]))
		assert.Equal(t, "# moved skill", readFile(t, filepath.Join(h.Skills[0].Source, "SKILL.md")))
		assert.Equal(t, `{"name":"moved"}`, readFile(t, filepath.Join(h.Plugins[0].Path, "plugin.json")))
		assert.Equal(t, "#!/bin/sh\necho moved", readFile(t, h.PreScript))
	})
}

func TestLoadWithBase_LockedHashes_SharedContentIsKeyedByURL(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "cache")

	const (
		agentURL  = "https://example.com/agents/triage.md"
		policyURL = "https://example.com/policies/sandbox.yaml"
	)

	baseContent := []byte(`
agent: agents/triage.md
policy: policies/sandbox.yaml
role: test
`)
	require.NoError(t, fetch.CachePut(cacheDir, "https://example.com/harness/triage.yaml", baseContent))
	baseURL := "https://example.com/harness/triage.yaml#sha256=" + computeHash(baseContent)

	// Both URLs served identical content when the lock was taken, so they
	// share one cache entry.
	shared := seedLockedFile(t, cacheDir, agentURL, []byte("shared"))
	require.Equal(t, shared, seedLockedFile(t, cacheDir, policyURL, []byte("shared")))

	// Each URL's index entry then moved to different content.
	require.NoError(t, urlIndexPut(cacheDir, agentURL, seedLockedFile(t, cacheDir, agentURL, []byte("agent v2"))))
	require.NoError(t, urlIndexPut(cacheDir, policyURL, seedLockedFile(t, cacheDir, policyURL, []byte("policy v2"))))

	path := writeTestHarness(t, dir, "child.yaml", `
role: test
base: `+baseURL+`
`)

	load := func(t *testing.T, pins map[string]string) *Harness {
		t.Helper()
		h, _, err := LoadWithBase(context.Background(), path, ComposeOpts{
			WorkspaceRoot: cacheDir,
			FetchPolicy:   fetch.FetchPolicy{Offline: true},
			OrgAllowlist:  []string{"https://example.com/"},
			LockedHashes:  pins,
		})
		require.NoError(t, err)
		return h
	}

	t.Run("both URLs pinned to the same content", func(t *testing.T) {
		h := load(t, map[string]string{agentURL: shared, policyURL: shared})
		assert.Equal(t, "shared", readFile(t, h.Agent))
		assert.Equal(t, "shared", readFile(t, h.Policy))
	})

	t.Run("pinning one URL does not pin another with the same content", func(t *testing.T) {
		h := load(t, map[string]string{agentURL: shared})
		assert.Equal(t, "shared", readFile(t, h.Agent))
		assert.Equal(t, "policy v2", readFile(t, h.Policy))
	})
}

func TestLoadWithBase_LockedHashes_URLSourcedChildOwnFiles(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "cache")

	const (
		childAgentURL  = "https://example.com/agents/child.md"
		childPolicyURL = "https://example.com/policies/child.yaml"
		baseAgentURL   = "https://example.com/agents/triage.md"
	)

	lockedChildAgent := seedLockedFile(t, cacheDir, childAgentURL, []byte("locked child agent"))
	require.NoError(t, urlIndexPut(cacheDir, childAgentURL, seedLockedFile(t, cacheDir, childAgentURL, []byte("moved child agent"))))
	lockedChildPolicy := seedLockedFile(t, cacheDir, childPolicyURL, []byte("locked: child"))
	require.NoError(t, urlIndexPut(cacheDir, childPolicyURL, seedLockedFile(t, cacheDir, childPolicyURL, []byte("moved: child"))))
	lockedBaseAgent := seedLockedFile(t, cacheDir, baseAgentURL, []byte("locked base agent"))
	require.NoError(t, urlIndexPut(cacheDir, baseAgentURL, seedLockedFile(t, cacheDir, baseAgentURL, []byte("moved base agent"))))

	opts := ComposeOpts{
		WorkspaceRoot: cacheDir,
		FetchPolicy:   fetch.FetchPolicy{Offline: true},
		OrgAllowlist:  []string{"https://example.com/"},
		SourceURL:     "https://example.com/harness/child.yaml",
		LockedHashes: map[string]string{
			childAgentURL:  lockedChildAgent,
			childPolicyURL: lockedChildPolicy,
			baseAgentURL:   lockedBaseAgent,
		},
	}

	t.Run("no base", func(t *testing.T) {
		path := writeTestHarness(t, dir, "nobase/child.yaml", `
agent: agents/child.md
policy: policies/child.yaml
role: test
`)
		h, _, err := LoadWithBase(context.Background(), path, opts)
		require.NoError(t, err)
		assert.Equal(t, "locked child agent", readFile(t, h.Agent))
		assert.Equal(t, "locked: child", readFile(t, h.Policy))
	})

	t.Run("with base", func(t *testing.T) {
		baseContent := []byte(`
agent: agents/triage.md
role: test
`)
		require.NoError(t, fetch.CachePut(cacheDir, "https://example.com/harness/triage.yaml", baseContent))
		path := writeTestHarness(t, dir, "withbase/child.yaml", `
policy: policies/child.yaml
role: test
base: https://example.com/harness/triage.yaml#sha256=`+computeHash(baseContent)+`
`)
		h, _, err := LoadWithBase(context.Background(), path, opts)
		require.NoError(t, err)
		assert.Equal(t, "locked base agent", readFile(t, h.Agent))
		assert.Equal(t, "locked: child", readFile(t, h.Policy))
	})
}

func TestLoadWithBase_LockedHashes_OfflineMissingLockedContent(t *testing.T) {
	missing := computeHash([]byte("never cached"))

	tests := []struct {
		name      string
		baseYAML  string
		lockedURL string
		field     string
	}{
		{"agent", "agent: agents/triage.md\nrole: test\n", "https://example.com/agents/triage.md", "agent"},
		{"skill", "agent: agents/triage.md\nrole: test\nskills:\n  - skills/common\n", "https://example.com/skills/common/SKILL.md", "skills[0]"},
		{"plugin", "agent: agents/triage.md\nrole: test\nplugins:\n  - plugins/gopls-lsp\n", "https://example.com/plugins/gopls-lsp/", "plugins[0]"},
		{"script", "agent: agents/triage.md\nrole: test\npre_script: scripts/pre.sh\n", "https://example.com/scripts/pre.sh", "pre_script"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cacheDir := filepath.Join(dir, "cache")

			baseContent := []byte(tt.baseYAML)
			require.NoError(t, fetch.CachePut(cacheDir, "https://example.com/harness/triage.yaml", baseContent))

			// Unlocked files resolve through the index as before.
			agentURL := "https://example.com/agents/triage.md"
			require.NoError(t, urlIndexPut(cacheDir, agentURL, seedLockedFile(t, cacheDir, agentURL, []byte("agent"))))

			// The locked URL's index entry points at cached content, but
			// the pinned content itself is not in the cache.
			if tt.lockedURL != agentURL {
				require.NoError(t, urlIndexPut(cacheDir, tt.lockedURL, seedLockedFile(t, cacheDir, tt.lockedURL, []byte("indexed"))))
			}

			path := writeTestHarness(t, dir, "child.yaml", "role: test\nbase: https://example.com/harness/triage.yaml#sha256="+computeHash(baseContent)+"\n")
			_, _, err := LoadWithBase(context.Background(), path, ComposeOpts{
				WorkspaceRoot: cacheDir,
				FetchPolicy:   fetch.FetchPolicy{Offline: true},
				OrgAllowlist:  []string{"https://example.com/"},
				LockedHashes:  map[string]string{tt.lockedURL: missing},
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "base "+tt.field+":")
			assert.Contains(t, err.Error(), "pinned in the lock file with sha256="+missing)
			assert.Contains(t, err.Error(), "run 'fullsend lock' first")
		})
	}
}

func TestFetchBaseFile_LockedHashes_Online(t *testing.T) {
	served := []byte("served agent")
	server, policy := setupScriptTestServer(t, nil, map[string][]byte{"/agents/triage.md": served})
	fileURL := server.URL + "/agents/triage.md"
	allowlist := []string{server.URL + "/"}

	t.Run("re-fetched content matching the pin is used", func(t *testing.T) {
		cacheDir := filepath.Join(t.TempDir(), "cache")
		pin := fetch.ComputeSHA256(served)
		dep, contentPath, err := fetchBaseFile(context.Background(), "agent", server.URL+"/", "agents/triage.md", allowlist, ComposeOpts{
			WorkspaceRoot: cacheDir,
			FetchPolicy:   policy,
			LockedHashes:  map[string]string{fileURL: pin},
		}, "resource", false)
		require.NoError(t, err)
		assert.False(t, dep.CacheHit)
		assert.Equal(t, pin, dep.SHA256)
		assert.Equal(t, string(served), readFile(t, contentPath))
	})

	t.Run("re-fetched content not matching the pin is rejected", func(t *testing.T) {
		cacheDir := filepath.Join(t.TempDir(), "cache")
		pin := computeHash([]byte("locked agent"))
		_, _, err := fetchBaseFile(context.Background(), "agent", server.URL+"/", "agents/triage.md", allowlist, ComposeOpts{
			WorkspaceRoot: cacheDir,
			FetchPolicy:   policy,
			LockedHashes:  map[string]string{fileURL: pin},
		}, "resource", false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "the lock file pins sha256="+pin)
		// Neither the cache nor the URL index takes the unpinned content.
		content, _, cErr := fetch.CacheGet(cacheDir, fetch.ComputeSHA256(served))
		require.NoError(t, cErr)
		assert.Nil(t, content)
		_, indexed := urlIndexLookup(cacheDir, fileURL)
		assert.False(t, indexed)
	})
}

func TestFetchBaseSkill_LockedHashes_Online(t *testing.T) {
	const (
		baseDir   = "https://raw.githubusercontent.com/org/repo/ref/"
		skillURL  = baseDir + "skills/common/SKILL.md"
		allowlist = "https://raw.githubusercontent.com/org/repo/"
	)
	files := map[string][]byte{"SKILL.md": []byte("# skill")}

	t.Run("pinned partial listing is served without a re-fetch", func(t *testing.T) {
		cacheDir := filepath.Join(t.TempDir(), "cache")
		pin := seedLockedTree(t, cacheDir, skillURL, files, false)
		dep, _, err := fetchBaseSkill(context.Background(), "skills[0]", baseDir, "skills/common", []string{allowlist}, ComposeOpts{
			WorkspaceRoot: cacheDir,
			TreeFetcher:   failingTreeFetcher(t),
			LockedHashes:  map[string]string{skillURL: pin},
		})
		require.NoError(t, err)
		assert.True(t, dep.CacheHit)
		assert.Equal(t, pin, dep.SHA256)
	})

	t.Run("re-fetched tree matching the pin is used", func(t *testing.T) {
		cacheDir := filepath.Join(t.TempDir(), "cache")
		pin := fetch.ComputeTreeHash(files)
		dep, _, err := fetchBaseSkill(context.Background(), "skills[0]", baseDir, "skills/common", []string{allowlist}, ComposeOpts{
			WorkspaceRoot: cacheDir,
			TreeFetcher:   fakeTreeFetcher(files),
			LockedHashes:  map[string]string{skillURL: pin},
		})
		require.NoError(t, err)
		assert.False(t, dep.CacheHit)
		assert.Equal(t, pin, dep.SHA256)
	})

	t.Run("re-fetched tree not matching the pin is rejected", func(t *testing.T) {
		cacheDir := filepath.Join(t.TempDir(), "cache")
		pin := fetch.ComputeTreeHash(map[string][]byte{"SKILL.md": []byte("# locked")})
		_, _, err := fetchBaseSkill(context.Background(), "skills[0]", baseDir, "skills/common", []string{allowlist}, ComposeOpts{
			WorkspaceRoot: cacheDir,
			TreeFetcher:   fakeTreeFetcher(files),
			LockedHashes:  map[string]string{skillURL: pin},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "the lock file pins sha256="+pin)
		_, indexed := urlIndexLookup(cacheDir, "skill:"+skillURL)
		assert.False(t, indexed)
	})
}

func TestFetchBasePlugin_LockedHashes_Online(t *testing.T) {
	const (
		baseDir   = "https://raw.githubusercontent.com/org/repo/ref/"
		pluginURL = baseDir + "plugins/gopls-lsp/"
		allowlist = "https://raw.githubusercontent.com/org/repo/"
	)
	files := map[string][]byte{"plugin.json": []byte(`{"name":"gopls-lsp"}`)}

	t.Run("pinned partial listing is served without a re-fetch", func(t *testing.T) {
		cacheDir := filepath.Join(t.TempDir(), "cache")
		pin := seedLockedTree(t, cacheDir, pluginURL, files, false)
		dep, _, err := fetchBasePlugin(context.Background(), "plugins[0]", baseDir, "plugins/gopls-lsp", []string{allowlist}, ComposeOpts{
			WorkspaceRoot: cacheDir,
			TreeFetcher:   failingTreeFetcher(t),
			LockedHashes:  map[string]string{pluginURL: pin},
		})
		require.NoError(t, err)
		assert.True(t, dep.CacheHit)
		assert.Equal(t, pin, dep.SHA256)
	})

	t.Run("re-fetched tree not matching the pin is rejected", func(t *testing.T) {
		cacheDir := filepath.Join(t.TempDir(), "cache")
		pin := fetch.ComputeTreeHash(map[string][]byte{"plugin.json": []byte(`{"name":"locked"}`)})
		_, _, err := fetchBasePlugin(context.Background(), "plugins[0]", baseDir, "plugins/gopls-lsp", []string{allowlist}, ComposeOpts{
			WorkspaceRoot: cacheDir,
			TreeFetcher:   fakeTreeFetcher(files),
			LockedHashes:  map[string]string{pluginURL: pin},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "the lock file pins sha256="+pin)
	})
}

func TestFetchBaseScriptOrDir_LockedHashes_Online(t *testing.T) {
	const (
		baseDir   = "https://raw.githubusercontent.com/org/repo/ref/"
		scriptURL = baseDir + "scripts/pre.sh"
		allowlist = "https://raw.githubusercontent.com/org/repo/"
	)
	files := map[string][]byte{"pre.sh": []byte("#!/bin/sh\necho hi")}

	t.Run("pinned single file is served without a directory fetch", func(t *testing.T) {
		cacheDir := filepath.Join(t.TempDir(), "cache")
		content := []byte("#!/bin/sh\necho locked")
		pin := seedLockedFile(t, cacheDir, scriptURL, content)
		dep, contentPath, err := fetchBaseScriptOrDir(context.Background(), "pre_script", baseDir, "scripts/pre.sh", []string{allowlist}, ComposeOpts{
			WorkspaceRoot: cacheDir,
			TreeFetcher:   failingTreeFetcher(t),
			LockedHashes:  map[string]string{scriptURL: pin},
		})
		require.NoError(t, err)
		assert.True(t, dep.CacheHit)
		assert.Equal(t, pin, dep.SHA256)
		assert.Equal(t, string(content), readFile(t, contentPath))
	})

	t.Run("re-fetched directory matching the pin is used", func(t *testing.T) {
		cacheDir := filepath.Join(t.TempDir(), "cache")
		pin := fetch.ComputeTreeHash(files)
		dep, _, err := fetchBaseScriptOrDir(context.Background(), "pre_script", baseDir, "scripts/pre.sh", []string{allowlist}, ComposeOpts{
			WorkspaceRoot: cacheDir,
			TreeFetcher:   fakeTreeFetcher(files),
			LockedHashes:  map[string]string{scriptURL: pin},
		})
		require.NoError(t, err)
		assert.False(t, dep.CacheHit)
		assert.Equal(t, pin, dep.SHA256)
	})

	t.Run("re-fetched directory not matching the pin is rejected", func(t *testing.T) {
		cacheDir := filepath.Join(t.TempDir(), "cache")
		pin := fetch.ComputeTreeHash(map[string][]byte{"pre.sh": []byte("#!/bin/sh\necho locked")})
		_, _, err := fetchBaseScriptOrDir(context.Background(), "pre_script", baseDir, "scripts/pre.sh", []string{allowlist}, ComposeOpts{
			WorkspaceRoot: cacheDir,
			TreeFetcher:   fakeTreeFetcher(files),
			LockedHashes:  map[string]string{scriptURL: pin},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "the lock file pins sha256="+pin)
	})
}
