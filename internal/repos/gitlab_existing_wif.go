package repos

import (
	"fmt"
	"regexp"
	"strings"

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

// validateExistingWIFOptions rejects the GitLab existing-provider options
// (inference.wif_audience, inference.service_account) where they would be
// ignored: they only change the GitLab agent job's Vertex credentials, so
// they require a GitLab repository whose inference.auth is vertex-wif.
func (c ResolvedConfig) validateExistingWIFOptions() error {
	if c.DeclaredWIFAudience == "" && c.DeclaredServiceAccount == "" {
		return nil
	}
	if c.Forge != ForgeGitLab {
		return fmt.Errorf("%s/%s: inference.wif_audience and inference.service_account apply only to GitLab repositories", c.Owner, c.Repo)
	}
	if c.InferenceAuth != InferenceAuthVertexWIF {
		return fmt.Errorf("%s/%s: inference.wif_audience and inference.service_account apply only to inference.auth %s (this repository uses %q)", c.Owner, c.Repo, InferenceAuthVertexWIF, c.InferenceAuth)
	}
	if err := validateExistingWIFOptionFormats(c.DeclaredWIFAudience, c.DeclaredServiceAccount); err != nil {
		return fmt.Errorf("%s/%s: effective configuration: %w", c.Owner, c.Repo, err)
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
