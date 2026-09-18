package vaultsyncd

import "time"

// Backoff bounds. A failing worker retries at MinBackoff and doubles up to
// MaxBackoff, where it stays.
//
// The ceiling is what makes a persistent failure cheap rather than free: a vault
// whose peer is switched off for a week must not spend that week retrying, and
// must not stop retrying either, because the remedy — the peer coming back — is
// invisible from here and needs no announcement. Fifteen minutes is short enough
// that a person who fixes something sees it take effect while they are still
// looking at it.
const (
	MinBackoff = 30 * time.Second
	MaxBackoff = 15 * time.Minute
)

// backoffFor is the delay after a given number of consecutive failures.
//
// Deliberately deterministic, with no jitter. Jitter exists to spread a herd of
// clients off one server, and this is a companion for a vault with exactly one
// peer and no server: there is no herd to spread, and the cost of randomness
// would be a schedule no test can assert and no operator can predict. A vault
// that says "next attempt in four minutes" means it.
//
// It saturates rather than overflowing: the doubling stops at the ceiling, so a
// worker that has failed a thousand times waits the same fifteen minutes as one
// that has failed six.
func backoffFor(failures int) time.Duration {
	if failures <= 0 {
		return 0
	}
	delay := MinBackoff
	for i := 1; i < failures; i++ {
		delay *= 2
		if delay >= MaxBackoff {
			return MaxBackoff
		}
	}
	return delay
}
