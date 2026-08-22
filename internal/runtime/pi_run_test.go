package runtime

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fullsend-ai/fullsend/internal/sandbox"
)

func TestTranslatePiModel(t *testing.T) {
	t.Setenv(piModelEnv, "")
	t.Setenv(piProviderEnv, "")
	assert.Equal(t, "anthropic-vertex/claude-opus-4-6", translatePiModel("opus"))
	assert.Equal(t, "anthropic-vertex/claude-sonnet-4-6", translatePiModel("sonnet"))
	assert.Equal(t, "anthropic-vertex/claude-haiku-4-5", translatePiModel("haiku"))
	assert.Equal(t, "anthropic-vertex/claude-opus-4-6", translatePiModel(""), "empty falls back to the opus alias")
	assert.Equal(t, "anthropic-vertex/claude-opus-4-8", translatePiModel("claude-opus-4-8"), "bare ids get the provider prefix")
	assert.Equal(t, "anthropic/claude-sonnet-4-6", translatePiModel("anthropic/claude-sonnet-4-6"), "provider/id passes through")

	t.Setenv(piProviderEnv, "anthropic")
	assert.Equal(t, "anthropic/claude-opus-4-6", translatePiModel("opus"))

	t.Setenv(piModelEnv, "google-vertex/gemini-2.5-pro")
	assert.Equal(t, "google-vertex/gemini-2.5-pro", translatePiModel("opus"), "FULLSEND_PI_MODEL overrides everything")
}

func TestPiThinkingFor(t *testing.T) {
	t.Parallel()
	for _, level := range []string{"low", "medium", "high", "xhigh", "max"} {
		got, ok := piThinkingFor(level)
		assert.True(t, ok)
		assert.Equal(t, level, got)
	}
	_, ok := piThinkingFor("")
	assert.False(t, ok)
	_, ok = piThinkingFor("turbo")
	assert.False(t, ok, "unknown levels are dropped rather than passed to pi")
}

func piTestParams() RunParams {
	return RunParams{
		SandboxName:   "sb",
		AgentBaseName: "triage",
		RepoDir:       sandbox.SandboxWorkspace + "/repo",
		Timeout:       time.Minute,
	}
}

func TestBuildPiRunCommand_Basic(t *testing.T) {
	t.Setenv(piModelEnv, "")
	t.Setenv(piProviderEnv, "")
	m := &piManifest{AgentName: "triage", Model: "opus", Tools: []string{"bash"}, BashAllowlist: []string{"gh"}, Hooks: &piHooksManifest{}}
	params := piTestParams()
	params.HooksSettingsPath = "/sandbox/claude-config/hooks.json"
	cmd := buildPiRunCommand(params, m)

	assert.True(t, strings.HasPrefix(cmd, `cd '/sandbox/workspace/repo' && . '/sandbox/workspace/.env' && export FULLSEND_PI_MANIFEST='/sandbox/pi-config/fullsend-manifest.json' && unset ANTHROPIC_API_KEY ANTHROPIC_AUTH_TOKEN ANTHROPIC_BASE_URL ANTHROPIC_VERTEX_BASE_URL && export GOOGLE_CLOUD_PROJECT="${ANTHROPIC_VERTEX_PROJECT_ID:-$GOOGLE_CLOUD_PROJECT}" && `+piHooksGuard("/sandbox/pi-config/fullsend-hooks.js", "/sandbox/pi-config/fullsend-manifest.json")+` && pi --print --mode json`), cmd)
	for _, want := range []string{
		"--no-approve", "--no-extensions", "--no-prompt-templates", "--no-themes",
		"--session-dir '/sandbox/pi-config/sessions'",
		"-e '/opt/pi-extensions/anthropic-vertex'",
		"-e '/sandbox/pi-config/fullsend-hooks.js'",
		"--tools 'bash'",
		"--model 'anthropic-vertex/claude-opus-4-6'",
		"'Run the agent task'",
	} {
		assert.Contains(t, cmd, want)
	}
	assert.NotContains(t, cmd, "--thinking")
	assert.NotContains(t, cmd, "2>>")
	assert.NotContains(t, cmd, "  ", "no double spaces")
	assert.True(t, strings.HasSuffix(cmd, "'Run the agent task'"))

	// Claude-on-Vertex: stray direct-API variables never reach pi, and the
	// project is pinned to the variable Claude Code on Vertex uses.
	unsetIdx := strings.Index(cmd, "&& unset ANTHROPIC_API_KEY ANTHROPIC_AUTH_TOKEN ANTHROPIC_BASE_URL ANTHROPIC_VERTEX_BASE_URL")
	envIdx := strings.Index(cmd, ". '/sandbox/workspace/.env'")
	piIdx := strings.Index(cmd, "&& pi ")
	assert.True(t, unsetIdx > envIdx && unsetIdx < piIdx, "unset runs after sourcing .env and before pi: %s", cmd)
	assert.Contains(t, cmd, `&& export GOOGLE_CLOUD_PROJECT="${ANTHROPIC_VERTEX_PROJECT_ID:-$GOOGLE_CLOUD_PROJECT}"`)

	// pi resolves the provider prefix case-insensitively; so must the gate.
	t.Setenv(piModelEnv, "Anthropic-Vertex/claude-opus-4-6")
	cmd = buildPiRunCommand(params, m)
	assert.Contains(t, cmd, "&& unset ANTHROPIC_API_KEY")
	assert.Contains(t, cmd, "--model 'Anthropic-Vertex/claude-opus-4-6'")
}

