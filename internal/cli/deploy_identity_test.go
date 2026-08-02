package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestResolveMintDeployCommit_PreservesNonDev(t *testing.T) {
	t.Parallel()
	got, err := resolveMintDeployCommit("abc123def", "/nonexistent")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != "abc123def" {
		t.Fatalf("got %q, want preserved commit", got)
	}
}

func TestResolveMintDeployCommit_MissingSourceDir(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "no-such-dir")
	got, err := resolveMintDeployCommit("dev", dir)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != "dev" {
		t.Fatalf("got %q, want dev when source missing", got)
	}
	got, err = resolveMintDeployCommit("", "")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != "" {
		t.Fatalf("got %q, want empty when sourceDir empty", got)
	}
}

func TestResolveMintDeployCommit_NotAGitRepo(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	got, err := resolveMintDeployCommit("dev", dir)
	if err == nil {
		t.Fatal("expected error when sourceDir is not a git work tree")
	}
	if got != "dev" {
		t.Fatalf("got %q, want dev when not a git work tree", got)
	}
}

func TestResolveMintDeployCommit_FromCheckout(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=commit.gpgsign",
			"GIT_CONFIG_VALUE_0=false",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init")
	runGit("config", "user.email", "test@example.com")
	runGit("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "README")
	runGit("commit", "-m", "init")

	wantCmd := exec.Command("git", "-C", dir, "rev-parse", "HEAD")
	wantOut, err := wantCmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	want := strings.TrimSpace(string(wantOut))

	got, err := resolveMintDeployCommit("dev", dir)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(got) {
		t.Fatalf("expected full SHA, got %q", got)
	}

	// Empty commit also resolves.
	got, err = resolveMintDeployCommit("", dir)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != want {
		t.Fatalf("empty commit: got %q, want %q", got, want)
	}
}
