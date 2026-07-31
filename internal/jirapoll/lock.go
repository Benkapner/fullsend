package jirapoll

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/fullsend-ai/fullsend/internal/forge"
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
		// The write succeeded but verification failed. Best-effort release
		// the lock we just wrote so a transient read error doesn't leak it
		// until the stale-lock threshold expires.
		if releaseErr := p.releaseLock(ctx, issueKey, lockID); releaseErr != nil {
			log.Printf("WARNING: releasing lock on %s after verify failure: %v", issueKey, releaseErr)
		}
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

// releaseLock removes the lock from the issue. When expectedID is non-empty,
// the lock is only deleted if its ID still matches, preventing a slow poller
// from deleting a lock that a faster concurrent poller has already replaced.
func (p *Poller) releaseLock(ctx context.Context, issueKey, expectedID string) error {
	owner, repo := splitOwnerRepo(p.opts.TargetRepo)
	propKey := lockPropertyKey(owner, repo)

	if expectedID != "" {
		raw, err := p.client.GetEntityProperty(ctx, issueKey, propKey)
		if err != nil {
			if errors.Is(err, forge.ErrNotFound) {
				return nil // already gone
			}
			return fmt.Errorf("read lock before release: %w", err)
		}
		var current LockValue
		if err := json.Unmarshal(raw, &current); err != nil {
			return fmt.Errorf("unmarshal lock before release: %w", err)
		}
		if current.ID != expectedID {
			log.Printf("lock on %s owned by %s, not %s; skipping release", issueKey, current.ID, expectedID)
			return nil
		}
	}

	return p.client.DeleteEntityProperty(ctx, issueKey, propKey)
}

// readLastCheck reads the lastCheck timestamp for an issue.
func (p *Poller) readLastCheck(ctx context.Context, issueKey string) (time.Time, error) {
	owner, repo := splitOwnerRepo(p.opts.TargetRepo)
	propKey := lastCheckPropertyKey(owner, repo)

	raw, err := p.client.GetEntityProperty(ctx, issueKey, propKey)
	if err != nil {
		if errors.Is(err, forge.ErrNotFound) {
			return time.Time{}, nil // first poll for this issue
		}
		return time.Time{}, fmt.Errorf("get lastCheck property: %w", err)
	}
	if len(raw) == 0 {
		return time.Time{}, nil
	}

	var ts string
	if err := json.Unmarshal(raw, &ts); err != nil {
		return time.Time{}, fmt.Errorf("unmarshal lastCheck: %w", err)
	}

	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		// Fall back to RFC3339 for values stored before the Nano switch.
		t, err = time.Parse(time.RFC3339, ts)
		if err != nil {
			return time.Time{}, fmt.Errorf("parse lastCheck: %w", err)
		}
	}
	return t, nil
}

// advanceLastCheck updates the lastCheck timestamp for an issue.
func (p *Poller) advanceLastCheck(ctx context.Context, issueKey string, t time.Time) error {
	owner, repo := splitOwnerRepo(p.opts.TargetRepo)
	propKey := lastCheckPropertyKey(owner, repo)
	return p.client.SetEntityProperty(ctx, issueKey, propKey, t.UTC().Format(time.RFC3339Nano))
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
		if errors.Is(err, forge.ErrNotFound) {
			return nil, nil // no lock property yet
		}
		return nil, fmt.Errorf("get lock property: %w", err)
	}
	if len(raw) == 0 {
		return nil, nil
	}

	var lock LockValue
	if err := json.Unmarshal(raw, &lock); err != nil {
		return nil, fmt.Errorf("unmarshal lock: %w", err)
	}
	return &lock, nil
}
