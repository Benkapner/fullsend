package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fullsend-ai/fullsend/internal/fetch"
	"github.com/fullsend-ai/fullsend/internal/gitfetch"
	"github.com/fullsend-ai/fullsend/internal/harness"
	"github.com/fullsend-ai/fullsend/internal/lock"
	"github.com/fullsend-ai/fullsend/internal/ui"
)

// lockThenReplay runs `fullsend lock` for the "code" harness under dir, then
// loads the harness offline and replays the lock entry onto it, as
// `fullsend run` does on a locked run. It returns the harness before and
// after the replay.
func lockThenReplay(t *testing.T, dir string, policy fetch.FetchPolicy, allowlist []string, treeFetcher gitfetch.TreeFetchFunc) (before, after *harness.Harness) {
	t.Helper()
	printer := ui.New(io.Discard)

	fetch.DefaultPolicy = policy
	t.Cleanup(func() { fetch.DefaultPolicy = fetch.FetchPolicy{} })
	require.NoError(t, runLock(context.Background(), "code", dir, "", false, resolveFlags{treeFetcher: treeFetcher}, printer))

	lf, err := lock.Load(filepath.Join(dir, "lock.yaml"))
	require.NoError(t, err)
	require.NotNil(t, lf)
	entry := lf.Lookup("code")
	require.NotNil(t, entry)

	offline := policy
	offline.Offline = true
	load := func() *harness.Harness {
		h, _, err := harness.LoadWithBase(context.Background(), filepath.Join(dir, "harness", "code.yaml"), harness.ComposeOpts{
			WorkspaceRoot: dir,
			FetchPolicy:   offline,
			OrgAllowlist:  allowlist,
			TreeFetcher:   treeFetcher,
		})
		require.NoError(t, err)
		require.NoError(t, h.ResolveRelativeTo(dir))
		require.True(t, h.HasURLReferences(), "the child must hold a URL so a run reads the lock")
		return h
	}

	before = load()
	after = load()
	_, err = resolveFromLock(after, entry, dir, allowlist, printer)
	require.NoError(t, err)
	return before, after
}

func writeTestFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}
}

// A URL base's relative agent is recorded in the lock as "agent". On replay
// it must not replace the child's own local agent (#8977).
func TestResolveFromLock_URLBaseAgentKeepsChildAgent(t *testing.T) {
	baseContent := []byte("agent: agents/shared.md\nrole: test\n")
	policyContent := []byte("# child policy\n")
	srv, policy := newLockTestServer(t, map[string][]byte{
		"/harness/base.yaml": baseContent,
		"/agents/shared.md":  []byte("# shared agent\n"),
		"/policies/p.yaml":   policyContent,
	})

	dir := t.TempDir()
	writeTestFiles(t, dir, map[string]string{
		"agents/mine.md": "# my agent\n",
		"harness/code.yaml": fmt.Sprintf("base: %q\nagent: agents/mine.md\npolicy: %q\nallowed_remote_resources:\n  - %q\n",
			srv.URL+"/harness/base.yaml#sha256="+fetch.ComputeSHA256(baseContent),
			srv.URL+"/policies/p.yaml#sha256="+fetch.ComputeSHA256(policyContent),
			srv.URL+"/"),
		"config.yaml": fmt.Sprintf("allowed_remote_resources:\n  - %q\n", srv.URL+"/"),
	})

	before, after := lockThenReplay(t, dir, policy, []string{srv.URL + "/"}, nil)

	assert.Equal(t, filepath.Join(dir, "agents", "mine.md"), before.Agent)
	assert.Equal(t, before.Agent, after.Agent, "the base's lock entry must not replace the child's agent")
	assert.False(t, harness.IsURL(after.Policy), "the child's URL policy is still replaced by its cached path")
	got, err := os.ReadFile(after.Policy)
	require.NoError(t, err)
	assert.Equal(t, policyContent, got)
}

