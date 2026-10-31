package cli

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fullsend-ai/fullsend/internal/harness"
	"github.com/fullsend-ai/fullsend/internal/ui"
)

// writeTree creates files (slash paths relative to root) with the given
// content, all mode 0644 unless the path ends in ".sh" (0755).
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		mode := fs.FileMode(0o644)
		if strings.HasSuffix(rel, ".sh") {
			mode = 0o755
		}
		require.NoError(t, os.WriteFile(p, []byte(content), mode))
		require.NoError(t, os.Chmod(p, mode))
	}
}

// snapshotTree reads every regular file under root keyed by slash path,
// with its mode, so a test can prove a tree was not modified.
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	require.NoError(t, filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		require.NoError(t, err)
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		data, err := os.ReadFile(p)
		require.NoError(t, err)
		info, err := d.Info()
		require.NoError(t, err)
		out[filepath.ToSlash(rel)] = info.Mode().String() + ":" + string(data)
		return nil
	}))
	return out
}

func strPtr(s string) *string { return &s }

func upstreamSkill(t *testing.T) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "cache", "pr-review")
	writeTree(t, src, map[string]string{
		"SKILL.md":                      "---\nname: pr-review\n---\nbody\n",
		"scripts/run.sh":                "#!/bin/sh\necho hi\n",
		"sub-agents/security.md":        "---\nname: security\n---\nupstream security\n",
		"sub-agents/correctness.md":     "---\nname: correctness\n---\nupstream correctness\n",
		"sub-agents/user-experience.md": "---\nname: user-experience\n---\nux\n",
	})
	return src
}

func TestStageSkills_ReplaceAddRemove(t *testing.T) {
	src := upstreamSkill(t)
	before := snapshotTree(t, src)

	overrideDir := t.TempDir()
	writeTree(t, overrideDir, map[string]string{
		"security.md": "---\nname: security\n---\nlocal security\n",
		"style.md":    "---\nname: style\n---\nnew persona\n",
	})

	var warnings []string
	stageRoot := filepath.Join(t.TempDir(), "skills")
	staged, err := stageSkills([]harness.SkillEntry{
		{Source: src, Overrides: map[string]*string{
			"sub-agents/security.md":        strPtr(filepath.Join(overrideDir, "security.md")),
			"sub-agents/style.md":           strPtr(filepath.Join(overrideDir, "style.md")),
			"sub-agents/user-experience.md": nil,
			"sub-agents/missing.md":         nil,
		}},
	}, stageRoot, func(w string) { warnings = append(warnings, w) })
	require.NoError(t, err)
	require.Len(t, staged, 1)

	s := staged[0]
	assert.Equal(t, filepath.Join(stageRoot, "0", "pr-review"), s.Dir)
	got := snapshotTree(t, s.Dir)
	assert.Equal(t, "-rw-r--r--:---\nname: security\n---\nlocal security\n", got["sub-agents/security.md"])
	assert.Equal(t, "-rw-r--r--:---\nname: style\n---\nnew persona\n", got["sub-agents/style.md"])
	assert.NotContains(t, got, "sub-agents/user-experience.md")
	assert.Equal(t, before["sub-agents/correctness.md"], got["sub-agents/correctness.md"])
	assert.Equal(t, before["scripts/run.sh"], got["scripts/run.sh"], "file modes are kept")
	assert.True(t, strings.HasPrefix(got["scripts/run.sh"], "-rwxr-xr-x"))

	assert.Equal(t, originOverride, s.origin("sub-agents/security.md"))
	assert.Equal(t, originAdded, s.origin("sub-agents/style.md"))
	assert.Equal(t, originUpstream, s.origin("sub-agents/correctness.md"))
	assert.Equal(t, []string{"sub-agents/user-experience.md"}, s.Removed)

	require.Len(t, warnings, 1)
	assert.Equal(t, `skills[0]: override "sub-agents/missing.md" is null but the skill has no such file; nothing removed`, warnings[0])

	assert.Equal(t, before, snapshotTree(t, src), "the resolved source tree must stay byte-identical")
}

