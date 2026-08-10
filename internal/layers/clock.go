package layers

import "time"

// Clock abstracts time.After for testability. Production code uses
// realClock (the default); tests inject a fake that resolves After
// channels immediately so poll/retry loops complete without wall-clock
// delays.
type Clock interface {
	After(d time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) After(d time.Duration) <-chan time.Time {
	return time.After(d)
}