// TestPiHooksGuard runs the rendered guard under a real sh: it must exit 97
// without running what follows when the adapter is missing or modified, and
// fall through when both files are intact.
func TestPiHooksGuard(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("sha256sum"); err != nil {
		t.Skip("sha256sum not on PATH (stock macOS); the sandbox image has coreutils")
	}
	dir := t.TempDir()
	ext := filepath.Join(dir, "fullsend-hooks.js")
	manifest := filepath.Join(dir, "fullsend-manifest.json")
	require.NoError(t, os.WriteFile(manifest, []byte("{}"), 0o644))

	run := func() (int, string) {
		cmd := exec.Command("sh", "-c", piHooksGuard(ext, manifest)+" && echo RAN")
		out, err := cmd.CombinedOutput()
		code := 0
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		} else {
			require.NoError(t, err)
		}
		return code, string(out)
	}

	code, out := run()
	assert.Equal(t, piHooksMissingExit, code, "missing adapter")
	assert.NotContains(t, out, "RAN")
	assert.Contains(t, out, "refusing to run unhooked")

	require.NoError(t, os.WriteFile(ext, append(append([]byte{}, piHooksExtensionJS...), []byte("\n// tampered\n")...), 0o644))
	code, out = run()
	assert.Equal(t, piHooksMissingExit, code, "modified adapter")
	assert.NotContains(t, out, "RAN")

	require.NoError(t, os.WriteFile(ext, piHooksExtensionJS, 0o644))
	code, out = run()
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "RAN")

	require.NoError(t, os.Remove(manifest))
	code, _ = run()
	assert.Equal(t, piHooksMissingExit, code, "missing manifest")
}

func TestBuildPiRunCommand_DirectProviderKeepsAnthropicEnv(t *testing.T) {
	t.Setenv(piModelEnv, "")
	t.Setenv(piProviderEnv, "anthropic")
	cmd := buildPiRunCommand(piTestParams(), &piManifest{})
	assert.Contains(t, cmd, "--model 'anthropic/claude-opus-4-6'")
	assert.NotContains(t, cmd, "unset ANTHROPIC_API_KEY", "direct Anthropic provider needs its key")
	assert.NotContains(t, cmd, "GOOGLE_CLOUD_PROJECT")
}

func TestBuildPiRunCommand_HarnessOverridesAndFlags(t *testing.T) {
	t.Setenv(piModelEnv, "")
	t.Setenv(piProviderEnv, "")
	params := piTestParams()
	params.Model = "sonnet"
	params.Effort = "high"
	params.Debug = "*"
	// A manifest claiming hooks must not matter: the runner's signal decides.
	m := &piManifest{AgentName: "code", Model: "opus", Tools: nil, Hooks: &piHooksManifest{}}
	cmd := buildPiRunCommand(params, m)

	assert.Contains(t, cmd, "--model 'anthropic-vertex/claude-sonnet-4-6'", "harness model wins over the agent definition")
	assert.Contains(t, cmd, "--thinking 'high'")
	assert.NotContains(t, cmd, "--tools", "nil tools keeps pi's default tool set")
	assert.NotContains(t, cmd, "--no-builtin-tools")
	assert.NotContains(t, cmd, "fullsend-hooks.js", "no hook extension when the runner has security disabled")
	assert.NotContains(t, cmd, "test -f")
	assert.Contains(t, cmd, "-e '/opt/pi-extensions/anthropic-vertex'")
	assert.True(t, strings.HasSuffix(cmd, "'Run the agent task' 2>>'/sandbox/workspace/pi-debug.log'"), cmd)
}

func TestBuildPiRunCommand_EmptyToolRestriction(t *testing.T) {
	t.Setenv(piModelEnv, "")
	m := &piManifest{Tools: []string{}}
	cmd := buildPiRunCommand(piTestParams(), m)
	assert.Contains(t, cmd, "--no-builtin-tools")
	assert.NotContains(t, cmd, "--tools ")
}

func TestBuildPiRunCommand_QuotesRepoDirAndModel(t *testing.T) {
	t.Setenv(piModelEnv, "")
	params := piTestParams()
	params.RepoDir = "/sandbox/workspace/it's"
	params.Model = "anthropic/claude'x"
	cmd := buildPiRunCommand(params, &piManifest{})
	assert.Contains(t, cmd, `cd '/sandbox/workspace/it'\''s'`)
	assert.Contains(t, cmd, `--model 'anthropic/claude'\''x'`)
}