// The same as above for policy.
func TestResolveFromLock_URLBasePolicyKeepsChildPolicy(t *testing.T) {
	baseContent := []byte("agent: agents/shared.md\nrole: test\npolicy: policies/shared.yaml\n")
	agentContent := []byte("# child agent\n")
	srv, policy := newLockTestServer(t, map[string][]byte{
		"/harness/base.yaml":    baseContent,
		"/agents/shared.md":     []byte("# shared agent\n"),
		"/policies/shared.yaml": []byte("# shared policy\n"),
		"/agents/child.md":      agentContent,
	})

	dir := t.TempDir()
	writeTestFiles(t, dir, map[string]string{
		"policies/mine.yaml": "# my policy\n",
		"harness/code.yaml": fmt.Sprintf("base: %q\nagent: %q\npolicy: policies/mine.yaml\nallowed_remote_resources:\n  - %q\n",
			srv.URL+"/harness/base.yaml#sha256="+fetch.ComputeSHA256(baseContent),
			srv.URL+"/agents/child.md#sha256="+fetch.ComputeSHA256(agentContent),
			srv.URL+"/"),
		"config.yaml": fmt.Sprintf("allowed_remote_resources:\n  - %q\n", srv.URL+"/"),
	})

	before, after := lockThenReplay(t, dir, policy, []string{srv.URL + "/"}, nil)

	assert.Equal(t, filepath.Join(dir, "policies", "mine.yaml"), before.Policy)
	assert.Equal(t, before.Policy, after.Policy, "the base's lock entry must not replace the child's policy")
	assert.False(t, harness.IsURL(after.Agent), "the child's URL agent is still replaced by its cached path")
	got, err := os.ReadFile(after.Agent)
	require.NoError(t, err)
	assert.Equal(t, agentContent, got)
}

// A URL base records its skills as skills[N] with N its own index. When the
// merged list holds a different skill at that index (here the child's own
// beta replaces the base's), replay must leave each skill's source alone,
// and still resolve the child's URL skill.
func TestResolveFromLock_URLBaseSkillsKeepTheirSources(t *testing.T) {
	skillFiles := func(name string) map[string][]byte {
		return map[string][]byte{"SKILL.md": []byte("---\nname: " + name + "\n---\n" + name + " body\n")}
	}
	treeFetcher := gitfetch.TreeFetchFunc(func(_ context.Context, _, p, _, _ string) (map[string][]byte, error) {
		return skillFiles(path.Base(p)), nil
	})

	// The base is served from the cache, so its raw URL needs no network;
	// its skills come through the tree fetcher.
	baseURL := "https://raw.githubusercontent.com/test-org/base-repo/main/harness/base.yaml"
	baseContent := []byte("role: test\nskills:\n  - skills/alpha\n  - skills/beta\n")
	gammaURL := "https://github.com/test-org/skills-repo/tree/main/skills/gamma#sha256=" + fetch.ComputeTreeHash(skillFiles("gamma"))
	allowlist := []string{"https://raw.githubusercontent.com/test-org/", "https://github.com/test-org/"}

	dir := t.TempDir()
	require.NoError(t, fetch.CachePut(dir, baseURL, baseContent))
	writeTestFiles(t, dir, map[string]string{
		"agents/mine.md":       "# my agent\n",
		"skills/beta/SKILL.md": "---\nname: beta\n---\nchild beta\n",
		"harness/code.yaml": fmt.Sprintf("base: %q\nagent: agents/mine.md\nskills:\n  - skills/beta\n  - %q\nallowed_remote_resources:\n  - %q\n  - %q\n",
			baseURL+"#sha256="+fetch.ComputeSHA256(baseContent), gammaURL, allowlist[0], allowlist[1]),
		"config.yaml": fmt.Sprintf("allowed_remote_resources:\n  - %q\n  - %q\n", allowlist[0], allowlist[1]),
	})

	before, after := lockThenReplay(t, dir, fetch.FetchPolicy{}, allowlist, treeFetcher)

	require.Len(t, before.Skills, 3)
	assert.Equal(t, filepath.Join(dir, "skills", "beta"), before.Skills[1].Source, "the child's beta replaces the base's")

	require.Len(t, after.Skills, 3)
	assert.Equal(t, before.Skills[0].Source, after.Skills[0].Source, "the base's alpha keeps its resolved source")
	assert.Equal(t, before.Skills[1].Source, after.Skills[1].Source, "the base's beta entry must not replace the child's beta")
	assert.Equal(t, "gamma", filepath.Base(after.Skills[2].Source), "the child's URL skill is still resolved from the lock")
	for i, want := range []string{"alpha body", "child beta", "gamma body"} {
		got, err := os.ReadFile(filepath.Join(after.Skills[i].Source, "SKILL.md"))
		require.NoError(t, err)
		assert.Contains(t, string(got), want, "skills[%d]", i)
	}
}

