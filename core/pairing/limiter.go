package pairing

import (
	"sync"
	"time"
)

// limiter is a token bucket per source address: a burst of `burst` requests,
// refilled at one per `every`. /v1/pair is reachable by any tailnet device that
// has a TLS client certificate, so it must not be a free oracle.
type limiter struct {
	burst float64
	every time.Duration
	now   func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

const maxBuckets = 1024

func newLimiter(burst int, every time.Duration, now func() time.Time) *limiter {
	return &limiter{burst: float64(burst), every: every, now: now, buckets: map[string]*bucket{}}
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b := l.buckets[key]
	if b == nil {
		if len(l.buckets) >= maxBuckets { // forget the idle ones rather than grow without bound
			for k, o := range l.buckets {
				if now.Sub(o.last) > l.every*time.Duration(l.burst) {
					delete(l.buckets, k)
				}
			}
			if len(l.buckets) >= maxBuckets {
				return false
			}
		}
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	b.tokens = min(l.burst, b.tokens+float64(now.Sub(b.last))/float64(l.every))
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
