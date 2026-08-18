//go:build !js

package mintcore

import (
	"net/http"
	"sync"
	"testing"
	"time"
)

var (
	defaultHTTPClient *http.Client
	httpOnce          sync.Once
	httpOverride      func(*http.Request) (*http.Response, error)
)

// mintHTTP executes an HTTP request using the package-level HTTP client.
// On native platforms this is a cached *http.Client with a 30-second
// timeout, matching the existing entrypoint behavior. Tests can
// override the behaviour via SetMintHTTPForTest.
func mintHTTP(req *http.Request) (*http.Response, error) {
	if f := httpOverride; f != nil {
		return f(req)
	}
	httpOnce.Do(func() {
		defaultHTTPClient = &http.Client{Timeout: 30 * time.Second}
	})
	return defaultHTTPClient.Do(req)
}

// SetMintHTTPForTest replaces the HTTP function for the duration of the
// test and restores the previous value when the test completes.
func SetMintHTTPForTest(t *testing.T, fake func(*http.Request) (*http.Response, error)) {
	t.Helper()
	prev := httpOverride
	httpOverride = fake
	t.Cleanup(func() { httpOverride = prev })
}
