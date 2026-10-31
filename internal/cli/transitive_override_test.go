package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fullsend-ai/fullsend/internal/fetch"
	"github.com/fullsend-ai/fullsend/internal/gitfetch"
	"github.com/fullsend-ai/fullsend/internal/harness"
	"github.com/fullsend-ai/fullsend/internal/lock"
	"github.com/fullsend-ai/fullsend/internal/resolve"
	"github.com/fullsend-ai/fullsend/internal/ui"
)

// TestLockDirectOverridesSurviveTransitiveDup: skill A declares B as a
// transitive dependency; B is also listed directly with a file override.
// After `fullsend lock` and lock replay, B must keep its override.
func TestLockDirectOverridesSurviveTransitiveDup(t *testing.T) {
	for name, aFirst := range map[string]bool{"A_then_B": true, "B_then_A": false} {
		t.Run(name, func(t *testing.T) {
			bFiles := map[string][]byte{"SKILL.md": []byte("# Skill B"), "sub-agents/x.md": []byte("x")}
			bHash := fetch.ComputeTreeHash(bFiles)
			aFiles := map[string][]byte{"SKILL.md": []byte(fmt.Sprintf("---\ndependencies:\n  - b#sha256=%s\n---\n# Skill A", bHash))}
			aHash := fetch.ComputeTreeHash(aFiles)
			trees := map[string]map[string][]byte{"skills/a": aFiles, "skills/b": bFiles}
			fetcher := func(_ context.Context, _, p, _, _ string) (map[string][]byte, error) {
				if f, ok := trees[p]; ok {
					return f, nil
				}
				return nil, fmt.Errorf("no tree %q", p)
			}

			const base = "https://github.com/test-org/test-repo/tree/main/"
			aLine := fmt.Sprintf("  - %q\n", base+"skills/a#sha256="+aHash)
			bLine := fmt.Sprintf("  - %q:\n      sub-agents/x.md: null\n", base+"skills/b#sha256="+bHash)
			skills := bLine + aLine
			if aFirst {
				skills = aLine + bLine
			}
			harnessYAML := "agent: agents/code.md\nrole: test\nskills:\n" + skills + "allowed_remote_resources:\n  - \"https://github.com/test-org/\"\n"

			dir := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(dir, "harness"), 0o755))
			require.NoError(t, os.MkdirAll(filepath.Join(dir, "agents"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "agents", "code.md"), []byte("agent"), 0o644))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "harness", "code.yaml"), []byte(harnessYAML), 0o644))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"),
				[]byte("allowed_remote_resources:\n  - \"https://github.com/test-org/\"\n"), 0o644))

			printer := ui.New(io.Discard)
			require.NoError(t, runLock(context.Background(), "code", dir, "", false,
				resolveFlags{treeFetcher: gitfetch.TreeFetchFunc(fetcher), maxDepth: resolve.DefaultMaxDepth}, printer))

			lf, err := lock.Load(filepath.Join(dir, "lock.yaml"))
			require.NoError(t, err)
			entry := lf.Lookup("code")
			require.NotNil(t, entry)
			for _, d := range entry.Dependencies {
				t.Logf("lock dep: field=%s url=%s", d.Field, d.URL)
			}

			h, err := harness.Load(filepath.Join(dir, "harness", "code.yaml"))
			require.NoError(t, err)
			require.NoError(t, h.ResolveRelativeTo(dir))
			_, err = resolveFromLock(h, entry, dir, nil, printer)
			require.NoError(t, err)

			var bEntries []harness.SkillEntry
			for _, s := range h.Skills {
				if filepath.Base(s.Source) == "b" {
					bEntries = append(bEntries, s)
				}
			}
			require.Len(t, bEntries, 1, "skills after lock replay: %+v", h.Skills)
			v, ok := bEntries[0].Overrides["sub-agents/x.md"]
			assert.True(t, ok, "override sub-agents/x.md: null was dropped from skill b; got overrides=%v", bEntries[0].Overrides)
			assert.Nil(t, v)
		})
	}
}

