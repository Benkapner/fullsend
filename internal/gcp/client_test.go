package gcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

func TestExtractErrorMessage(t *testing.T) {
	t.Run("valid error response", func(t *testing.T) {
		body := []byte(`{"error":{"message":"Permission denied on resource"}}`)
		msg := ExtractErrorMessage(body)
		assert.Equal(t, "Permission denied on resource", msg)
	})

	t.Run("empty message", func(t *testing.T) {
		body := []byte(`{"error":{"message":""}}`)
		msg := ExtractErrorMessage(body)
		assert.Equal(t, "(error details unavailable)", msg)
	})

	t.Run("invalid json", func(t *testing.T) {
		body := []byte(`not json`)
		msg := ExtractErrorMessage(body)
		assert.Equal(t, "(error details unavailable)", msg)
	})

	t.Run("missing error field", func(t *testing.T) {
		body := []byte(`{"status":"error"}`)
		msg := ExtractErrorMessage(body)
		assert.Equal(t, "(error details unavailable)", msg)
	})
}

func TestNewClient(t *testing.T) {
	c := NewClient()
	assert.NotNil(t, c)
	assert.NotNil(t, c.httpClient)
}

func TestDoRequest(t *testing.T) {
	t.Run("GET request with auth", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "GET", r.Method)
			assert.Contains(t, r.Header.Get("Authorization"), "Bearer ")
			assert.Empty(t, r.Header.Get("Content-Type"))
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		}))
		defer srv.Close()

		c := NewClient()
		// Override the token function for testing.
		c.tokenFunc = func(_ context.Context) (string, error) {
			return "test-token", nil
		}

		resp, err := c.DoRequest(context.Background(), http.MethodGet, srv.URL+"/test", "")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("POST request with body", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "POST", r.Method)
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
			assert.Contains(t, r.Header.Get("Authorization"), "Bearer ")

			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			assert.Equal(t, "value", body["key"])

			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		c := NewClient()
		c.tokenFunc = func(_ context.Context) (string, error) {
			return "test-token", nil
		}

		resp, err := c.DoRequest(context.Background(), http.MethodPost, srv.URL+"/test", `{"key":"value"}`)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})
}

func TestAccessToken_WithTokenFunc(t *testing.T) {
	c := NewClient()
	c.tokenFunc = func(_ context.Context) (string, error) {
		return "custom-token", nil
	}

	token, err := c.AccessToken(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "custom-token", token)
}

func TestDoRequest_QuotaProjectHeader(t *testing.T) {
	t.Run("sets x-goog-user-project when QuotaProject is non-empty", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "target-project", r.Header.Get("x-goog-user-project"))
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		c := NewClient()
		c.tokenFunc = func(_ context.Context) (string, error) { return "test-token", nil }
		c.QuotaProject = "target-project"

		resp, err := c.DoRequest(context.Background(), http.MethodGet, srv.URL+"/test", "")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("omits x-goog-user-project when QuotaProject is empty", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Empty(t, r.Header.Get("x-goog-user-project"))
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		c := NewClient()
		c.tokenFunc = func(_ context.Context) (string, error) { return "test-token", nil }

		resp, err := c.DoRequest(context.Background(), http.MethodGet, srv.URL+"/test", "")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})
}

func TestAccessToken_ErrorPropagation(t *testing.T) {
	c := NewClient()
	c.tokenFunc = func(_ context.Context) (string, error) {
		return "", fmt.Errorf("finding GCP credentials: no credentials found")
	}

	_, err := c.AccessToken(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "finding GCP credentials")
}

func TestAdcToken_CachesTokenSource(t *testing.T) {
	t.Parallel()

	// Verify that credential discovery runs exactly once even when
	// multiple goroutines call AccessToken concurrently.
	var discoveryCount atomic.Int32
	var tokenCount atomic.Int32

	c := NewClient()
	c.tokenFunc = c.adcToken
	// Pre-populate the cached TokenSource via sync.Once to simulate
	// a successful FindDefaultCredentials without hitting real GCP.
	c.adcOnce.Do(func() {
		discoveryCount.Add(1)
		c.adcTS = oauth2.StaticTokenSource(&oauth2.Token{
			AccessToken: "cached-token",
			Expiry:      time.Now().Add(time.Hour),
		})
	})

	// Wrap the cached token source to count Token() calls.
	inner := c.adcTS
	c.adcTS = tokenSourceFunc(func() (*oauth2.Token, error) {
		tokenCount.Add(1)
		return inner.Token()
	})

	const goroutines = 12
	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tok, err := c.AccessToken(context.Background())
			require.NoError(t, err)
			assert.Equal(t, "cached-token", tok)
		}()
	}
	wg.Wait()

	assert.Equal(t, int32(1), discoveryCount.Load(),
		"credential discovery should happen exactly once")
	assert.Equal(t, int32(goroutines), tokenCount.Load(),
		"Token() should be called on every AccessToken invocation")
}

func TestAdcToken_DiscoveryErrorSurfaces(t *testing.T) {
	c := NewClient()
	// Force a discovery error through the lazy init path.
	c.adcOnce.Do(func() {
		c.adcErr = fmt.Errorf("finding GCP credentials: %w",
			fmt.Errorf("no credentials found"))
	})
	c.tokenFunc = c.adcToken

	_, err := c.AccessToken(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "finding GCP credentials")

	// Second call should return the same cached error.
	_, err = c.AccessToken(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "finding GCP credentials")
}

func TestAdcToken_TokenFetchError(t *testing.T) {
	c := NewClient()
	c.tokenFunc = c.adcToken
	// Pre-populate a TokenSource that always returns an error on Token().
	c.adcOnce.Do(func() {
		c.adcTS = tokenSourceFunc(func() (*oauth2.Token, error) {
			return nil, fmt.Errorf("token refresh failed")
		})
	})

	_, err := c.AccessToken(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "obtaining GCP access token")
}

func TestAdcToken_EmptyAccessToken(t *testing.T) {
	c := NewClient()
	c.tokenFunc = c.adcToken
	// Pre-populate a TokenSource that returns an empty access token.
	c.adcOnce.Do(func() {
		c.adcTS = oauth2.StaticTokenSource(&oauth2.Token{
			AccessToken: "",
			Expiry:      time.Now().Add(time.Hour),
		})
	})

	_, err := c.AccessToken(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GCP credentials returned empty access token")
}

func TestNewClientWithHTTP(t *testing.T) {
	httpClient := &http.Client{Timeout: 5 * time.Second}
	c := NewClientWithHTTP(httpClient)
	assert.NotNil(t, c)

	token, err := c.AccessToken(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "test-token", token)
}

func TestDoRequest_TokenError(t *testing.T) {
	c := NewClient()
	c.tokenFunc = func(_ context.Context) (string, error) {
		return "", fmt.Errorf("auth failure")
	}

	_, err := c.DoRequest(context.Background(), http.MethodGet, "http://example.com", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "auth failure")
}

// tokenSourceFunc adapts a plain function to the oauth2.TokenSource
// interface, allowing Token() call counting in tests.
type tokenSourceFunc func() (*oauth2.Token, error)

func (f tokenSourceFunc) Token() (*oauth2.Token, error) { return f() }
