package scaffold

import (
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writePinCIJobIdentityScripts(t *testing.T) (root, pinScript string) {
	t.Helper()
	root = t.TempDir()
	writeGitLabScript(t, root, ".gitlab/ci/scripts/trust-ci-server-ca.sh")
	pinScript = writeGitLabScript(t, root, gitlabPinCIJobIdentityScriptPath)
	return root, pinScript
}

func sourcePinScript(t *testing.T, root, script string, extraEnv []string) (combined string, err error) {
	t.Helper()
	cmd := exec.Command("bash", "-c", "set -euo pipefail; . \"$SCRIPT\"; echo PINNED_PROJECT=$FULLSEND_PINNED_PROJECT_ID; echo PINNED_PIPELINE=$FULLSEND_PINNED_PIPELINE_ID; echo PINNED_REF=$FULLSEND_PINNED_REF; echo PINNED_SOURCE=$FULLSEND_PINNED_PIPELINE_SOURCE")
	cmd.Env = append([]string{
		"SCRIPT=" + script,
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
		"CI_PROJECT_DIR=" + root,
		"RUNNER_TEMP=" + t.TempDir(),
		"FULLSEND_ADMIT_SOURCE=api",
		"CI_JOB_TOKEN=job-token",
	}, extraEnv...)
	out, runErr := cmd.CombinedOutput()
	return string(out), runErr
}

type pinAPIState struct {
	jobJSON        string
	projectJSON    string
	branchJSON     string
	pipelineJSON   string
	jobStatus      int
	projectStatus  int
	branchStatus   int
	pipelineStatus int
	jobHits        atomic.Int32
	sawPAT         atomic.Bool
	jobAuth        atomic.Bool
}

func buildPinAPIMux(st *pinAPIState) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/job", func(w http.ResponseWriter, r *http.Request) {
		st.jobHits.Add(1)
		if r.Header.Get("JOB-TOKEN") == "job-token" {
			st.jobAuth.Store(true)
		}
		if r.Header.Get("PRIVATE-TOKEN") != "" {
			st.sawPAT.Store(true)
		}
		status := st.jobStatus
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(st.jobJSON))
	})
	mux.HandleFunc("/api/v4/projects/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "" {
			st.sawPAT.Store(true)
		}
		path := r.URL.Path
		status := http.StatusOK
		body := ""
		switch {
		case strings.Contains(path, "/repository/branches/"):
			status = st.branchStatus
			body = st.branchJSON
		case strings.Contains(path, "/pipelines/"):
			status = st.pipelineStatus
			body = st.pipelineJSON
		default:
			status = st.projectStatus
			body = st.projectJSON
		}
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
	return mux
}

func startPinAPI(t *testing.T, st *pinAPIState) *httptest.Server {
	t.Helper()
	return httptest.NewTLSServer(buildPinAPIMux(st))
}

// startPinAPIOnIPv6Loopback serves the same mock API as startPinAPI, but
// bound to the IPv6 loopback address so its httptest.Server.URL is a
// bracketed IPv6 literal (https://[::1]:PORT) — used to prove the identity
// pin's CI_API_V4_URL validation admits a real IPv6 API root rather than
// only asserting the validation didn't reject a fabricated one.
func startPinAPIOnIPv6Loopback(t *testing.T, st *pinAPIState) *httptest.Server {
	t.Helper()
	l, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback unavailable in this environment: %v", err)
	}
	srv := httptest.NewUnstartedServer(buildPinAPIMux(st))
	srv.Listener.Close()
	srv.Listener = l
	srv.StartTLS()
	return srv
}

func pinJobJSON(projectID, pipelineID, ref string) string {
	return fmt.Sprintf(`{"id":9,"ref":%q,"pipeline":{"id":%s,"project_id":%s,"ref":%q}}`, ref, pipelineID, projectID, ref)
}

func pinTLSEnv(t *testing.T, srv *httptest.Server) []string {
	t.Helper()
	return []string{
		"CI_API_V4_URL=" + srv.URL + "/api/v4",
		"CI_SERVER_TLS_CA_FILE=" + writeServerCA(t, srv),
	}
}

