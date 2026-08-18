//go:build !js

package mintcore

import (
	"net/http"
	"sync"
	"testing"
	"time"
)

var (
	defaultHTTPClient HTTPDoer
	httpOnce          sync.Once
	httpOverride      HTTPDoer
)

// mintHTTP returns the package-level HTTP client. On native platforms
// this is a cached *http.Client with a 30-second timeout, matching the
// existing entrypoint behavior. Tests can override it via
// SetHTTPDoerForTest.
func mintHTTP() HTTPDoer {
	if o := httpOverride; o != nil {
		return o
	}
	httpOnce.Do(func() {
		defaultHTTPClient = &http.Client{Timeout: 30 * time.Second}
	})
	return defaultHTTPClient
}

// SetHTTPDoerForTest replaces the HTTP client for the duration of the
// test and restores it when the test completes.
func SetHTTPDoerForTest(t *testing.T, fake HTTPDoer) {
	t.Helper()
	prev := httpOverride
	httpOverride = fake
	t.Cleanup(func() { httpOverride = prev })
}