// resolveBaseScripts records a URL base's scripts as pre_script and
// post_script. On replay they must not replace the child's own scripts,
// and an inherited script keeps the executable cached path LoadWithBase set.
func TestResolveFromLock_URLBaseScriptsKeepChildScripts(t *testing.T) {
	baseContent := []byte("agent: agents/shared.md\nrole: test\npre_script: scripts/pre.sh\npost_script: scripts/post.sh\n")
	policyContent := []byte("# child policy\n")
	srv, policy := newLockTestServer(t, map[string][]byte{
		"/harness/base.yaml": baseContent,
		"/agents/shared.md":  []byte("# shared agent\n"),
		"/scripts/pre.sh":    []byte("#!/bin/sh\necho base pre\n"),
		"/scripts/post.sh":   []byte("#!/bin/sh\necho base post\n"),
		"/policies/p.yaml":   policyContent,
	})

	dir := t.TempDir()
	writeTestFiles(t, dir, map[string]string{
		"scripts/mine-pre.sh": "#!/bin/sh\necho child pre\n",
		"harness/code.yaml": fmt.Sprintf("base: %q\npre_script: scripts/mine-pre.sh\npolicy: %q\nallowed_remote_resources:\n  - %q\n",
			srv.URL+"/harness/base.yaml#sha256="+fetch.ComputeSHA256(baseContent),
			srv.URL+"/policies/p.yaml#sha256="+fetch.ComputeSHA256(policyContent),
			srv.URL+"/"),
		"config.yaml": fmt.Sprintf("allowed_remote_resources:\n  - %q\n", srv.URL+"/"),
	})

	before, after := lockThenReplay(t, dir, policy, []string{srv.URL + "/"}, nil)

	assert.Equal(t, filepath.Join(dir, "scripts", "mine-pre.sh"), before.PreScript)
	assert.Equal(t, before.PreScript, after.PreScript, "the base's lock entry must not replace the child's pre_script")
	assert.Equal(t, before.PostScript, after.PostScript, "the inherited post_script keeps the path LoadWithBase resolved")
	got, err := os.ReadFile(after.PostScript)
	require.NoError(t, err)
	assert.Contains(t, string(got), "base post")
	info, err := os.Stat(after.PostScript)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&0o111, "the inherited post_script is executable")
}

// Script entries replace a field that holds their URL and nothing else.
func TestResolveFromLock_ScriptEntriesReplaceOnlyMatchingFields(t *testing.T) {
	content := []byte("#!/bin/sh\necho locked\n")
	hash := fetch.ComputeSHA256(content)
	root := t.TempDir()
	require.NoError(t, fetch.CachePut(root, "https://example.com/scripts/post.sh", content))
	require.NoError(t, fetch.CachePut(root, "https://example.com/scripts/validate.sh", content))

	entry := &lock.HarnessLock{Dependencies: []lock.DependencyEntry{
		{Field: "post_script", URL: "https://example.com/scripts/post.sh", SHA256: hash},
		{Field: "validation_loop.script", URL: "https://example.com/scripts/validate.sh", SHA256: hash},
	}}
	newHarness := func(post, validate string) *harness.Harness {
		return &harness.Harness{
			PostScript:             post,
			ValidationLoop:         &harness.ValidationLoop{Script: validate},
			AllowedRemoteResources: []string{"https://example.com/"},
		}
	}

	h := newHarness("https://example.com/scripts/post.sh#sha256="+hash, "https://example.com/scripts/validate.sh")
	_, err := resolveFromLock(h, entry, root, nil, ui.New(io.Discard))
	require.NoError(t, err)
	for _, p := range []string{h.PostScript, h.ValidationLoop.Script} {
		assert.True(t, strings.HasSuffix(p, "/content"), p)
		info, err := os.Stat(p)
		require.NoError(t, err)
		assert.NotZero(t, info.Mode()&0o111, "a replaced script is executable")
	}

	h = newHarness("/work/scripts/post.sh", "/work/scripts/validate.sh")
	_, err = resolveFromLock(h, entry, root, nil, ui.New(io.Discard))
	require.NoError(t, err)
	assert.Equal(t, "/work/scripts/post.sh", h.PostScript)
	assert.Equal(t, "/work/scripts/validate.sh", h.ValidationLoop.Script)
}

func TestLockFieldHolds(t *testing.T) {
	const u = "https://example.com/agents/a.md"
	for _, tc := range []struct {
		value, url string
		want       bool
	}{
		{u, u, true},
		{u + "#sha256=" + strings.Repeat("a", 64), u, true},
		{u + "#other", u, true},
		{"https://example.com/agents/b.md", u, false},
		{"/work/.fullsend-cache/resources/sha256/abc/content", u, false},
		{"agents/a.md", u, false},
		{"", "", false},
	} {
		assert.Equal(t, tc.want, lockFieldHolds(tc.value, tc.url), "lockFieldHolds(%q, %q)", tc.value, tc.url)
	}
}
