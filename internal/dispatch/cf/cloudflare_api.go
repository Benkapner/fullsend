package cf

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
)

// mintWAFRulesetName is the name used for the managed WAF ruleset
// deployed on the custom-domain zone. The provisioner uses this name
// to identify its own ruleset during updates and teardown.
const mintWAFRulesetName = "fullsend-mint-waf"

// mintWAFRulesetDescription is the human-readable description for the
// managed WAF ruleset.
const mintWAFRulesetDescription = "Managed WAF rules for fullsend mint custom domain"

// CloudflareAPIClient abstracts direct Cloudflare API calls for
// operations not covered by wrangler: custom domain attachment and
// zone-level WAF rulesets. Implementations must be safe for concurrent
// use from multiple goroutines.
type CloudflareAPIClient interface {
	// AttachCustomDomain registers a custom domain for a Worker via
	// the Cloudflare Workers Custom Domains API. If the domain is
	// already attached, this is a no-op.
	AttachCustomDomain(ctx context.Context, accountID, workerName, zoneID, hostname string) error

	// DeployWAFRules creates or updates the hardcoded mint WAF
	// ruleset on the given zone. The rules are identified by the
	// mintWAFRulesetName constant; existing rules under that name
	// are replaced.
	DeployWAFRules(ctx context.Context, zoneID string, rules []WAFRule) error

	// RemoveCustomDomain removes a Worker's custom domain binding
	// by hostname.
	RemoveCustomDomain(ctx context.Context, accountID, hostname string) error

	// RemoveWAFRuleset removes the managed WAF ruleset from the zone.
	RemoveWAFRuleset(ctx context.Context, zoneID string) error
}

// WAFRule defines a single WAF rule in the Cloudflare Rulesets API
// format.
type WAFRule struct {
	// Description is a human-readable label for the rule.
	Description string
	// Expression is a Cloudflare Ruleset filter expression.
	Expression string
	// Action is the action to take when the expression matches
	// (e.g. "block").
	Action string
}

// MintWAFRules returns the hardcoded WAF rules for the mint custom
// domain. These rules are defined by the CLI — not user-configurable
// — encoding the knowledge of what edge protection the mint needs.
//
// Rules:
//  1. Block non-POST methods on /v1/token
//  2. Block oversized request bodies on /v1/token (>64 KB)
//  3. Block requests to /v1/token with non-JSON content-type
func MintWAFRules() []WAFRule {
	return []WAFRule{
		{
			Description: "Block non-POST methods on /v1/token",
			Expression:  `(http.request.uri.path eq "/v1/token" and http.request.method ne "POST")`,
			Action:      "block",
		},
		{
			Description: "Block oversized bodies on /v1/token (>64KB)",
			Expression:  `(http.request.uri.path eq "/v1/token" and http.request.body.size gt 65536)`,
			Action:      "block",
		},
		{
			Description: "Block malformed content-type on POST /v1/token",
			Expression:  `(http.request.uri.path eq "/v1/token" and http.request.method eq "POST" and not any(http.request.headers["content-type"][*], contains(it, "application/json")))`,
			Action:      "block",
		},
	}
}

// --- LiveCloudflareAPIClient ---

// LiveCloudflareAPIClient implements CloudflareAPIClient using the
// Cloudflare REST API. Authentication is via CLOUDFLARE_API_TOKEN
// environment variable (Bearer token).
type LiveCloudflareAPIClient struct {
	httpClient *http.Client
	// baseURL overrides the Cloudflare API base URL. When empty
	// (default), production URL https://api.cloudflare.com/client/v4
	// is used. Set in tests to point at httptest servers.
	baseURL string
}

// NewLiveCloudflareAPIClient creates a client that calls the
// Cloudflare REST API.
func NewLiveCloudflareAPIClient() *LiveCloudflareAPIClient {
	return &LiveCloudflareAPIClient{
		httpClient: http.DefaultClient,
	}
}

// cfBaseURL returns the base URL for the Cloudflare API. When
// baseURL is set (e.g. in tests), it is used instead of the
// production URL.
func (c *LiveCloudflareAPIClient) cfBaseURL() string {
	if c.baseURL != "" {
		return c.baseURL
	}
	return "https://api.cloudflare.com/client/v4"
}

// cfAPIToken reads the Cloudflare API token from the environment.
func cfAPIToken() (string, error) {
	token := os.Getenv("CLOUDFLARE_API_TOKEN")
	if token == "" {
		return "", fmt.Errorf("CLOUDFLARE_API_TOKEN is required for custom domain and WAF operations")
	}
	return token, nil
}