func writeServerCA(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	cert := srv.Certificate()
	require.NotNil(t, cert)
	path := filepath.Join(t.TempDir(), "server-ca.pem")
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: cert.Raw,
	}), 0o644))
	return path
}

func TestPinCIJobIdentity_AdmitsMatchingAPISource(t *testing.T) {
	root, script := writePinCIJobIdentityScripts(t)
	st := &pinAPIState{
		jobJSON:      pinJobJSON("42", "100", "main"),
		projectJSON:  `{"id":42,"default_branch":"main","path_with_namespace":"group/project"}`,
		branchJSON:   `{"name":"main","protected":true}`,
		pipelineJSON: `{"id":100,"source":"api","user":{"id":7}}`,
	}
	srv := startPinAPI(t, st)
	t.Cleanup(srv.Close)

	out, err := sourcePinScript(t, root, script, pinTLSEnv(t, srv))
	require.NoError(t, err, "stdout/stderr: %s", out)
	assert.Contains(t, out, "PINNED_PROJECT=42")
	assert.Contains(t, out, "PINNED_PIPELINE=100")
	assert.Contains(t, out, "PINNED_REF=main")
	assert.Contains(t, out, "PINNED_SOURCE=api")
	assert.True(t, st.jobAuth.Load(), "job lookup must send JOB-TOKEN")
	assert.False(t, st.sawPAT.Load(), "gate curls must not send the role PAT")
}

func TestPinCIJobIdentity_DeniesParentPipelineSource(t *testing.T) {
	root, script := writePinCIJobIdentityScripts(t)
	st := &pinAPIState{
		jobJSON:      pinJobJSON("42", "100", "main"),
		projectJSON:  `{"id":42,"default_branch":"main","path_with_namespace":"group/project"}`,
		branchJSON:   `{"name":"main","protected":true}`,
		pipelineJSON: `{"id":100,"source":"parent_pipeline","user":{"id":7}}`,
	}
	srv := startPinAPI(t, st)
	t.Cleanup(srv.Close)

	out, err := sourcePinScript(t, root, script, pinTLSEnv(t, srv))
	require.Error(t, err, "stdout/stderr: %s", out)
	assert.Contains(t, out, "parent_pipeline")
	assert.Contains(t, out, "disjoint allowlist deny")
}

func TestPinCIJobIdentity_DeniesScheduleWhenAgentAdmitsAPI(t *testing.T) {
	root, script := writePinCIJobIdentityScripts(t)
	st := &pinAPIState{
		jobJSON:      pinJobJSON("42", "100", "main"),
		projectJSON:  `{"id":42,"default_branch":"main","path_with_namespace":"group/project"}`,
		branchJSON:   `{"name":"main","protected":true}`,
		pipelineJSON: `{"id":100,"source":"schedule"}`,
	}
	srv := startPinAPI(t, st)
	t.Cleanup(srv.Close)

	out, err := sourcePinScript(t, root, script, pinTLSEnv(t, srv))
	require.Error(t, err, "stdout/stderr: %s", out)
	assert.Contains(t, out, "schedule")
	assert.Contains(t, out, "disjoint allowlist deny")
}

func TestPinCIJobIdentity_DeniesProtectedNonDefaultRef(t *testing.T) {
	root, script := writePinCIJobIdentityScripts(t)
	st := &pinAPIState{
		jobJSON:      pinJobJSON("42", "100", "release-1.0"),
		projectJSON:  `{"id":42,"default_branch":"main","path_with_namespace":"group/project"}`,
		branchJSON:   `{"name":"release-1.0","protected":true}`,
		pipelineJSON: `{"id":100,"source":"api"}`,
	}
	srv := startPinAPI(t, st)
	t.Cleanup(srv.Close)

	out, err := sourcePinScript(t, root, script, pinTLSEnv(t, srv))
	require.Error(t, err, "stdout/stderr: %s", out)
	assert.Contains(t, out, "protected-but-non-default")
	assert.Contains(t, out, "release-1.0")
}

