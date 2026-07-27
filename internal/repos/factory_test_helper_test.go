package repos

import "github.com/fullsend-ai/fullsend/internal/forge"

// testClientFactory wraps a single forge client as a ForgeClientFactory
// for use in tests. It returns the same client for any forge name, matching
// the single-client test pattern that existed before ForgeClientFactory.
type testClientFactory struct {
	client forge.Client
}

func newTestClientFactory(fc forge.Client) ForgeClientFactory {
	return &testClientFactory{client: fc}
}

func (f *testClientFactory) ConfigFor(forgeName string) (ForgeConfig, error) {
	if forgeName == "" {
		forgeName = ForgeGitHub
	}
	cfg := ForgeConfigFor(forgeName)
	cfg.Client = f.client
	return cfg, nil
}
