package repos

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/fullsend-ai/fullsend/internal/config"
	"github.com/fullsend-ai/fullsend/internal/forge"
	"github.com/fullsend-ai/fullsend/internal/scaffold"
)

// GCPServiceAccountEmailPattern matches a full Google service account
// email: a 6-30 character account ID and a project-scoped
// .iam.gserviceaccount.com domain. The character set also keeps the value
// safe inside the credential JSON the GitLab agent job writes.
var GCPServiceAccountEmailPattern = regexp.MustCompile(
	`^[a-z][a-z0-9-]{4,28}[a-z0-9]@[a-z0-9][a-z0-9.-]*[a-z0-9]\.iam\.gserviceaccount\.com$`,
)

// validateGCPServiceAccountEmail checks inference.service_account.
func validateGCPServiceAccountEmail(email string) error {
	if !GCPServiceAccountEmailPattern.MatchString(email) {
		return fmt.Errorf("inference.service_account %q is not a full service account email (expected <name>@<project>.iam.gserviceaccount.com)", email)
	}
	return nil
}

// validateWIFAudience checks inference.wif_audience: a non-empty single
// line of printable ASCII with no surrounding spaces. "$" is rejected
// because GitLab expands CI/CD variables in id_tokens audiences, so the
// token would not carry the configured value.
func validateWIFAudience(audience string) error {
	if audience == "" || strings.TrimSpace(audience) != audience {
		return fmt.Errorf("inference.wif_audience %q must be a non-empty value without surrounding spaces", audience)
	}
	for _, r := range audience {
		if r < 0x20 || r > 0x7e {
			return fmt.Errorf("inference.wif_audience %q must be a single line of printable ASCII characters", audience)
		}
		if r == '$' {
			return fmt.Errorf("inference.wif_audience %q must not contain \"$\": GitLab would expand it as a CI/CD variable", audience)
		}
	}
	return nil
}

// validateExistingWIFOptionFormats checks the format of the GitLab
// existing-provider options; empty values are not declared and pass.
func validateExistingWIFOptionFormats(audience, serviceAccount string) error {
	if audience != "" {
		if err := validateWIFAudience(audience); err != nil {
			return err
		}
	}
	if serviceAccount != "" {
		if err := validateGCPServiceAccountEmail(serviceAccount); err != nil {
			return err
		}
	}
	return nil
}

// applyExistingWIFOptions checks the format of the GitLab existing-provider
// options (inference.wif_audience, inference.service_account) wherever they
// are declared, then clears them where they do not apply. They only change
// the GitLab agent job's Vertex credentials, so on GitHub repositories (which
// can inherit them from defaults.config) and on GitLab repositories whose
// inference.auth is not vertex-wif they are inert: no variable is written and
// no audience is rendered.
func (c *ResolvedConfig) applyExistingWIFOptions() error {
	if err := validateExistingWIFOptionFormats(c.DeclaredWIFAudience, c.DeclaredServiceAccount); err != nil {
		return fmt.Errorf("%s/%s: effective configuration: %w", c.Owner, c.Repo, err)
	}
	if c.Forge != ForgeGitLab || c.InferenceAuth != InferenceAuthVertexWIF {
		c.DeclaredWIFAudience = ""
		c.DeclaredServiceAccount = ""
	}
	return nil
}

// rejectGitLabOnlyInferenceKeys refuses the GitLab existing-provider options
// in a GitHub layer (github.config or a GitHub repository's config), where
// they could never apply.
func rejectGitLabOnlyInferenceKeys(field string, o config.ManagedConfig) error {
	if !o.IsSet() {
		return nil
	}
	r := o.Writer()
	for _, key := range []struct{ name, value string }{
		{"inference.wif_audience", r.ConfigInferenceWIFAudience()},
		{"inference.service_account", r.ConfigInferenceServiceAccount()},
	} {
		if key.value != "" {
			return fmt.Errorf("%s.%s applies only to GitLab repositories; set it under gitlab.config or on a gitlab.repos entry", field, key.name)
		}
	}
	return nil
}

// renderGitLabAgentAudience sets the configured FULLSEND_ID_TOKEN audience
// in the GitLab agent template among files. An empty audience leaves the
// files unchanged, so the shipped aud "fullsend" stays byte for byte.
func renderGitLabAgentAudience(files []forge.TreeFile, audience string) ([]forge.TreeFile, error) {
	if audience == "" {
		return files, nil
	}
	out := make([]forge.TreeFile, len(files))
	copy(out, files)
	for i, f := range out {
		if f.Path != fullsendAgentTemplatePath {
			continue
		}
		rendered, err := scaffold.RenderGitLabAgentAudience(f.Content, audience)
		if err != nil {
			return nil, err
		}
		out[i].Content = rendered
	}
	return out, nil
}