// TestResolveFromLock_URLOverrideSurvivesTransitiveDup replays the lock that
// `fullsend lock` records when skill A pulls in B transitively and B is also
// listed directly with a URL-valued override. The override must end up on
// the kept entry for B, whatever the list order.
func TestResolveFromLock_URLOverrideSurvivesTransitiveDup(t *testing.T) {
	const base = "https://github.com/test-org/test-repo/tree/main/"
	const overrideURL = "https://example.com/overrides/x.md"
	bFiles := map[string][]byte{"SKILL.md": []byte("# Skill B"), "sub-agents/x.md": []byte("x")}
	bHash := fetch.ComputeTreeHash(bFiles)
	aFiles := map[string][]byte{"SKILL.md": []byte(fmt.Sprintf("---\ndependencies:\n  - b#sha256=%s\n---\n# Skill A", bHash))}
	aHash := fetch.ComputeTreeHash(aFiles)
	overrideContent := []byte("custom x")
	overrideHash := fetch.ComputeSHA256(overrideContent)

	for name, aFirst := range map[string]bool{"A_then_B": true, "B_then_A": false} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			_, err := fetch.CachePutDir(root, base+"skills/a", aFiles)
			require.NoError(t, err)
			_, err = fetch.CachePutDir(root, base+"skills/b", bFiles)
			require.NoError(t, err)
			require.NoError(t, fetch.CachePut(root, overrideURL, overrideContent))

			val := overrideURL + "#sha256=" + overrideHash
			aEntry := harness.SkillEntry{Source: base + "skills/a#sha256=" + aHash}
			bEntry := harness.SkillEntry{Source: base + "skills/b#sha256=" + bHash, Overrides: map[string]*string{"sub-agents/x.md": &val}}
			bDir := lock.DependencyEntry{URL: base + "skills/b", SHA256: bHash, Type: "directory"}
			aDir := lock.DependencyEntry{Field: "skills[0]", URL: base + "skills/a", SHA256: aHash, Type: "directory"}
			override := lock.DependencyEntry{URL: overrideURL, SHA256: overrideHash}

			var skills []harness.SkillEntry
			var deps []lock.DependencyEntry
			if aFirst {
				skills = []harness.SkillEntry{aEntry, bEntry}
				bDir.Field = "skills[" + base + "skills/a:dep0]"
				override.Field = "skills[1].overrides[sub-agents/x.md]"
				deps = []lock.DependencyEntry{bDir, aDir, override}
			} else {
				skills = []harness.SkillEntry{bEntry, aEntry}
				bDir.Field = "skills[0]"
				aDir.Field = "skills[1]"
				override.Field = "skills[0].overrides[sub-agents/x.md]"
				deps = []lock.DependencyEntry{bDir, override, aDir}
			}
			h := &harness.Harness{
				Agent:                  "agents/code.md",
				Skills:                 skills,
				AllowedRemoteResources: []string{"https://github.com/test-org/", "https://example.com/"},
			}

			_, err = resolveFromLock(h, &lock.HarnessLock{Dependencies: deps}, root, nil, ui.New(io.Discard))
			require.NoError(t, err)

			var bEntries []harness.SkillEntry
			for _, s := range h.Skills {
				if filepath.Base(s.Source) == "b" {
					bEntries = append(bEntries, s)
				}
			}
			require.Len(t, h.Skills, 2, "skills after lock replay: %+v", h.Skills)
			require.Len(t, bEntries, 1, "skills after lock replay: %+v", h.Skills)
			v := bEntries[0].Overrides["sub-agents/x.md"]
			require.NotNil(t, v, "URL override was dropped from skill b; got overrides=%v", bEntries[0].Overrides)
			assert.True(t, strings.HasSuffix(*v, "/content"), "override not resolved to a cache path: %s", *v)
		})
	}
}

// TestResolveFromLock_OverridesWithoutLockEntry: a direct URL skill with
// overrides that the lock does not cover fails instead of dropping them.
func TestResolveFromLock_OverridesWithoutLockEntry(t *testing.T) {
	h := &harness.Harness{
		Agent: "agents/code.md",
		Skills: []harness.SkillEntry{{
			Source:    "https://github.com/test-org/test-repo/tree/main/skills/b#sha256=abc",
			Overrides: map[string]*string{"sub-agents/x.md": nil},
		}},
		AllowedRemoteResources: []string{"https://github.com/test-org/"},
	}
	_, err := resolveFromLock(h, &lock.HarnessLock{}, t.TempDir(), nil, ui.New(io.Discard))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "skills[0]: file overrides cannot be applied")
}
