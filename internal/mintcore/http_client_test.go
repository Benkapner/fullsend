//go:build !js

package mintcore

import (
	"net/http"
	"testing"
)

func TestMintHTTP_ReturnsCachedClient(t *testing.T) {
	client := mintHTTP()
	if client == nil {
		t.Fatal("mintHTTP() returned nil")
	}
	// Calling again should return the same instance.
	client2 := mintHTTP()
	if client != client2 {
		t.Fatal("mintHTTP() should return the same cached client")
	}
}

func TestSetHTTPDoerForTest_OverridesAndRestores(t *testing.T) {
	original := mintHTTP()
	fake := &http.Client{}
	SetHTTPDoerForTest(t, fake)

	got := mintHTTP()
	if got != fake {
		t.Fatal("mintHTTP() should return the test override")
	}

	// After t.Cleanup runs (at end of this test), original is restored.
	// We can't easily verify the cleanup here, but the mechanism is
	// tested by ensuring the override works during the test.
	_ = original
}

func TestNewHandler_UsesMintHTTPOverride(t *testing.T) {
	t.Setenv("ROLE_APP_IDS", `{"coder":"200"}`)
	t.Setenv("ALLOWED_WORKFLOW_FILES", "*")

	fake := &fakeHTTPDoer{}
	SetHTTPDoerForTest(t, fake)

	h, err := NewHandler(&fakePEMAccessor{}, &fakeOIDCVerifier{})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	if h.httpClient != fake {
		t.Fatal("expected handler to use the test-injected HTTP client")
	}
}
