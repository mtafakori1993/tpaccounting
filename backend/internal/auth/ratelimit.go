package auth

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// limiter is a per-key token bucket used to slow down credential guessing.
type limiter struct {
	mu      sync.Mutex
	perMin  int
	buckets map[string]*bucket
}

type bucket struct {
	l    *rate.Limiter
	seen time.Time
}

func newLimiter(perMinute int) *limiter {
	if perMinute <= 0 {
		perMinute = 10
	}
	l := &limiter{perMin: perMinute, buckets: map[string]*bucket{}}
	go l.gc()
	return l
}

func (l *limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{l: rate.NewLimiter(rate.Every(time.Minute/time.Duration(l.perMin)), l.perMin)}
		l.buckets[key] = b
	}
	b.seen = time.Now()
	return b.l.Allow()
}

func (l *limiter) gc() {
	for range time.Tick(10 * time.Minute) {
		l.mu.Lock()
		for k, b := range l.buckets {
			if time.Since(b.seen) > 30*time.Minute {
				delete(l.buckets, k)
			}
		}
		l.mu.Unlock()
	}
}
