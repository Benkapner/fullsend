package mintcore

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	defaults := map[string]string{
		"ALLOWED_ORGS":           "test-org",
		"GCP_PROJECT_NUMBER":     "123456",
		"ROLE_APP_IDS":           `{"triage":"100","coder":"200","review":"300","fullsend":"500","retro":"600","prioritize":"700"}`,
		"ALLOWED_WORKFLOW_FILES": "*",
		// PER_REPO_WIF_REPOS stays unset so all callers get per-org behavior
		// (org-mode shapes) unless a test opts into per-repo mode.
	}
	for k, v := range defaults {
		if os.Getenv(k) == "" {
			os.Setenv(k, v)
		}
	}
	os.Exit(m.Run())
}
