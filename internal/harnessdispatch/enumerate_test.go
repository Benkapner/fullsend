package harnessdispatch

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/fullsend-ai/fullsend/internal/config"
	"github.com/fullsend-ai/fullsend/internal/harness"
	"github.com/fullsend-ai/fullsend/internal/normevent"
)

func TestMergedConfigAgents_MissingFile(t *testing.T) {
	agents, err := MergedConfigAgents(t.TempDir())
	require.NoError(t, err)
	assert.Nil(t, agents)
}

func TestListTriggeredHarnesses_SkipsEmptyTrigger(t *testing.T) {
	dir := t.TempDir()
	writeHarnessConfig(t, dir, `agent: agents/triage.md
role: triage
slug: no-trigger
model: opus
image: ghcr.io/fullsend-ai/fullsend-sandbox:latest
`)
	cfg := config.NewPerRepoConfig(nil, "o/r")
	cfg.SetAgents([]config.AgentEntry{{Name: "issue-ping", Source: "harness/issue-ping.yaml"}})
	data, err := yaml.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), data, 0o644))

	dirCfg, err := config.LoadConfig(dir, config.LoadOpts{MissingOK: false})
	require.NoError(t, err)

	out, err := ListTriggeredHarnesses(context.Background(), dir, dirCfg, nil)
	require.NoError(t, err)
	assert.Empty(t, out)
}

func TestListTriggeredHarnesses_DuplicateName(t *testing.T) {
	dir := t.TempDir()
	cfg := config.NewPerRepoConfig(nil, "o/r")
	cfg.SetAgents([]config.AgentEntry{
		{Name: "Ping", Source: "harness/a.yaml"},
		{Name: "ping", Source: "harness/b.yaml"},
	})
	data, err := yaml.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), data, 0o644))

	dirCfg, err := config.LoadConfig(dir, config.LoadOpts{MissingOK: false})
	require.NoError(t, err)

	_, err = ListTriggeredHarnesses(context.Background(), dir, dirCfg, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate agent name")
}

func TestListTriggeredHarnesses_MissingHarness(t *testing.T) {
	dir := t.TempDir()
	harnessDir := filepath.Join(dir, "harness")
	require.NoError(t, os.MkdirAll(harnessDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(harnessDir, "good.yaml"), []byte(`agent: agents/triage.md
role: triage
slug: good
model: opus
image: ghcr.io/fullsend-ai/fullsend-sandbox:latest
trigger: event.entity.kind == "work_item"
`), 0o644))
	cfg := config.NewPerRepoConfig(nil, "o/r")
	cfg.SetAgents([]config.AgentEntry{
		{Name: "good", Source: "harness/good.yaml"},
		{Name: "missing", Source: "harness/missing.yaml"},
	})
	data, err := yaml.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), data, 0o644))

	dirCfg, err := config.LoadConfig(dir, config.LoadOpts{MissingOK: false})
	require.NoError(t, err)

	out, err := ListTriggeredHarnesses(context.Background(), dir, dirCfg, nil)
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, "good", out[0].Name)
}

func TestDispatch_FetchPolicyPlumbing(t *testing.T) {
	// Verify that Options.FetchPolicy is threaded through Dispatch →
	// ListTriggeredHarnesses → ResolveRegisteredPath. A URL-sourced agent
	// pointing at a non-github domain should be skipped (not error) when
	// the default policy is used, confirming the policy is applied.
	dir := t.TempDir()
	rawURL := "https://evil.example.com/org/repo/sha/harness/evil.yaml#sha256=" + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	allowlist := []string{"https://evil.example.com/"}

	cfg := config.NewPerRepoConfig(nil, "o/r")
	cfg.SetAgents([]config.AgentEntry{{Name: "evil", Source: rawURL}})
	cfg.SetAllowedRemoteResources(allowlist)
	data, err := yaml.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), data, 0o644))

	ev := mustEvent(t, "issue-opened.json")

	// nil FetchPolicy → DefaultPolicy (allows only github.com, raw.githubusercontent.com).
	// evil.example.com is not in DefaultPolicy's AllowedDomains, so the agent
	// is skipped. Dispatch should return empty results, not an error.
	refs, err := Dispatch(context.Background(), Options{
		ConfigDir: dir,
		Event:     ev,
		// FetchPolicy: nil → uses DefaultPolicy
	})
	require.NoError(t, err)
	assert.Empty(t, refs, "URL-sourced agent with non-github domain should be skipped by DefaultPolicy")
}

func TestMatchHarnesses_InvalidTrigger(t *testing.T) {
	ev := mustEvent(t, "issue-opened.json")
	matched, err := MatchHarnesses([]TriggeredHarness{{
		Name:    "bad",
		Harness: &harness.Harness{Trigger: "event.entity.kind == \"work_item\""},
	}, {
		Name:    "broken",
		Harness: &harness.Harness{Trigger: "!!!"},
	}}, ev)
	require.NoError(t, err)
	require.Len(t, matched, 1)
	assert.Equal(t, "bad", matched[0].Name)
}

func TestMatchHarnesses_NoCandidates(t *testing.T) {
	ev := mustEvent(t, "issue-opened.json")
	matched, err := MatchHarnesses(nil, ev)
	require.NoError(t, err)
	assert.Empty(t, matched)
}

func TestDispatch_PRMatch(t *testing.T) {
	dir := t.TempDir()
	harnessDir := filepath.Join(dir, "harness")
	require.NoError(t, os.MkdirAll(harnessDir, 0o755))
	prYAML := `agent: agents/triage.md
role: triage
slug: pr-ping
model: opus
image: ghcr.io/fullsend-ai/fullsend-sandbox:latest
trigger: |
  event.entity.kind == "change_proposal"
  && event.transition.kind == "label_changed"
  && event.transition.label.name == "ready-for-pr-ping"
`
	require.NoError(t, os.WriteFile(filepath.Join(harnessDir, "pr-ping.yaml"), []byte(prYAML), 0o644))
	cfg := config.NewPerRepoConfig(nil, "o/r")
	cfg.SetAgents([]config.AgentEntry{{Name: "pr-ping", Source: "harness/pr-ping.yaml"}})
	data, err := yaml.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), data, 0o644))

	ev := mustEvent(t, "ready-to-code-labeled.json")
	ev.Entity = normevent.Entity{Kind: normevent.EntityChangeProposal, ID: 100, URL: "https://github.com/o/r/pull/100"}
	ev.Transition.Label.Name = "ready-for-pr-ping"

	refs, err := Dispatch(context.Background(), Options{ConfigDir: dir, Event: ev})
	require.NoError(t, err)
	require.Len(t, refs, 1)
	assert.Equal(t, "pr-ping", refs[0].Agent)
}
