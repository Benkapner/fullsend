package install

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeMintDriver is a test double for MintDriver.
type fakeMintDriver struct {
	teardownCalled bool
	teardownErr    error
}

func (f *fakeMintDriver) Install(_ context.Context, _ string) (State, error) {
	return NewPerRepoState("org", "", "https://mint.test"), nil
}

func (f *fakeMintDriver) Teardown(_ context.Context, _ string, _ State) error {
	f.teardownCalled = true
	return f.teardownErr
}

func TestNewComposedDriver_OK(t *testing.T) {
	e := newFakeEnsurer()
	mint := &fakeMintDriver{}
	st := NewPerRepoState("org", "", "https://mint.test")

	d, err := newComposedDriver("org", mint, st, e, 3, t.Logf)
	require.NoError(t, err)
	require.NotNil(t, d)
	assert.Equal(t, 3, d.Capacity())
}

func TestNewComposedDriver_InvalidCapacity(t *testing.T) {
	_, err := newComposedDriver("org", nil, nil, nil, 0, t.Logf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "capacity must be positive")
}

func TestComposedDriver_AllocateAndDeallocate(t *testing.T) {
	e := newFakeEnsurer()
	mint := &fakeMintDriver{}
	st := NewPerRepoState("org", "", "https://mint.test")

	d, err := newComposedDriver("org", mint, st, e, 3, t.Logf)
	require.NoError(t, err)

	ctx := context.Background()

	// Allocate a repo.
	name, err := d.AllocateRepo(ctx)
	require.NoError(t, err)
	assert.Contains(t, name, "test-repo-")

	// Deallocate the repo.
	err = d.DeallocateRepo(ctx, name)
	require.NoError(t, err)

	// Re-allocate should succeed.
	name2, err := d.AllocateRepo(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, name2)
}

func TestComposedDriver_DeallocateUnknownName(t *testing.T) {
	e := newFakeEnsurer()
	mint := &fakeMintDriver{}
	st := NewPerRepoState("org", "", "https://mint.test")

	d, err := newComposedDriver("org", mint, st, e, 2, t.Logf)
	require.NoError(t, err)

	err = d.DeallocateRepo(context.Background(), "unknown-repo")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not an outstanding lease")
}

func TestComposedDriver_DoubleDeallocate(t *testing.T) {
	e := newFakeEnsurer()
	mint := &fakeMintDriver{}
	st := NewPerRepoState("org", "", "https://mint.test")

	d, err := newComposedDriver("org", mint, st, e, 2, t.Logf)
	require.NoError(t, err)

	ctx := context.Background()
	name, err := d.AllocateRepo(ctx)
	require.NoError(t, err)

	// First deallocate succeeds.
	err = d.DeallocateRepo(ctx, name)
	require.NoError(t, err)

	// Second deallocate fails.
	err = d.DeallocateRepo(ctx, name)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "double-release")
}

func TestComposedDriver_AllocateBlocksUntilDeallocate(t *testing.T) {
	e := newFakeEnsurer()
	mint := &fakeMintDriver{}
	st := NewPerRepoState("org", "", "https://mint.test")

	d, err := newComposedDriver("org", mint, st, e, 1, t.Logf)
	require.NoError(t, err)

	ctx := context.Background()

	// Exhaust the pool.
	name, err := d.AllocateRepo(ctx)
	require.NoError(t, err)

	// Allocate with a short-timeout context should fail.
	shortCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()

	_, err = d.AllocateRepo(shortCtx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "allocating repo")

	// Deallocate the first name.
	err = d.DeallocateRepo(ctx, name)
	require.NoError(t, err)

	// Now allocate should succeed.
	name2, err := d.AllocateRepo(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, name2)
}

