package cli

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
)

// resolveMintDeployCommit returns the commit to stamp into a mint deploy.
// When commit is already set to something other than "dev"/empty, it is
// returned unchanged. When deploying from on-disk source (sourceDir exists)
// and commit is unset, it tries git -C sourceDir rev-parse HEAD. Embedded
// deploys (missing sourceDir) never invoke git.
func resolveMintDeployCommit(commit, sourceDir string) string {
	if commit != "" && commit != "dev" {
		return commit
	}
	if sourceDir == "" {
		return commit
	}
	info, err := os.Stat(sourceDir)
	if err != nil || !info.IsDir() {
		return commit
	}
	sha, err := gitRevParse(sourceDir, "HEAD")
	if err != nil || sha == "" {
		return commit
	}
	return sha
}

func gitRevParse(dir string, args ...string) (string, error) {
	cmdArgs := append([]string{"-C", dir, "rev-parse"}, args...)
	cmd := exec.Command("git", cmdArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}
