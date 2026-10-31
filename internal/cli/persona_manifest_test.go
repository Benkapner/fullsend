package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fullsend-ai/fullsend/internal/harness"
	"github.com/fullsend-ai/fullsend/internal/ui"
)

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestBuildPersonaManifest_OriginsRemovedAndSorting(t *testing.T) {
	src := upstreamSkill(t)
	other := filepath.Join(t.TempDir(), "zeta")
	writeTree(t, other, map[string]string{
		"SKILL.md":             "---\nname: zeta\n---\n",
		"sub-agents/alpha.md":  "no frontmatter at all\n",
		"sub-agents/notes.txt": "ignored\n",
		"sub-agents/deep/x.md": "---\nname: nested\n---\n",
		"sub-agents/beta.md":   "---\nname: beta\n---\n",
		"other/ignored.md":     "---\nname: ignored\n---\n",
	})
	override := filepath.Join(t.TempDir(), "security.md")
	const overrideBody = "---\nname: security\n---\nlocal\n"
	require.NoError(t, os.WriteFile(override, []byte(overrideBody), 0o644))
	added := filepath.Join(t.TempDir(), "added.md")
	require.NoError(t, os.WriteFile(added, []byte("---\nname: added\n---\n"), 0o644))

	staged, err := stageSkills([]harness.SkillEntry{
		{Source: other},
		{Source: src, Overrides: map[string]*string{
			"sub-agents/security.md":        strPtr(override),
			"sub-agents/added.md":           strPtr(added),
			"sub-agents/user-experience.md": nil,
			"scripts/run.sh":                nil,
		}},
	}, t.TempDir(), func(string) {})
	require.NoError(t, err)

	m, err := buildPersonaManifest(staged)
	require.NoError(t, err)
	assert.Equal(t, 1, m.Version)

	type row struct{ skill, name, path, origin string }
	var rows []row
	for _, p := range m.Personas {
		rows = append(rows, row{p.Skill, p.Name, p.Path, p.Origin})
	}
	assert.Equal(t, []row{
		{"pr-review", "added", "sub-agents/added.md", originAdded},
		{"pr-review", "correctness", "sub-agents/correctness.md", originUpstream},
		{"pr-review", "security", "sub-agents/security.md", originOverride},
		{"zeta", "alpha", "sub-agents/alpha.md", originUpstream},
		{"zeta", "beta", "sub-agents/beta.md", originUpstream},
	}, rows)
	assert.Equal(t, sha256Hex(overrideBody), m.Personas[2].SHA256)
	assert.Equal(t, []removedPersona{{Skill: "pr-review", Path: "sub-agents/user-experience.md"}}, m.Removed,
		"only sub-agents/ removals are listed")
}

func TestBuildPersonaManifest_FrontmatterFields(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pr-review")
	writeTree(t, dir, map[string]string{
		"sub-agents/full.md": `---
name: full
description: Finds bugs
role: dimension
when: always relevant
categories: [injection, authz]
always: false
failure_severity: high
model: opus
unknown: {nested: true}
---
body
`,
		"sub-agents/bare.md":  "---\nname: bare\n---\n",
		"sub-agents/empty.md": "---\ncategories: []\ndescription: \"\"\n---\n",
	})
	m, err := buildPersonaManifest([]stagedSkill{{Dir: dir}})
	require.NoError(t, err)

	data, err := json.Marshal(m.Personas)
	require.NoError(t, err)
	var got []map[string]any
	require.NoError(t, json.Unmarshal(data, &got))
	require.Len(t, got, 3)

	bare, empty, full := got[0], got[1], got[2]
	assert.Equal(t, []string{"name", "origin", "path", "sha256", "skill"}, sortedKeys(bare))
	assert.Equal(t, "empty", empty["name"], "name falls back to the file stem")
	assert.Equal(t, []any{}, empty["categories"])
	assert.Equal(t, "", empty["description"])
	assert.Equal(t, "Finds bugs", full["description"])
	assert.Equal(t, "dimension", full["role"])
	assert.Equal(t, "always relevant", full["when"])
	assert.Equal(t, []any{"injection", "authz"}, full["categories"])
	assert.Equal(t, false, full["always"])
	assert.Equal(t, "high", full["failure_severity"])
	assert.NotContains(t, full, "model")
	assert.NotContains(t, full, "unknown")
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func TestBuildPersonaManifest_Errors(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		want string
	}{
		"bad yaml":         {"---\nname: [unclosed\n---\n", "pr-review/sub-agents/x.md: frontmatter does not parse"},
		"unclosed":         {"---\nname: x\n", "frontmatter does not parse: no closing ---"},
		"name not string":  {"---\nname: 3\n---\n", `frontmatter field "name" must be a string`},
		"role not string":  {"---\nrole: [a]\n---\n", `frontmatter field "role" must be a string`},
		"always not bool":  {"---\nalways: \"yes\"\n---\n", `frontmatter field "always" must be a bool`},
		"categories map":   {"---\ncategories: {a: b}\n---\n", `frontmatter field "categories" must be a list of strings`},
		"categories mixed": {"---\ncategories: [a, 2]\n---\n", `frontmatter field "categories" must be a list of strings`},
	} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "pr-review")
			writeTree(t, dir, map[string]string{"sub-agents/x.md": tc.body})
			_, err := buildPersonaManifest([]stagedSkill{{Dir: dir}})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "personas.json: ")
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestBuildPersonaManifest_DuplicateNameAcrossSkills(t *testing.T) {
	a := filepath.Join(t.TempDir(), "alpha")
	b := filepath.Join(t.TempDir(), "beta")
	writeTree(t, a, map[string]string{"sub-agents/security.md": "---\nname: security\n---\n"})
	writeTree(t, b, map[string]string{"sub-agents/sec.md": "---\nname: security\n---\n"})
	_, err := buildPersonaManifest([]stagedSkill{{Dir: a}, {Dir: b}})
	require.Error(t, err)
	assert.Equal(t, `personas.json: duplicate persona name "security" in alpha/sub-agents/security.md and beta/sub-agents/sec.md`, err.Error())
}