// cfAPIRequest is a helper that makes an authenticated Cloudflare API
// request and returns the response body. It handles setting the
// Authorization header and Content-Type.
func (c *LiveCloudflareAPIClient) cfAPIRequest(ctx context.Context, method, url string, body any) ([]byte, error) {
	token, err := cfAPIToken()
	if err != nil {
		return nil, err
	}

	var reqBody io.Reader
	if body != nil {
		data, marshalErr := json.Marshal(body)
		if marshalErr != nil {
			return nil, fmt.Errorf("marshaling request body: %w", marshalErr)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling Cloudflare API: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Cloudflare API returned %d: %s", resp.StatusCode, string(respBody))
	}
	return respBody, nil
}

// AttachCustomDomain registers a custom domain for a Worker via
// PUT /accounts/{account_id}/workers/domains.
func (c *LiveCloudflareAPIClient) AttachCustomDomain(ctx context.Context, accountID, workerName, zoneID, hostname string) error {
	url := fmt.Sprintf("%s/accounts/%s/workers/domains", c.cfBaseURL(), accountID)
	body := map[string]string{
		"hostname":    hostname,
		"zone_id":     zoneID,
		"service":     workerName,
		"environment": "production",
	}

	_, err := c.cfAPIRequest(ctx, http.MethodPut, url, body)
	if err != nil {
		return fmt.Errorf("attaching custom domain %s: %w", hostname, err)
	}
	return nil
}

// DeployWAFRules creates or updates the mint WAF ruleset on the zone.
// It first searches for an existing ruleset by name; if found, it
// updates the rules via PUT. Otherwise, it creates a new ruleset via
// POST.
func (c *LiveCloudflareAPIClient) DeployWAFRules(ctx context.Context, zoneID string, rules []WAFRule) error {
	// Search for existing ruleset.
	rulesetID, err := c.findMintRulesetID(ctx, zoneID)
	if err != nil {
		return fmt.Errorf("searching for existing WAF ruleset: %w", err)
	}

	apiRules := make([]map[string]string, len(rules))
	for i, r := range rules {
		apiRules[i] = map[string]string{
			"description": r.Description,
			"expression":  r.Expression,
			"action":      r.Action,
		}
	}

	if rulesetID != "" {
		// Update existing ruleset.
		url := fmt.Sprintf("%s/zones/%s/rulesets/%s", c.cfBaseURL(), zoneID, rulesetID)
		body := map[string]any{
			"name":        mintWAFRulesetName,
			"description": mintWAFRulesetDescription,
			"rules":       apiRules,
		}
		if _, err := c.cfAPIRequest(ctx, http.MethodPut, url, body); err != nil {
			return fmt.Errorf("updating WAF ruleset: %w", err)
		}
		return nil
	}

	// Create new ruleset.
	url := fmt.Sprintf("%s/zones/%s/rulesets", c.cfBaseURL(), zoneID)
	body := map[string]any{
		"name":        mintWAFRulesetName,
		"description": mintWAFRulesetDescription,
		"kind":        "zone",
		"phase":       "http_request_firewall_custom",
		"rules":       apiRules,
	}
	if _, err := c.cfAPIRequest(ctx, http.MethodPost, url, body); err != nil {
		return fmt.Errorf("creating WAF ruleset: %w", err)
	}
	return nil
}

// RemoveCustomDomain removes a Worker's custom domain binding by
// first looking up the domain ID, then deleting it.
func (c *LiveCloudflareAPIClient) RemoveCustomDomain(ctx context.Context, accountID, hostname string) error {
	domainID, err := c.findCustomDomainID(ctx, accountID, hostname)
	if err != nil {
		return fmt.Errorf("looking up custom domain %s: %w", hostname, err)
	}
	if domainID == "" {
		// Domain not found — nothing to remove.
		return nil
	}

	url := fmt.Sprintf("%s/accounts/%s/workers/domains/%s", c.cfBaseURL(), accountID, domainID)
	if _, err := c.cfAPIRequest(ctx, http.MethodDelete, url, nil); err != nil {
		return fmt.Errorf("removing custom domain %s: %w", hostname, err)
	}
	return nil
}

// RemoveWAFRuleset removes the managed mint WAF ruleset from the zone.
func (c *LiveCloudflareAPIClient) RemoveWAFRuleset(ctx context.Context, zoneID string) error {
	rulesetID, err := c.findMintRulesetID(ctx, zoneID)
	if err != nil {
		return fmt.Errorf("searching for WAF ruleset: %w", err)
	}
	if rulesetID == "" {
		// Ruleset not found — nothing to remove.
		return nil
	}

	url := fmt.Sprintf("%s/zones/%s/rulesets/%s", c.cfBaseURL(), zoneID, rulesetID)
	if _, err := c.cfAPIRequest(ctx, http.MethodDelete, url, nil); err != nil {
		return fmt.Errorf("removing WAF ruleset: %w", err)
	}
	return nil
}

// findMintRulesetID searches for the mint WAF ruleset on a zone by
// name. Returns the ruleset ID if found, or empty string if not.
func (c *LiveCloudflareAPIClient) findMintRulesetID(ctx context.Context, zoneID string) (string, error) {
	url := fmt.Sprintf("%s/zones/%s/rulesets", c.cfBaseURL(), zoneID)
	respBody, err := c.cfAPIRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}

	var result struct {
		Result []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parsing rulesets response: %w", err)
	}

	for _, rs := range result.Result {
		if rs.Name == mintWAFRulesetName {
			return rs.ID, nil
		}
	}
	return "", nil
}

// findCustomDomainID looks up a Worker custom domain by hostname.
// Returns the domain ID if found, or empty string if not.
func (c *LiveCloudflareAPIClient) findCustomDomainID(ctx context.Context, accountID, hostname string) (string, error) {
	params := url.Values{"hostname": {hostname}}
	reqURL := fmt.Sprintf("%s/accounts/%s/workers/domains?%s", c.cfBaseURL(), accountID, params.Encode())
	respBody, err := c.cfAPIRequest(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return "", err
	}

	var result struct {
		Result []struct {
			ID       string `json:"id"`
			Hostname string `json:"hostname"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parsing domains response: %w", err)
	}

	for _, d := range result.Result {
		if d.Hostname == hostname {
			return d.ID, nil
		}
	}
	return "", nil
}
