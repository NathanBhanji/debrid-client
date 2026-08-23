package auth

import (
	"sync"
	"time"
)

// loginLimiter is a fixed-window per-key (client IP) limiter for login
// attempts. Windows are pruned lazily; memory is bounded by the number of
// distinct clients seen per window, which is tiny for a self-hosted server.
type loginLimiter struct {
	mu       sync.Mutex
	window   time.Duration
	max      int
	attempts map[string]*windowCount
}

type windowCount struct {
	start time.Time
	n     int
}

func newLoginLimiter(max int, window time.Duration) *loginLimiter {
	return &loginLimiter{window: window, max: max, attempts: map[string]*windowCount{}}
}

// allow records an attempt for key and reports whether it is within the limit.
func (l *loginLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for k, w := range l.attempts {
		if now.Sub(w.start) > l.window {
			delete(l.attempts, k)
		}
	}
	w := l.attempts[key]
	if w == nil || now.Sub(w.start) > l.window {
		w = &windowCount{start: now}
		l.attempts[key] = w
	}
	w.n++
	return w.n <= l.max
}

// reset clears the window for key (after a successful login).
func (l *loginLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, key)
}