// An override may reuse another file's bytes inside the same skill, so a
// repeated name within one skill is kept: each entry has its own path.
func TestBuildPersonaManifest_DuplicateNameWithinSkillAllowed(t *testing.T) {
	src := upstreamSkill(t)
	staged, err := stageSkills([]harness.SkillEntry{
		{Source: src, Overrides: map[string]*string{
			"sub-agents/security.md": strPtr(filepath.Join(src, "sub-agents", "correctness.md")),
		}},
	}, t.TempDir(), func(string) {})
	require.NoError(t, err)
	m, err := buildPersonaManifest(staged)
	require.NoError(t, err)

	byPath := map[string]personaEntry{}
	for _, p := range m.Personas {
		byPath[p.Path] = p
	}
	sec, cor := byPath["sub-agents/security.md"], byPath["sub-agents/correctness.md"]
	assert.Equal(t, "correctness", sec.Name)
	assert.Equal(t, originOverride, sec.Origin)
	assert.Equal(t, originUpstream, cor.Origin)
	assert.Equal(t, cor.SHA256, sec.SHA256)
	assert.Equal(t, "sub-agents/correctness.md", m.Personas[0].Path, "same name sorts by path")
	assert.Equal(t, "sub-agents/security.md", m.Personas[1].Path)
}

func TestBuildPersonaManifest_UnreadableSubAgents(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pr-review")
	writeTree(t, dir, map[string]string{"sub-agents": "a file, not a directory"})
	_, err := buildPersonaManifest([]stagedSkill{{Dir: dir}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading pr-review/sub-agents")
}

func TestWritePersonaManifest_EmptyRoster(t *testing.T) {
	runDir := t.TempDir()
	dir := filepath.Join(t.TempDir(), "plain")
	writeTree(t, dir, map[string]string{"SKILL.md": "---\nname: plain\n---\n"})

	path, err := writePersonaManifest([]stagedSkill{{Dir: dir}}, runDir)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(runDir, "personas.json"), path)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "{\n  \"version\": 1,\n  \"personas\": [],\n  \"removed\": []\n}\n", string(data))

	path, err = writePersonaManifest(nil, runDir)
	require.NoError(t, err)
	data, err = os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"personas": []`)

	_, err = writePersonaManifest(nil, filepath.Join(runDir, "missing", "dir"))
	require.Error(t, err)
}

func envValue(env []string, key string) (string, int) {
	var val string
	n := 0
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, key+"="); ok {
			val = v
			n++
		}
	}
	return val, n
}

func TestWithPersonasFile_RunnerEnvCannotOverride(t *testing.T) {
	runDir := t.TempDir()
	want := filepath.Join(runDir, "personas.json")
	h := &harness.Harness{RunnerEnv: map[string]string{
		"FULLSEND_PERSONAS_FILE": "/tmp/spoofed.json",
		"OTHER":                  "kept",
	}}

	for name, env := range map[string][]string{
		"post-script": withPersonasFile(postScriptEnv(h, ""), runDir),
		"validation":  validationEnv(h, "/repo", runDir),
	} {
		t.Run(name, func(t *testing.T) {
			got, n := envValue(env, "FULLSEND_PERSONAS_FILE")
			assert.Equal(t, want, got)
			assert.Equal(t, 1, n, "the runner's value is the only one")
			assert.Equal(t, "FULLSEND_PERSONAS_FILE="+want, env[len(env)-1], "appended after runner_env")
			other, _ := envValue(env, "OTHER")
			assert.Equal(t, "kept", other)
		})
	}
}

// TestPersonaManifest_ReadyBeforePreScript stages the skills the way
// runAgent does before the pre-script, then runs a pre-script that
// requires the manifest to exist at FULLSEND_PERSONAS_FILE — even when
// runner_env tries to point the variable elsewhere.
func TestPersonaManifest_ReadyBeforePreScript(t *testing.T) {
	src := upstreamSkill(t)
	runDir := t.TempDir()
	seen := filepath.Join(t.TempDir(), "seen.json")
	h := &harness.Harness{
		Skills: []harness.SkillEntry{
			{Source: src, Overrides: map[string]*string{"sub-agents/user-experience.md": nil}},
		},
		RunnerEnv: map[string]string{"FULLSEND_PERSONAS_FILE": "/tmp/spoofed.json"},
		PreScript: writePreScript(t, `test -f "${FULLSEND_PERSONAS_FILE}"
cp "${FULLSEND_PERSONAS_FILE}" `+seen+"\n"),
	}
	printer := ui.New(io.Discard)

	_, err := stageRunSkills(h, runDir, printer)
	require.NoError(t, err)
	_, err = runPreScript(h, runDir, "", printer)
	require.NoError(t, err)

	data, err := os.ReadFile(seen)
	require.NoError(t, err)
	var m personaManifest
	require.NoError(t, json.Unmarshal(data, &m))
	assert.Equal(t, []removedPersona{{Skill: "pr-review", Path: "sub-agents/user-experience.md"}}, m.Removed)
}
