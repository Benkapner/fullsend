package scaffold

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const defaultsVendoredPrefix = ".defaults/"

// CollectVendoredAssets gathers files for --vendor installs.
// Upstream mirror content lives under .defaults/ (same layout as runtime sparse checkout).
// Reusable workflows are always written under .github/workflows/ because GitHub
// Actions requires local reusable workflow references (./path) to live there.
// Other vendored assets use workflowPrefix (.fullsend/ for per-repo, "" for per-org).
func CollectVendoredAssets(root, workflowPrefix string) (InstallFiles, error) {
	var files InstallFiles

	if err := walkVendoredUpstreamFromRoot(root, func(path string, content []byte) error {
		if isVendoredReusableWorkflow(path) {
			files = append(files, InstallFile{
				Path:    path,
				Content: content,
				Mode:    "100644",
			})
		}
		if isVendoredDefaultsInfra(path) {
			files = append(files, InstallFile{
				Path:    defaultsVendoredPrefix + path,
				Content: content,
				Mode:    vendoredInfraFileMode(path),
			})
		}
		return nil
	}); err != nil {
		return nil, err
	}

	layeredRoot := filepath.Join(root, "internal", "scaffold", "fullsend-repo")
	if err := walkLayeredFromRoot(layeredRoot, func(path string, content []byte) error {
		files = append(files, InstallFile{
			Path:    defaultsVendoredPrefix + "internal/scaffold/fullsend-repo/" + path,
			Content: content,
			Mode:    FileMode(path),
		})
		return nil
	}); err != nil {
		return nil, err
	}

	return files, nil
}

// ManagedVendoredContentPaths returns embed-derived paths for the current vendor layout.
func ManagedVendoredContentPaths(workflowPrefix string) ([]string, error) {
	return enumerateVendoredPaths()
}

// LegacyFlatVendoredPaths lists pre-.defaults flat layout paths for legacy cleanup.
func LegacyFlatVendoredPaths(workflowPrefix string) ([]string, error) {
	return enumerateLegacyFlatVendoredPaths(workflowPrefix)
}

func moduleRootFromScaffold() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "cmd", "fullsend")); err == nil {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("not in module")
		}
		dir = parent
	}
}

func walkVendoredUpstreamFromRoot(root string, fn func(path string, content []byte) error) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !isVendoredReusableWorkflow(rel) && !isVendoredDefaultsInfra(rel) {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("reading %s: %w", rel, readErr)
		}
		return fn(rel, data)
	})
}

func walkLayeredFromRoot(layeredRoot string, fn func(path string, content []byte) error) error {
	info, err := os.Stat(layeredRoot)
	if err != nil {
		return fmt.Errorf("layered content root %s: %w", layeredRoot, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("layered content root %s is not a directory", layeredRoot)
	}
	return filepath.WalkDir(layeredRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		rel, err := filepath.Rel(layeredRoot, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !IsLayeredPath(rel) && rel != ".github/scripts/setup-agent-env.sh" {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("reading %s: %w", rel, readErr)
		}
		return fn(rel, data)
	})
}

func isVendoredReusableWorkflow(path string) bool {
	if !strings.HasPrefix(path, ".github/workflows/") {
		return false
	}
	base := path[strings.LastIndex(path, "/")+1:]
	return strings.HasPrefix(base, "reusable-") && strings.HasSuffix(base, ".yml")
}

// vendoredDefaultsScripts is the explicit allowlist of .github/scripts/
// files that ship to consumer repos. Everything here is executed in user
// repos: check-fix-eligibility.sh directly by the vendored reusable
// workflows, the other three by the root composite action (action.yml,
// invoked as ./.defaults/ with GITHUB_ACTION_PATH resolving into the
// vendored tree).
//
// .github/scripts/ also hosts repo-local CI tooling and *-test.sh files
// that must NOT ship to consumers. The directory prefix alone is
// deliberately not sufficient for vendoring — add new user-facing scripts
// here explicitly. Scripts whose path is a cross-repo contract
// (openshell-version.sh and install-openshell.sh are read from a fullsend
// checkout by the agents functional-tests gate, hack/gitlab-runner-vm,
// and scripts/renovate/update-openshell-sha.sh) must stay at their
// current path regardless of whether they are listed.
var vendoredDefaultsScripts = map[string]bool{
	".github/scripts/check-fix-eligibility.sh": true,
	".github/scripts/install-openshell.sh":     true,
	".github/scripts/install-podman.sh":        true,
	".github/scripts/openshell-version.sh":     true,
}

func isVendoredDefaultsInfra(path string) bool {
	if path == "action.yml" {
		return true
	}
	if strings.HasPrefix(path, ".github/actions/") {
		return true
	}
	return vendoredDefaultsScripts[path]
}

func vendoredInfraFileMode(path string) string {
	if strings.HasPrefix(path, ".github/scripts/") {
		return "100755"
	}
	return "100644"
}

// VendoredMarkerPath returns the path used to detect a vendored install.
func VendoredMarkerPath() string {
	return defaultsVendoredPrefix + "action.yml"
}