func TestPinCIJobIdentity_DeniesUnprotectedDefaultBranch(t *testing.T) {
	root, script := writePinCIJobIdentityScripts(t)
	st := &pinAPIState{
		jobJSON:      pinJobJSON("42", "100", "main"),
		projectJSON:  `{"id":42,"default_branch":"main","path_with_namespace":"group/project"}`,
		branchJSON:   `{"name":"main","protected":false}`,
		pipelineJSON: `{"id":100,"source":"api"}`,
	}
	srv := startPinAPI(t, st)
	t.Cleanup(srv.Close)

	out, err := sourcePinScript(t, root, script, pinTLSEnv(t, srv))
	require.Error(t, err, "stdout/stderr: %s", out)
	assert.Contains(t, out, "is not protected")
}

func TestPinCIJobIdentity_InvalidJobTokenFailsClosed(t *testing.T) {
	root, script := writePinCIJobIdentityScripts(t)
	st := &pinAPIState{
		jobStatus: http.StatusUnauthorized,
		jobJSON:   `{"message":"401 Unauthorized"}`,
	}
	srv := startPinAPI(t, st)
	t.Cleanup(srv.Close)

	out, err := sourcePinScript(t, root, script, pinTLSEnv(t, srv))
	require.Error(t, err, "stdout/stderr: %s", out)
	assert.Contains(t, out, "CI_JOB_TOKEN job lookup failed")
}

func TestPinCIJobIdentity_ProjectLookupFailsClosed(t *testing.T) {
	root, script := writePinCIJobIdentityScripts(t)
	st := &pinAPIState{
		jobJSON:       pinJobJSON("42", "100", "main"),
		projectStatus: http.StatusForbidden,
		projectJSON:   `{"message":"403 Forbidden"}`,
	}
	srv := startPinAPI(t, st)
	t.Cleanup(srv.Close)

	out, err := sourcePinScript(t, root, script, pinTLSEnv(t, srv))
	require.Error(t, err, "stdout/stderr: %s", out)
	assert.Contains(t, out, "cannot read pinned project")
}

func TestPinCIJobIdentity_BranchLookupFailsClosed(t *testing.T) {
	root, script := writePinCIJobIdentityScripts(t)
	st := &pinAPIState{
		jobJSON:      pinJobJSON("42", "100", "main"),
		projectJSON:  `{"id":42,"default_branch":"main","path_with_namespace":"group/project"}`,
		branchStatus: http.StatusForbidden,
		branchJSON:   `{"message":"403 Forbidden"}`,
	}
	srv := startPinAPI(t, st)
	t.Cleanup(srv.Close)

	out, err := sourcePinScript(t, root, script, pinTLSEnv(t, srv))
	require.Error(t, err, "stdout/stderr: %s", out)
	assert.Contains(t, out, "cannot read pinned branch")
}

func TestPinCIJobIdentity_PipelineLookupFailsClosed(t *testing.T) {
	root, script := writePinCIJobIdentityScripts(t)
	st := &pinAPIState{
		jobJSON:        pinJobJSON("42", "100", "main"),
		projectJSON:    `{"id":42,"default_branch":"main","path_with_namespace":"group/project"}`,
		branchJSON:     `{"name":"main","protected":true}`,
		pipelineStatus: http.StatusForbidden,
		pipelineJSON:   `{"message":"403 Forbidden"}`,
	}
	srv := startPinAPI(t, st)
	t.Cleanup(srv.Close)

	out, err := sourcePinScript(t, root, script, pinTLSEnv(t, srv))
	require.Error(t, err, "stdout/stderr: %s", out)
	assert.Contains(t, out, "cannot read pinned pipeline")
}

func TestPinCIJobIdentity_EmptyJobTokenFailsClosed(t *testing.T) {
	root, script := writePinCIJobIdentityScripts(t)
	out, err := sourcePinScript(t, root, script, []string{
		"CI_JOB_TOKEN=",
		"CI_API_V4_URL=https://gitlab.example/api/v4",
	})
	require.Error(t, err, "stdout/stderr: %s", out)
	assert.Contains(t, out, "CI_JOB_TOKEN is empty")
}

func TestPinCIJobIdentity_RejectsNonHTTPSAPIURL(t *testing.T) {
	root, script := writePinCIJobIdentityScripts(t)
	out, err := sourcePinScript(t, root, script, []string{
		"CI_API_V4_URL=http://gitlab.example/api/v4",
	})
	require.Error(t, err, "stdout/stderr: %s", out)
	assert.Contains(t, out, "not an https GitLab API root")
}

