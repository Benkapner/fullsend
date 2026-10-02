package agentnew

import (
	"bytes"
	_ "embed"
	"fmt"

	"github.com/fullsend-ai/fullsend/internal/harness"
	"github.com/fullsend-ai/fullsend/internal/scaffold"
)

// BasePolicy returns the base sandbox policy a generated agent's harness
// names: filesystem, landlock and process rules with no network section, so
// egress comes only from the harness's providers (ADR 0065). The behaviour
// suite commits it for scenario harnesses, because OpenShell 0.1 refuses a
// sandbox with no policy and a policy file that declares no fields.
func BasePolicy() []byte {
	return bytes.Clone(basePolicy)
}

//go:embed templates/policies/base.yaml
var basePolicy []byte

// sharedAssets returns the scaffold files a generated agent depends on but
// does not own: the sandbox policy, the role's providers and profiles, and
// the schema validator when the validation loop is enabled.
//
// These are written only when absent and are never overwritten, because they
// are shared by every agent in the directory. They are needed at all because
// a per-repo install vendors none of them: CollectPerRepoInstallFiles returns
// only the shim workflow and one thin caller, and CI layers providers/ but
// never policies/ or profiles/, so the copies written here are the ones every
// run uses. The policy template here is the only in-repo copy (#7268).
//
// Providers and profiles come from the scaffold embed, so a generated
// providers/ tree matches what CI layers in.
func sharedAssets(role Role, validationLoop bool) ([]File, error) {
	files := []File{}

	files = append(files, File{Path: "policies/base.yaml", Data: BasePolicy(), Mode: 0o644, Shared: true})

	// Path-referenced providers and profiles are copied from the embedded
	// scaffold. A bare name is skipped: the OpenAI provider is binary-only
	// (appendEmbeddedProviderDefs fills it in at run time) and passing it to
	// FullsendRepoFile would look for a file the scaffold does not ship.
	for _, path := range append(append([]string{}, role.Providers...), role.Profiles...) {
		if !harness.IsProviderPath(path) {
			continue
		}
		data, err := scaffold.FullsendRepoFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading %s from the embedded scaffold: %w", path, err)
		}
		files = append(files, File{Path: path, Data: data, Mode: 0o644, Shared: true})
	}

	if validationLoop {
		script, err := templates.ReadFile("templates/scripts/validate-output-schema.sh")
		if err != nil {
			return nil, fmt.Errorf("reading validation script: %w", err)
		}
		files = append(files, File{
			Path: "scripts/validate-output-schema.sh", Data: script, Mode: 0o755, Shared: true,
		})
	}
	return files, nil
}