func TestStageSkills_ReplaceKeepsUpstreamMode(t *testing.T) {
	src := upstreamSkill(t)
	override := filepath.Join(t.TempDir(), "run.sh")
	require.NoError(t, os.WriteFile(override, []byte("#!/bin/sh\necho override\n"), 0o644))

	staged, err := stageSkills([]harness.SkillEntry{
		{Source: src, Overrides: map[string]*string{"scripts/run.sh": strPtr(override)}},
	}, t.TempDir(), func(string) {})
	require.NoError(t, err)
	info, err := os.Stat(filepath.Join(staged[0].Dir, "scripts", "run.sh"))
	require.NoError(t, err)
	assert.Equal(t, fs.FileMode(0o755), info.Mode().Perm())
}

func TestStageSkills_NoOverridesKeepsSource(t *testing.T) {
	a := filepath.Join(t.TempDir(), "a")
	stageRoot := filepath.Join(t.TempDir(), "skills")
	staged, err := stageSkills([]harness.SkillEntry{{Source: a}, {Source: ""}}, stageRoot, func(string) {})
	require.NoError(t, err)
	assert.Equal(t, []string{a}, stagedSkillDirs(staged))
	_, statErr := os.Stat(stageRoot)
	assert.True(t, errors.Is(statErr, fs.ErrNotExist), "nothing is staged without overrides")
	assert.Nil(t, stagedSkillDirs(nil))
}

func TestStageSkills_SymlinkRefused(t *testing.T) {
	src := upstreamSkill(t)
	require.NoError(t, os.Symlink("/etc/passwd", filepath.Join(src, "sub-agents", "link.md")))

	_, err := stageSkills([]harness.SkillEntry{
		{Source: src, Overrides: map[string]*string{"sub-agents/user-experience.md": nil}},
	}, t.TempDir(), func(string) {})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `skills[0]: skill tree contains a symlink at "sub-agents/link.md"`)
}

func TestStageSkills_BadKeysRefusedAtApplyTime(t *testing.T) {
	src := upstreamSkill(t)
	for key, want := range map[string]string{
		"../escape.md":  "must not contain path traversal segments",
		"/etc/passwd":   "must be relative (no leading /)",
		".":             "resolves outside the skill directory",
		"sub-agents":    "names a directory; only files can be overridden",
		"SKILL.md/x.md": "skills[0]: override \"SKILL.md/x.md\"",
		"a\x00b":        "must not contain null bytes",
	} {
		t.Run(key, func(t *testing.T) {
			_, err := stageSkills([]harness.SkillEntry{
				{Source: src, Overrides: map[string]*string{key: nil}},
			}, t.TempDir(), func(string) {})
			require.Error(t, err)
			assert.Contains(t, err.Error(), want)
		})
	}
}

func TestStageSkills_OverrideSourceErrors(t *testing.T) {
	src := upstreamSkill(t)
	dir := t.TempDir()
	link := filepath.Join(dir, "link.md")
	require.NoError(t, os.Symlink(filepath.Join(src, "SKILL.md"), link))

	for name, val := range map[string]string{
		"missing": filepath.Join(dir, "nope.md"),
		"symlink": link,
		"dir":     dir,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := stageSkills([]harness.SkillEntry{
				{Source: src, Overrides: map[string]*string{"sub-agents/security.md": strPtr(val)}},
			}, t.TempDir(), func(string) {})
			require.Error(t, err)
			assert.Contains(t, err.Error(), `skills[0]: override "sub-agents/security.md": reading override source`)
		})
	}
}

func TestStageSkills_SourceErrors(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(file, nil, 0o644))
	for name, src := range map[string]string{
		"missing": filepath.Join(t.TempDir(), "nope"),
		"not dir": file,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := stageSkills([]harness.SkillEntry{
				{Source: src, Overrides: map[string]*string{"x.md": nil}},
			}, t.TempDir(), func(string) {})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "skills[0]")
		})
	}
}

