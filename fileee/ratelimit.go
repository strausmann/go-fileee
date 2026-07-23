package fileee

import (
	"math/rand"
	"net/http"
	"time"

	"golang.org/x/time/rate"
)

// BackoffPolicy entscheidet, ob nach einem fehlgeschlagenen Request erneut versucht wird, und wie
// lange gewartet wird (ADR-0005: exponentiell + Jitter).
type BackoffPolicy interface {
	ShouldRetry(attempt int, resp *http.Response, err error) bool
	NextDelay(attempt int) time.Duration
}

// ExponentialBackoff ist die Default-Implementierung: Basis-Delay * 2^attempt (gedeckelt bei
// MaxDelay) + Jitter, maximal MaxAttempts Versuche. Rand ist injizierbar für deterministische
// Tests (nil -> math/rand-Default mit Zeit-Seed).
type ExponentialBackoff struct {
	BaseDelay   time.Duration
	MaxDelay    time.Duration
	MaxAttempts int
	// Rand ist **nicht** nebenläufig-sicher — ein explizit gesetztes *rand.Rand NICHT über
	// gleichzeitige Requests teilen (relevant für Task 10).
	Rand *rand.Rand
}

// NewExponentialBackoff liefert eine ExponentialBackoff-Instanz mit konservativen
// Default-Werten (200ms Basis, 5s Deckel, 5 Versuche).
func NewExponentialBackoff() *ExponentialBackoff {
	return &ExponentialBackoff{
		BaseDelay:   200 * time.Millisecond,
		MaxDelay:    5 * time.Second,
		MaxAttempts: 5,
	}
}

func (b *ExponentialBackoff) ShouldRetry(attempt int, resp *http.Response, err error) bool {
	if attempt >= b.MaxAttempts {
		return false
	}
	if err != nil {
		return true
	}
	if resp == nil {
		return false
	}
	return resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
}

func (b *ExponentialBackoff) NextDelay(attempt int) time.Duration {
	delay := b.BaseDelay << attempt
	if delay > b.MaxDelay || delay <= 0 {
		delay = b.MaxDelay
	}
	r := b.Rand
	if r == nil {
		r = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	jitterRange := int64(delay)/2 + 1
	jitter := time.Duration(r.Int63n(jitterRange))
	return delay/2 + jitter
}

// newLimiter baut den Token-Bucket-Limiter (ADR-0005). Default konservativ: 1 req/s, Burst 3.
func newLimiter(rps float64, burst int) *rate.Limiter {
	if rps <= 0 {
		rps = 1
	}
	if burst <= 0 {
		burst = 3
	}
	return rate.NewLimiter(rate.Limit(rps), burst)
}
