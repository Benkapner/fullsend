// Package jirapoll implements a polling-based input driver for Jira,
// converting issue comments, label changes, and status transitions into
// NormalizedEvents per ADR 0063's write-then-verify coordination protocol.
package jirapoll

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"regexp"
	"time"

	"github.com/fullsend-ai/fullsend/internal/dispatch"
	"github.com/fullsend-ai/fullsend/internal/forge/jira"
	"github.com/fullsend-ai/fullsend/internal/poll"

	"github.com/google/uuid"
)

// validProjectKey matches Jira Cloud project keys: 2–10 uppercase
// alphanumeric characters, starting with a letter. Jira Cloud only allows
// uppercase letters and digits in project keys (the driver is Cloud-only;
// Data Center's jira.projectkey.pattern is admin-customizable and not
// supported here). Validated before interpolation into JQL.
var validProjectKey = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,9}$`)

// Poller discovers Jira events and dispatches agent stages.
type Poller struct {
	client              JiraClient
	router              dispatch.EventRouter
	opts                Options
	dispatches          []poll.Dispatch
	sleepFn             func(time.Duration) // overridable for testing
	roleMembership      map[string]string   // accountID → Jira project role name
	statusCategoryCache map[string]string   // status name → statusCategory key, reset each cycle
}

// New creates a Jira Poller with the given options.
func New(client JiraClient, router dispatch.EventRouter, opts Options) *Poller {
	if opts.M == 0 {
		opts.M = 50
	}
	if opts.N == 0 {
		opts.N = 5
	}
	if opts.StaleThreshold == 0 {
		opts.StaleThreshold = 900 * time.Second
	}
	if opts.FirstPollBackfillWindow == 0 {
		opts.FirstPollBackfillWindow = 24 * time.Hour
	}
	return &Poller{
		client:  client,
		router:  router,
		opts:    opts,
		sleepFn: time.Sleep,
	}
}

// Run executes a single poll cycle per ADR 0063.
func (p *Poller) Run(ctx context.Context) error {
	cycleID := uuid.New().String()
	p.dispatches = nil
	p.statusCategoryCache = make(map[string]string)
	p.roleMembership = make(map[string]string)

	// Step 1: Execute JQL to get candidate issues.
	candidates, err := p.searchCandidates(ctx)
	if err != nil {
		return fmt.Errorf("search candidates: %w", err)
	}

	// Step 2: Filter locked issues, clean up stale locks.
	unlocked, err := p.filterLocked(ctx, candidates)
	if err != nil {
		return fmt.Errorf("filter locked: %w", err)
	}

	// Step 3: Randomly select min(N, len(unlocked)) candidates.
	selected := selectRandom(unlocked, p.opts.N)

	// Load project role membership for actor role resolution. Deferred
	// until after selection so a cycle with nothing to process doesn't
	// spend Jira API calls resolving roles that end up unused.
	if len(selected) > 0 {
		if p.opts.JiraProject != "" {
			membership, err := p.client.GetProjectRoleMembership(ctx, p.opts.JiraProject)
			if err != nil {
				log.Printf("WARNING: loading project roles: %v (defaulting to external)", err)
				membership = make(map[string]string)
			}
			p.roleMembership = membership
		} else {
			log.Printf("WARNING: no Jira project key set, cannot resolve actor roles (defaulting to external)")
		}
	}

	// Step 4: Process each selected issue.
	var processErrors int
	for _, issue := range selected {
		if err := p.processIssue(ctx, issue, cycleID); err != nil {
			log.Printf("WARNING: processing %s: %v", issue.Key, err)
			processErrors++
		}
	}
	if processErrors > 0 && processErrors == len(selected) {
		return fmt.Errorf("all %d selected issues failed to process", processErrors)
	}

	// Step 5: Write dispatch records.
	if p.opts.OutputPath != "" {
		if err := p.writeDispatches(p.opts.OutputPath); err != nil {
			return fmt.Errorf("write dispatches: %w", err)
		}
	}

	log.Printf("poll complete: %d candidates, %d unlocked, %d selected, %d dispatches",
		len(candidates), len(unlocked), len(selected), len(p.dispatches))
	return nil
}

// searchCandidates executes JQL and collects up to M results.
func (p *Poller) searchCandidates(ctx context.Context) ([]jira.Issue, error) {
	jql := p.opts.JQL
	if jql == "" {
		if !validProjectKey.MatchString(p.opts.JiraProject) {
			return nil, fmt.Errorf("invalid Jira project key %q: must match %s", p.opts.JiraProject, validProjectKey.String())
		}
		jql = fmt.Sprintf("project = %q AND statusCategory != Done ORDER BY updated DESC", p.opts.JiraProject)
	}

	return p.client.SearchIssues(ctx, jql, p.opts.M)
}

// filterLocked removes locked issues and cleans up stale locks. If every
// candidate is dropped by a failing lock-property read or a failing
// stale-lock release (e.g. broken auth, or read-only credentials that
// cannot write entity properties), it returns an error instead of silently
// reporting zero unlocked issues, which would otherwise be
// indistinguishable from a genuinely quiet Jira project.
func (p *Poller) filterLocked(ctx context.Context, issues []jira.Issue) ([]jira.Issue, error) {
	var unlocked []jira.Issue
	var readErrors, releaseErrors int
	for _, issue := range issues {
		lock, err := p.readLock(ctx, issue.Key)
		if err != nil {
			log.Printf("WARNING: reading lock for %s: %v (skipping)", issue.Key, err)
			readErrors++
			continue
		}
		if lock != nil {
			if isLockStale(*lock, p.opts.StaleThreshold) {
				log.Printf("cleaning stale lock on %s (age > %s)", issue.Key, p.opts.StaleThreshold)
				if err := p.releaseLock(ctx, issue.Key, lock.ID); err != nil {
					log.Printf("WARNING: cleaning stale lock for %s: %v", issue.Key, err)
					releaseErrors++
					continue
				}
			} else {
				continue
			}
		}
		unlocked = append(unlocked, issue)
	}
	if len(issues) > 0 && readErrors+releaseErrors == len(issues) {
		return nil, fmt.Errorf("all %d candidates dropped by lock errors (%d reads, %d stale-lock releases failed)", len(issues), readErrors, releaseErrors)
	}
	return unlocked, nil
}

// selectRandom randomly selects min(n, len(items)) items.
func selectRandom(items []jira.Issue, n int) []jira.Issue {
	if len(items) <= n {
		return items
	}
	rand.Shuffle(len(items), func(i, j int) {
		items[i], items[j] = items[j], items[i]
	})
	return items[:n]
}

// processIssue acquires the lock, detects changes, converts, routes,
// dispatches, and advances lastCheck.
func (p *Poller) processIssue(ctx context.Context, issue jira.Issue, cycleID string) error {
	// Attempt lock.
	acquired, err := p.attemptLock(ctx, issue.Key, cycleID)
	if err != nil {
		return fmt.Errorf("lock %s: %w", issue.Key, err)
	}
	if !acquired {
		log.Printf("lock contention on %s, skipping", issue.Key)
		return nil
	}
	// KNOWN LIMITATION: the lock covers the change-detection window only.
	// It is released here at the end of processIssue — before writeDispatches
	// runs and before the downstream dispatch step consumes the records — so
	// it does not provide the through-dispatch-scheduling ownership ADR 0063
	// describes; that handoff is part of the same tracked follow-up as the
	// lastCheck dispatch-confirmation note below. The lock is also never
	// renewed while an issue is processed: a cycle stalled longer than
	// StaleThreshold can have its lock reclaimed as stale by a concurrent
	// poller, which may duplicate dispatches for the same activity.
	defer func() {
		if err := p.releaseLock(ctx, issue.Key, cycleID); err != nil {
			log.Printf("WARNING: releasing lock for %s: %v", issue.Key, err)
		}
	}()

	// Read lastCheck.
	lastCheck, err := p.readLastCheck(ctx, issue.Key)
	if err != nil {
		return fmt.Errorf("read lastCheck for %s: %w", issue.Key, err)
	}
	// Detect changes.
	result, err := p.detectChanges(ctx, issue, lastCheck)
	if err != nil {
		return fmt.Errorf("detect changes for %s: %w", issue.Key, err)
	}

	if len(result.events) == 0 {
		// No routable events, but there may have been changelog entries
		// with unsupported fields. Advance lastCheck past them so the
		// poller does not re-scan the same updates every cycle.
		if !result.maxSeen.IsZero() {
			if err := p.advanceLastCheck(ctx, issue.Key, result.maxSeen); err != nil {
				log.Printf("WARNING: advancing lastCheck for %s: %v", issue.Key, err)
			}
		}
		return nil
	}

	// Deduplicate.
	events := deduplicate(result.events)

	// Filter bot events.
	events = filterBotEvents(events)

	// Convert, route, dispatch. maxTime starts at result.maxSeen
	// (the latest timestamp across all changelog entries) so that
	// lastCheck always advances past all inspected entries. This
	// prevents the poller from stalling when a routing error persists
	// across cycles. The trade-off is that a transiently failing event
	// is skipped rather than retried.
	maxTime := result.maxSeen
	dispatchCountBefore := len(p.dispatches)
	for _, event := range events {
		ne := p.toNormalizedEvent(event)

		var stages []string
		if p.router != nil {
			stages, err = p.router.Route(&ne)
			if err != nil {
				log.Printf("WARNING: routing event %s: %v", event.Key(), err)
				continue
			}
		}

		if len(stages) > 0 {
			neJSON, err := json.Marshal(ne)
			if err != nil {
				log.Printf("WARNING: marshaling event payload for %s: %v", event.Key(), err)
				continue
			}
			payloadB64 := base64.StdEncoding.EncodeToString(neJSON)

			for _, stage := range stages {
				p.dispatches = append(p.dispatches, poll.Dispatch{
					Stage:           stage,
					EventType:       event.Type,
					ResourceKey:     fmt.Sprintf("issue-%s", event.IssueKey),
					IID:             parseIssueID(event.IssueID),
					EventPayloadB64: payloadB64,
				})
			}
		}

		if event.UpdatedAt.After(maxTime) {
			maxTime = event.UpdatedAt
		}
	}

	// KNOWN LIMITATION: lastCheck below advances as soon as routing succeeds,
	// but the dispatch records written here are only *scheduled* by a
	// separate downstream CI step (see docs/guides/user/jira-integration.md)
	// that is not yet confirmed back to the poller. Per ADR 0063, lastCheck
	// should only advance once the output driver confirms scheduling — until
	// a real output driver replaces the shell-based dispatch step (tracked
	// follow-up), a failure in that downstream step will silently drop the
	// event instead of being retried.
	if len(p.dispatches) > dispatchCountBefore {
		log.Printf("WARNING: %s: lastCheck is advancing for %d dispatch(es) that are not yet confirmed as scheduled downstream; see KNOWN LIMITATION note in processIssue", issue.Key, len(p.dispatches)-dispatchCountBefore)
	}

	// Advance lastCheck.
	if !maxTime.IsZero() {
		if err := p.advanceLastCheck(ctx, issue.Key, maxTime); err != nil {
			log.Printf("WARNING: advancing lastCheck for %s: %v", issue.Key, err)
		}
	}

	return nil
}

// deduplicate removes duplicate events based on their Key().
func deduplicate(events []JiraEvent) []JiraEvent {
	seen := make(map[string]bool)
	var unique []JiraEvent
	for _, event := range events {
		key := event.Key()
		if seen[key] {
			continue
		}
		seen[key] = true
		unique = append(unique, event)
	}
	return unique
}

// filterBotEvents removes events from bot accounts.
func filterBotEvents(events []JiraEvent) []JiraEvent {
	var filtered []JiraEvent
	for _, event := range events {
		if actorKind(event) == "bot" {
			continue
		}
		filtered = append(filtered, event)
	}
	return filtered
}

// writeDispatches marshals the accumulated dispatches as a JSON array
// and writes them to the given file path.
func (p *Poller) writeDispatches(path string) error {
	dispatches := p.dispatches
	if len(dispatches) == 0 {
		return os.WriteFile(path, []byte("[]\n"), 0o644)
	}
	data, err := json.MarshalIndent(dispatches, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal dispatches: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}
