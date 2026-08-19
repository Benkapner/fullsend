package mintcore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
)

// errStatusAuthSkip is returned by the stub validateStatusGitHub and
// by the real validator when the request does not carry credentials it
// recognises. authenticateStatus treats this as "not applicable" and
// falls through to 401.
var errStatusAuthSkip = errors.New("status auth: skip")

// errOIDCNotAuthenticated is returned by verifyOIDCRequest when the
// request cannot be authenticated via OIDC — either the Bearer header
// is missing or token verification failed. Distinct from "valid OIDC
// token that fails authorization", which is a hard policy denial.
var errOIDCNotAuthenticated = errors.New("OIDC: not authenticated")

// statusAuthResult describes how a /v1/status request was
// authenticated, so handleStatusWithAuth can choose the right payload shape.
type statusAuthResult struct {
	// oidcClaims is set when OIDC authentication succeeded.
	// When non-nil, the status response is scoped to the
	// authenticating workflow's org. When nil, a non-OIDC validator
	// authenticated the request and the status response reports all
	// configured allowed orgs.
	oidcClaims *Claims
}

// verifyOIDCRequest extracts the Bearer token, verifies it via OIDC,
// and runs the full authorization pipeline (AuthorizeToken,
// dual-enrollment, ValidateWorkflowRef). Used by both the /v1/token
// path and the /v1/status auth pipeline.
//
// Returns (claims, isPerRepo, nil) on success. isPerRepo reflects the
// final per-repo mode after dual-enrollment promotion.
//
// Returns errOIDCNotAuthenticated (via errors.Is) when the Bearer
// header is missing or OIDC verification fails — callers that support
// fallback auth can check this. Any other error means the token was
// valid but denied by policy (hard 401, no fallback).
func (h *Handler) verifyOIDCRequest(ctx context.Context, r *http.Request) (*Claims, bool, error) {
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return nil, false, errOIDCNotAuthenticated
	}
	oidcToken := strings.TrimPrefix(authHeader, "Bearer ")

	claims, err := h.oidcVerifier.Verify(ctx, oidcToken)
	if err != nil {
		return nil, false, fmt.Errorf("%w: %v", errOIDCNotAuthenticated, err)
	}
	if claims == nil {
		return nil, false, errOIDCNotAuthenticated
	}

	if err := AuthorizeToken(claims, h.allowedOrgs, h.perRepoWIFRepos); err != nil {
		return nil, false, fmt.Errorf("token authorization failed: %w", err)
	}

	isPerRepo := IsPerRepoMode(claims.Repository, h.perRepoWIFRepos)
	isDualEnrolled := false
	if isPerRepo && !IsPublicMintRepos(h.perRepoWIFRepos) &&
		ValidateOrgAllowed(claims.RepositoryOwner, h.allowedOrgs) == nil {
		isDualEnrolled = true
		log.Printf("dual-enrollment: %s matches both per-repo and per-org — accepting workflow refs from either mode", claims.Repository)
		isPerRepo = false
	}
	wfErr := ValidateWorkflowRef(claims.JobWorkflowRef, claims.Repository, isPerRepo, h.workflowHostRepos, h.allowedWorkflowFiles)
	if wfErr != nil && isDualEnrolled {
		wfErr = ValidateWorkflowRef(claims.JobWorkflowRef, claims.Repository, true, h.workflowHostRepos, h.allowedWorkflowFiles)
	}
	if wfErr != nil {
		return nil, false, fmt.Errorf("workflow ref validation failed: %w", wfErr)
	}
	return claims, isPerRepo, nil
}

// authenticateStatus runs the /v1/status auth pipeline:
//
//  1. OIDC (always first, always compiled in).
//  2. validateStatusGitHub — compile-time selected via build tags:
//     real validator (github tag) or stub returning errStatusAuthSkip.
//  3. If everything fails → error.
//
// Non-skip errors from validateStatusGitHub produce an immediate 401
// with no fall-through — the validator positively rejected the request.
func (h *Handler) authenticateStatus(ctx context.Context, r *http.Request) (*statusAuthResult, error) {
	// --- OIDC (always tried first) ---
	claims, _, oidcErr := h.verifyOIDCRequest(ctx, r)
	if oidcErr == nil {
		return &statusAuthResult{oidcClaims: claims}, nil
	}
	if !errors.Is(oidcErr, errOIDCNotAuthenticated) {
		// OIDC token is valid but not authorized — policy denial,
		// do NOT fall through to other validators.
		log.Printf("OIDC authorization failed for /v1/status: %v", oidcErr)
		return nil, errors.New("authentication failed")
	}
	log.Printf("OIDC verification failed for /v1/status: %v", oidcErr)

	// --- GitHub validator (compile-time selected) ---
	ghErr := validateStatusGitHub(ctx, r)
	if ghErr == nil {
		return &statusAuthResult{}, nil
	}
	if !errors.Is(ghErr, errStatusAuthSkip) {
		// Real rejection — 401 immediately, no fall-through.
		log.Printf("GitHub status validator rejected request: %v", ghErr)
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
