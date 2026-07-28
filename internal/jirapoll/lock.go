package jirapoll

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"
)

// lockPropertyKey returns the entity property key for locks.
func lockPropertyKey(owner, repo string) string {
	return fmt.Sprintf("fullsend.poll.%s.%s.lock", owner, repo)
}

// lastCheckPropertyKey returns the entity property key for lastCheck.
func lastCheckPropertyKey(owner, repo string) string {
	return fmt.Sprintf("fullsend.poll.%s.%s.lastCheck", owner, repo)
}

// splitOwnerRepo splits "owner/repo" into owner and repo.
func splitOwnerRepo(slug string) (string, string) {
	idx := strings.LastIndex(slug, "/")
	if idx < 0 {
		return "", slug
	}
	return slug[:idx], slug[idx+1:]
}

// attemptLock tries to acquire a lock on the issue using write-then-verify.
// Returns true if the lock was acquired successfully.
func (p *Poller) attemptLock(ctx context.Context, issueKey, lockID string) (bool, error) {
	owner, repo := splitOwnerRepo(p.opts.TargetRepo)
	propKey := lockPropertyKey(owner, repo)

	lock := LockValue{
		ID:    lockID,
		TS:    time.Now().UTC().Format(time.RFC3339),
		Phase: "pending",
	}

	if err := p.client.SetEntityProperty(ctx, issueKey, propKey, lock); err != nil {
		return false, fmt.Errorf("set lock property: %w", err)
	}

	// Jitter sleep: 500-1500ms to allow concurrent pollers to write.
	jitter := time.Duration(500+rand.IntN(1000)) * time.Millisecond
	p.sleepFn(jitter)

	// Re-read and verify our UUID still holds.
	raw, err := p.client.GetEntityProperty(ctx, issueKey, propKey)
	if err != nil {
		return false, fmt.Errorf("verify lock: %w", err)
	}
	if len(raw) == 0 {
		return false, nil
	}

	var current LockValue
	if err := json.Unmarshal(raw, &current); err != nil {
		return false, fmt.Errorf("unmarshal lock: %w", err)
	}

	return current.ID == lockID, nil
}

// releaseLock removes the lock from the issue.
func (p *Poller) releaseLock(ctx context.Context, issueKey string) error {
	owner, repo := splitOwnerRepo(p.opts.TargetRepo)
	propKey := lockPropertyKey(owner, repo)
	return p.client.DeleteEntityProperty(ctx, issueKey, propKey)
}

// readLastCheck reads the lastCheck timestamp for an issue.
func (p *Poller) readLastCheck(ctx context.Context, issueKey string) (time.Time, error) {
	owner, repo := splitOwnerRepo(p.opts.TargetRepo)
	propKey := lastCheckPropertyKey(owner, repo)

	raw, err := p.client.GetEntityProperty(ctx, issueKey, propKey)
	if err != nil {
		return time.Time{}, fmt.Errorf("get lastCheck property: %w", err)
	}
	if len(raw) == 0 {
		// Property empty means first poll for this issue.
		return time.Time{}, nil
	}

	var ts string
	if err := json.Unmarshal(raw, &ts); err != nil {
		return time.Time{}, fmt.Errorf("unmarshal lastCheck: %w", err)
	}

	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse lastCheck: %w", err)
	}
	return t, nil
}

// advanceLastCheck updates the lastCheck timestamp for an issue.
func (p *Poller) advanceLastCheck(ctx context.Context, issueKey string, t time.Time) error {
	owner, repo := splitOwnerRepo(p.opts.TargetRepo)
	propKey := lastCheckPropertyKey(owner, repo)
	return p.client.SetEntityProperty(ctx, issueKey, propKey, t.UTC().Format(time.RFC3339))
}

// isLockStale checks if a lock has exceeded the stale threshold.
func isLockStale(lock LockValue, threshold time.Duration) bool {
	t, err := time.Parse(time.RFC3339, lock.TS)
	if err != nil {
		// Unparseable timestamp is treated as stale.
		return true
	}
	return time.Since(t) > threshold
}

// readLock reads the lock value for an issue. Returns nil if no lock exists.
func (p *Poller) readLock(ctx context.Context, issueKey string) (*LockValue, error) {
	owner, repo := splitOwnerRepo(p.opts.TargetRepo)
	propKey := lockPropertyKey(owner, repo)

	raw, err := p.client.GetEntityProperty(ctx, issueKey, propKey)
	if err != nil {
		return nil, fmt.Errorf("get lock property: %w", err)
	}
	if len(raw) == 0 {
		// Property empty means no lock.
		return nil, nil
	}

	var lock LockValue
	if err := json.Unmarshal(raw, &lock); err != nil {
		return nil, fmt.Errorf("unmarshal lock: %w", err)
	}
	return &lock, nil
}