func TestPinCIJobIdentity_RejectsBraceGlobAPIURL(t *testing.T) {
	root, script := writePinCIJobIdentityScripts(t)
	out, err := sourcePinScript(t, root, script, []string{
		"CI_API_V4_URL=https://{attacker.example,gitlab.example}/api/v4",
	})
	require.Error(t, err, "stdout/stderr: %s", out)
	assert.Contains(t, out, "not permitted in a GitLab API root")
}

func TestPinCIJobIdentity_RejectsBracketRangeAPIURL(t *testing.T) {
	root, script := writePinCIJobIdentityScripts(t)
	out, err := sourcePinScript(t, root, script, []string{
		"CI_API_V4_URL=https://gitlab[1-2].example/api/v4",
	})
	require.Error(t, err, "stdout/stderr: %s", out)
	assert.Contains(t, out, "not permitted in a GitLab API root")
}

func TestPinCIJobIdentity_AllowsIPv6LiteralAPIURL(t *testing.T) {
	root, script := writePinCIJobIdentityScripts(t)
	st := &pinAPIState{
		jobJSON:      pinJobJSON("42", "100", "main"),
		projectJSON:  `{"id":42,"default_branch":"main","path_with_namespace":"group/project"}`,
		branchJSON:   `{"name":"main","protected":true}`,
		pipelineJSON: `{"id":100,"source":"api","user":{"id":7}}`,
	}
	srv := startPinAPIOnIPv6Loopback(t, st)
	t.Cleanup(srv.Close)
	require.Contains(t, srv.URL, "[::1]", "test setup: expected an IPv6-literal server URL")

	out, err := sourcePinScript(t, root, script, pinTLSEnv(t, srv))
	require.NoError(t, err, "stdout/stderr: %s", out)
	assert.NotContains(t, out, "not permitted in a GitLab API root")
	assert.Contains(t, out, "PINNED_PROJECT=42")
}

func TestPinCIJobIdentity_RejectsUserinfoAPIURL(t *testing.T) {
	root, script := writePinCIJobIdentityScripts(t)
	out, err := sourcePinScript(t, root, script, []string{
		"CI_API_V4_URL=https://gitlab.example@attacker.example/api/v4",
	})
	require.Error(t, err, "stdout/stderr: %s", out)
	assert.Contains(t, out, "not permitted in a GitLab API root")
}

func TestPinCIJobIdentity_RejectsUnknownAdmitSource(t *testing.T) {
	root, script := writePinCIJobIdentityScripts(t)
	out, err := sourcePinScript(t, root, script, []string{
		"FULLSEND_ADMIT_SOURCE=api,schedule",
		"CI_API_V4_URL=https://gitlab.example/api/v4",
	})
	require.Error(t, err, "stdout/stderr: %s", out)
	assert.Contains(t, out, "disjoint per-job allowlist")
}

func TestPinCIJobIdentity_IgnoresHTTPProxy(t *testing.T) {
	root, script := writePinCIJobIdentityScripts(t)
	st := &pinAPIState{
		jobJSON:      pinJobJSON("42", "100", "main"),
		projectJSON:  `{"id":42,"default_branch":"main","path_with_namespace":"group/project"}`,
		branchJSON:   `{"name":"main","protected":true}`,
		pipelineJSON: `{"id":100,"source":"api"}`,
	}
	srv := startPinAPI(t, st)
	t.Cleanup(srv.Close)

	proxyHits := atomic.Int32{}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		proxyHits.Add(1)
		http.Error(w, "proxy should not be used", http.StatusBadGateway)
	}))
	t.Cleanup(proxy.Close)

	out, err := sourcePinScript(t, root, script, append(pinTLSEnv(t, srv),
		"HTTP_PROXY="+proxy.URL,
		"HTTPS_PROXY="+proxy.URL,
		"ALL_PROXY="+proxy.URL,
		"http_proxy="+proxy.URL,
		"https_proxy="+proxy.URL,
		"all_proxy="+proxy.URL,
	))
	require.NoError(t, err, "stdout/stderr: %s", out)
	assert.Equal(t, int32(0), proxyHits.Load(), "gate curls must ignore trigger-influenced proxy settings")
	assert.Contains(t, out, "PINNED_SOURCE=api")
}

