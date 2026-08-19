package mintcore

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sort"
	"strings"
)

// StatusValidator authenticates a /v1/status request using a non-OIDC
// credential (e.g. GitHub user token, Cloudflare Access JWT). Each
// validator is compiled as either the real check or a passthru stub
// via build tags.
//
//   - nil means the validator authenticated the request.
//   - errStatusAuthSkip means the validator does not handle this request
//     (stub or credential not present); the dispatcher moves on.
//   - Any other error means the validator rejected the request.
type StatusValidator func(ctx context.Context, r *http.Request) error

// errStatusAuthSkip is returned by stub validators and by real
// validators when the request does not carry credentials they
// recognise. The dispatcher ignores this error and tries the next
// validator.
var errStatusAuthSkip = errors.New("status auth: skip")

// statusAuthResult describes how a /v1/status request was
// authenticated, so handleStatus can choose the right payload shape.
type statusAuthResult struct {
	// oidcClaims is set when OIDC authentication succeeded.
	// When non-nil, the status response is scoped to the
	// authenticating workflow's org. When nil, a non-OIDC validator
	// authenticated the request and the status response reports all
	// configured allowed orgs.
	oidcClaims *Claims
}

// authenticateStatus runs the /v1/status auth pipeline:
//
//  1. OIDC (always first, always compiled in).
//  2. Optional status validators in a stable order (github, then
//     access when present). First nil wins.
//  3. If everything fails → 401.
func (h *Handler) authenticateStatus(ctx context.Context, r *http.Request) (*statusAuthResult, error) {
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return nil, errors.New("missing or invalid Authorization header")
	}
	oidcToken := strings.TrimPrefix(authHeader, "Bearer ")

	// --- OIDC (always tried first) ---
	claims, oidcErr := h.oidcVerifier.Verify(ctx, oidcToken)
	if oidcErr == nil {
		if err := AuthorizeToken(claims, h.allowedOrgs, h.perRepoWIFRepos); err != nil {
			log.Printf("token authorization failed for /v1/status: %v", err)
			// OIDC token is valid but not authorized — do NOT fall
			// through to optional validators; this is a policy denial.
			return nil, errors.New("authentication failed")
		}
		isPerRepo := IsPerRepoMode(claims.Repository, h.perRepoWIFRepos)
		isDualEnrolled := false
		if isPerRepo && !IsPublicMintRepos(h.perRepoWIFRepos) &&
			ValidateOrgAllowed(claims.RepositoryOwner, h.allowedOrgs) == nil {
			isDualEnrolled = true
			isPerRepo = false
		}
		wfErr := ValidateWorkflowRef(claims.JobWorkflowRef, claims.Repository, isPerRepo, h.workflowHostRepos, h.allowedWorkflowFiles)
		if wfErr != nil && isDualEnrolled {
			wfErr = ValidateWorkflowRef(claims.JobWorkflowRef, claims.Repository, true, h.workflowHostRepos, h.allowedWorkflowFiles)
		}
		if wfErr != nil {
			log.Printf("workflow ref validation failed for /v1/status: %v", wfErr)
			return nil, errors.New("authentication failed")
		}
		return &statusAuthResult{oidcClaims: claims}, nil
	}

	// OIDC failed — try optional validators.
	log.Printf("OIDC verification failed for /v1/status: %v", oidcErr)

	validators := statusValidators()
	for _, v := range validators {
		err := v(ctx, r)
		if err == nil {
			// Authenticated by an optional validator.
			return &statusAuthResult{}, nil
		}
		if errors.Is(err, errStatusAuthSkip) {
			continue
		}
		// Real rejection — log and continue to next validator.
		log.Printf("status validator rejected request: %v", err)
	}

	return nil, errors.New("authentication failed")
}

// handleStatusWithAuth serves the /v1/status response using the
// authentication result to determine payload shape.
func (h *Handler) handleStatusWithAuth(w http.ResponseWriter, auth *statusAuthResult) {
	roles := append([]string(nil), h.allowedRoles...)

	var hostRepos []string
	for repo := range h.workflowHostRepos {
		hostRepos = append(hostRepos, repo)
	}
	sort.Strings(hostRepos)

	resp := statusResponse{
		Roles:             roles,
		WorkflowHostRepos: hostRepos,
		Version:           Version,
		Commit:            Commit,
	}

	if auth.oidcClaims != nil {
		// OIDC success: scope to the authenticating workflow's org.
		resp.Org = strings.ToLower(auth.oidcClaims.RepositoryOwner)
	} else {
		// Non-OIDC validator: report all configured allowed orgs.
		resp.AllowedOrgs = h.allowedOrgs
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("encoding status response: %v", err)
	}
}
