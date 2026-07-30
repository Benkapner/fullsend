package jirapoll

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"time"

	"github.com/fullsend-ai/fullsend/internal/dispatch"
	"github.com/fullsend-ai/fullsend/internal/forge/jira"
	"github.com/fullsend-ai/fullsend/internal/poll"

	"github.com/google/uuid"
)

// Poller discovers Jira events and dispatches agent stages.
type Poller struct {
	client         JiraClient
	router         dispatch.EventRouter
	opts           Options
	dispatches     []poll.Dispatch
	sleepFn        func(time.Duration) // overridable for testing
	roleMembership map[string]string   // accountID → Jira project role name
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

	// Load project role membership for actor role resolution.
	if p.opts.JiraProject != "" {
		membership, err := p.client.GetProjectRoleMembership(ctx, p.opts.JiraProject)
		if err != nil {
			log.Printf("WARNING: loading project roles: %v (defaulting to external)", err)
			membership = make(map[string]string)
		}
		p.roleMembership = membership
	} else {
		log.Printf("WARNING: no Jira project key set, cannot resolve actor roles (defaulting to external)")
		p.roleMembership = make(map[string]string)
	}

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

	// Step 4: Process each selected issue.
	for _, issue := range selected {
		if err := p.processIssue(ctx, issue, cycleID); err != nil {
			log.Printf("WARNING: processing %s: %v", issue.Key, err)
		}
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
		jql = fmt.Sprintf("project = %s AND status != Done ORDER BY updated DESC", p.opts.JiraProject)
	}

	all, err := p.client.SearchIssues(ctx, jql)
	if err != nil {
		return nil, err
	}

	if len(all) > p.opts.M {
		all = all[:p.opts.M]
	}
	return all, nil
}

// filterLocked removes locked issues and cleans up stale locks.
func (p *Poller) filterLocked(ctx context.Context, issues []jira.Issue) ([]jira.Issue, error) {
	var unlocked []jira.Issue
	for _, issue := range issues {
		lock, err := p.readLock(ctx, issue.Key)
		if err != nil {
			log.Printf("WARNING: reading lock for %s: %v (skipping)", issue.Key, err)
			continue
		}
		if lock != nil {
			if isLockStale(*lock, p.opts.StaleThreshold) {
				log.Printf("cleaning stale lock on %s (age > %s)", issue.Key, p.opts.StaleThreshold)
				if err := p.releaseLock(ctx, issue.Key, ""); err != nil {
					log.Printf("WARNING: cleaning stale lock for %s: %v", issue.Key, err)
					continue
				}
			} else {
				continue
			}
		}
		unlocked = append(unlocked, issue)
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

	// Convert, route, dispatch. Only advance maxTime past events that
	// were successfully routed (or had no matching stages). Events that
	// fail routing are not counted so they can be retried next cycle.
	maxTime := result.maxSeen
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

		for _, stage := range stages {
			p.dispatches = append(p.dispatches, poll.Dispatch{
				Stage:       stage,
				EventType:   event.Type,
				ResourceKey: fmt.Sprintf("issue-%s", event.IssueKey),
				IID:         parseIssueID(event.IssueID),
			})
		}

		if event.UpdatedAt.After(maxTime) {
			maxTime = event.UpdatedAt
		}
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