// TestPinCIJobIdentity_AuthHeaderNotOnCurlArgv verifies that
// fullsend_gate_curl rewrites a caller's `-H "JOB-TOKEN: ..."` into
// `-H @tempfile` before invoking curl, so the token value itself never
// appears in curl's argv (visible via /proc/<pid>/cmdline, `ps`, or
// execve audit logs otherwise). A shim `curl` binary ahead of the real
// one on PATH logs its raw argv, then execs the real curl so the pin
// script still completes normally.
func TestPinCIJobIdentity_AuthHeaderNotOnCurlArgv(t *testing.T) {
	realCurl, err := exec.LookPath("curl")
	require.NoError(t, err)

	root, script := writePinCIJobIdentityScripts(t)
	st := &pinAPIState{
		jobJSON:      pinJobJSON("42", "100", "main"),
		projectJSON:  `{"id":42,"default_branch":"main","path_with_namespace":"group/project"}`,
		branchJSON:   `{"name":"main","protected":true}`,
		pipelineJSON: `{"id":100,"source":"api"}`,
	}
	srv := startPinAPI(t, st)
	t.Cleanup(srv.Close)

	argvLog := filepath.Join(t.TempDir(), "argv.log")
	shimDir := t.TempDir()
	shimScript := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> " + argvLog + "\nexec " + realCurl + " \"$@\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(shimDir, "curl"), []byte(shimScript), 0o755))

	out, err := sourcePinScript(t, root, script, append(pinTLSEnv(t, srv),
		"PATH="+shimDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	))
	require.NoError(t, err, "stdout/stderr: %s", out)

	logged, readErr := os.ReadFile(argvLog)
	require.NoError(t, readErr)
	loggedStr := string(logged)
	assert.NotContains(t, loggedStr, "job-token", "the raw JOB-TOKEN value must not appear in curl's argv")
	assert.Contains(t, loggedStr, "-H\n@", "the auth header must be passed to curl via an @file, not a literal argument")
}

func TestPinCIJobIdentity_PollerAdmitsScheduleOnly(t *testing.T) {
	root, script := writePinCIJobIdentityScripts(t)
	st := &pinAPIState{
		jobJSON:      pinJobJSON("42", "100", "main"),
		projectJSON:  `{"id":42,"default_branch":"main","path_with_namespace":"group/project"}`,
		branchJSON:   `{"name":"main","protected":true}`,
		pipelineJSON: `{"id":100,"source":"schedule"}`,
	}
	srv := startPinAPI(t, st)
	t.Cleanup(srv.Close)

	out, err := sourcePinScript(t, root, script, append(pinTLSEnv(t, srv),
		"FULLSEND_ADMIT_SOURCE=schedule",
	))
	require.NoError(t, err, "stdout/stderr: %s", out)
	assert.Contains(t, out, "PINNED_SOURCE=schedule")
}

func TestPinCIJobIdentity_PollerDeniesAPISource(t *testing.T) {
	root, script := writePinCIJobIdentityScripts(t)
	st := &pinAPIState{
		jobJSON:      pinJobJSON("42", "100", "main"),
		projectJSON:  `{"id":42,"default_branch":"main","path_with_namespace":"group/project"}`,
		branchJSON:   `{"name":"main","protected":true}`,
		pipelineJSON: `{"id":100,"source":"api"}`,
	}
	srv := startPinAPI(t, st)
	t.Cleanup(srv.Close)

	out, err := sourcePinScript(t, root, script, append(pinTLSEnv(t, srv),
		"FULLSEND_ADMIT_SOURCE=schedule",
	))
	require.Error(t, err, "stdout/stderr: %s", out)
	assert.Contains(t, out, "disjoint allowlist deny")
}

// Run*JobScript_* integration tests that exercise run-agent-job.sh /
// run-poll-job.sh end to end (not just the sourced
// pin-ci-job-identity.sh helper) live in gitlab_job_scripts_test.go,
// alongside the rest of that script-level coverage.
