package cli

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/fullsend-ai/fullsend/internal/ui"
)

func useSettleClock(t *testing.T, now time.Time) *[]time.Duration {
	t.Helper()
	var slept []time.Duration
	origNow, origSleep := settleNow, settleSleep
	settleNow = func() time.Time { return now }
	settleSleep = func(d time.Duration) { slept = append(slept, d) }
	t.Cleanup(func() { settleNow, settleSleep = origNow, origSleep })
	return &slept
}

func TestWaitForOpenShellFirstPoll_WaitsForTheRemainder(t *testing.T) {
	readyAt := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	slept := useSettleClock(t, readyAt.Add(7*time.Second))
	var buf bytes.Buffer

	got := waitForOpenShellFirstPoll(readyAt, ui.New(&buf))

	assert.Equal(t, 5*time.Second, got)
	assert.Equal(t, []time.Duration{5 * time.Second}, *slept)
	assert.Contains(t, buf.String(), "first policy poll")
	assert.Contains(t, buf.String(), "#3809")
}

func TestWaitForOpenShellFirstPoll_NoWaitOncePast(t *testing.T) {
	readyAt := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for _, elapsed := range []time.Duration{openShellFirstPollSettle, 30 * time.Second} {
		slept := useSettleClock(t, readyAt.Add(elapsed))
		var buf bytes.Buffer

		got := waitForOpenShellFirstPoll(readyAt, ui.New(&buf))

		assert.Zero(t, got, "elapsed %s", elapsed)
		assert.Empty(t, *slept, "elapsed %s", elapsed)
		assert.Empty(t, buf.String(), "no message when nothing is waited (elapsed %s)", elapsed)
	}
}

func TestWaitForOpenShellFirstPoll_NilPrinter(t *testing.T) {
	readyAt := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	slept := useSettleClock(t, readyAt)

	got := waitForOpenShellFirstPoll(readyAt, nil)

	assert.Equal(t, openShellFirstPollSettle, got)
	assert.Equal(t, []time.Duration{openShellFirstPollSettle}, *slept)
}