func TestStageRunSkills_WritesManifestAndStagesOverrides(t *testing.T) {
	src := upstreamSkill(t)
	plain := filepath.Join(t.TempDir(), "lint")
	writeTree(t, plain, map[string]string{"SKILL.md": "---\nname: lint\n---\n"})
	runDir := t.TempDir()
	h := &harness.Harness{Skills: []harness.SkillEntry{
		{Source: plain},
		{Source: src, Overrides: map[string]*string{"sub-agents/user-experience.md": nil}},
	}}

	dirs, err := stageRunSkills(h, runDir, ui.New(io.Discard))
	require.NoError(t, err)
	assert.Equal(t, []string{plain, filepath.Join(runDir, "skills", "1", "pr-review")}, dirs)
	assert.FileExists(t, filepath.Join(runDir, personasFileName))
	assert.FileExists(t, filepath.Join(src, "sub-agents", "user-experience.md"))
}

func TestStageRunSkills_Errors(t *testing.T) {
	src := upstreamSkill(t)
	h := &harness.Harness{Skills: []harness.SkillEntry{
		{Source: src, Overrides: map[string]*string{"../x": nil}},
	}}
	_, err := stageRunSkills(h, t.TempDir(), ui.New(io.Discard))
	require.Error(t, err)

	bad := filepath.Join(t.TempDir(), "bad")
	writeTree(t, bad, map[string]string{"sub-agents/a.md": "---\nalways: yes please\n---\n"})
	_, err = stageRunSkills(&harness.Harness{Skills: []harness.SkillEntry{{Source: bad}}}, t.TempDir(), ui.New(io.Discard))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `frontmatter field "always" must be a bool`)
}

func TestSetSkillDirs_ReplacesBootstrapDirs(t *testing.T) {
	h := &harness.Harness{Skills: []harness.SkillEntry{{Source: "/src/a"}}}
	boot, err := newHarnessBootstrap(h, "sb", "agent", "", nil, nil, "", "/repo", false)
	require.NoError(t, err)
	b, ok := boot.(interface{ setSkillDirs([]string) })
	require.True(t, ok)
	b.setSkillDirs([]string{"/run/skills/0/a"})
	assert.Equal(t, []string{"/run/skills/0/a"}, boot.SkillDirs())
}

func TestUploadPersonaManifest(t *testing.T) {
	runDir := t.TempDir()
	var gotCmd, gotLocal, gotRemote string
	exec := func(_ string, cmd string, _ time.Duration) (string, string, int, error) {
		gotCmd = cmd
		return "", "", 0, nil
	}
	upload := func(_ string, local, remote string) error {
		gotLocal, gotRemote = local, remote
		return nil
	}
	require.NoError(t, uploadPersonaManifest(exec, upload, "sb", runDir))
	assert.Equal(t, "mkdir -p /sandbox/workspace/.fullsend", gotCmd)
	assert.Equal(t, filepath.Join(runDir, "personas.json"), gotLocal)
	assert.Equal(t, "/sandbox/workspace/.fullsend/personas.json", gotRemote)
	assert.Equal(t, "export FULLSEND_PERSONAS_FILE=/sandbox/workspace/.fullsend/personas.json", personasEnvLine())

	failExec := func(string, string, time.Duration) (string, string, int, error) {
		return "", "", 0, errors.New("exec boom")
	}
	assert.ErrorContains(t, uploadPersonaManifest(failExec, upload, "sb", runDir), "exec boom")
	codeExec := func(string, string, time.Duration) (string, string, int, error) {
		return "", "denied", 1, nil
	}
	assert.ErrorContains(t, uploadPersonaManifest(codeExec, upload, "sb", runDir), "denied")
	failUpload := func(string, string, string) error { return errors.New("upload boom") }
	assert.ErrorContains(t, uploadPersonaManifest(exec, failUpload, "sb", runDir), "upload boom")
}

func TestPersonasFile_IsReservedSandboxKey(t *testing.T) {
	assert.True(t, isReservedSandboxKey("FULLSEND_PERSONAS_FILE"))
}
