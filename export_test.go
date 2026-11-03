package ledger

import "time"

// SetMeterRetryBackoff lets a test shrink the retry backoff for keyed usage
// events, which is one second doubling to thirty in production.
func SetMeterRetryBackoff(l *Ledger, initial, maxDelay time.Duration) {
	l.retryInitial = initial
	l.retryMax = maxDelay
}