func TestComposedDriver_AllocateEnsureError_ReturnsNameToPool(t *testing.T) {
	// Use a failing ensurer to verify the name is returned to the pool
	// on failure.
	failEnsurer := &failingEnsurer{err: fmt.Errorf("ensure failed")}
	mint := &fakeMintDriver{}
	st := NewPerRepoState("org", "", "https://mint.test")

	d, err := newComposedDriver("org", mint, st, failEnsurer, 1, t.Logf)
	require.NoError(t, err)

	ctx := context.Background()

	// First allocate fails due to ensurer error.
	_, err = d.AllocateRepo(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ensure failed")

	// The name should be back in the pool — next allocate with a
	// working ensurer would succeed. But since we can't swap the
	// ensurer, verify the pool still has the slot by checking capacity
	// or attempting another allocate (which will also fail but proves
	// the pool isn't exhausted).
	shortCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	_, err = d.AllocateRepo(shortCtx)
	// Should get an ensure error, not a context deadline (proving the
	// slot was returned).
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ensure failed")
}

func TestComposedDriver_FinalizeNoOutstanding(t *testing.T) {
	mint := &fakeMintDriver{}
	st := NewPerRepoState("org", "", "https://mint.test")
	e := newFakeEnsurer()

	d, err := newComposedDriver("org", mint, st, e, 2, t.Logf)
	require.NoError(t, err)

	err = d.Finalize(context.Background())
	require.NoError(t, err)
	assert.True(t, mint.teardownCalled, "mint teardown should be called")
}

func TestComposedDriver_FinalizeWithOutstanding(t *testing.T) {
	mint := &fakeMintDriver{}
	st := NewPerRepoState("org", "", "https://mint.test")
	e := newFakeEnsurer()

	d, err := newComposedDriver("org", mint, st, e, 2, t.Logf)
	require.NoError(t, err)

	ctx := context.Background()
	name, err := d.AllocateRepo(ctx)
	require.NoError(t, err)
	_ = name // outstanding

	err = d.Finalize(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "outstanding lease")
	assert.True(t, mint.teardownCalled, "mint teardown should still be called")
}

func TestComposedDriver_FinalizeJoinsErrors(t *testing.T) {
	mint := &fakeMintDriver{teardownErr: fmt.Errorf("teardown boom")}
	st := NewPerRepoState("org", "", "https://mint.test")
	e := newFakeEnsurer()

	d, err := newComposedDriver("org", mint, st, e, 2, t.Logf)
	require.NoError(t, err)

	ctx := context.Background()
	_, err = d.AllocateRepo(ctx)
	require.NoError(t, err)

	err = d.Finalize(ctx)
	require.Error(t, err)
	// Both errors should be present via errors.Join.
	assert.Contains(t, err.Error(), "outstanding lease")
	assert.Contains(t, err.Error(), "teardown boom")
}

func TestComposedDriver_ConcurrentAllocateDeallocate(t *testing.T) {
	e := newFakeEnsurer()
	mint := &fakeMintDriver{}
	st := NewPerRepoState("org", "", "https://mint.test")

	const poolSize = 4
	d, err := newComposedDriver("org", mint, st, e, poolSize, t.Logf)
	require.NoError(t, err)

	const goroutines = 8
	ctx := context.Background()
	errs := make([]error, goroutines)
	var wg sync.WaitGroup

	wg.Add(goroutines)
	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			name, allocErr := d.AllocateRepo(ctx)
			if allocErr != nil {
				errs[idx] = allocErr
				return
			}
			// Simulate some work.
			time.Sleep(5 * time.Millisecond)
			errs[idx] = d.DeallocateRepo(ctx, name)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		assert.NoError(t, err, "goroutine %d failed", i)
	}

	// All names should be back in the pool.
	err = d.Finalize(ctx)
	require.NoError(t, err, "no outstanding leases after all deallocations")
}

// failingEnsurer always returns an error.
type failingEnsurer struct {
	err error
}

func (f *failingEnsurer) EnsureRepo(_ context.Context, _, _ string) (State, error) {
	return nil, f.err
}
