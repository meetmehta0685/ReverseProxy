package proxy

import (
	"sync"
	"time"
)

type rateLimiter struct {
	mu     sync.Mutex
	limit  int
	count  int
	window time.Time
	now    func() time.Time
}

func newRateLimiter(limit int) *rateLimiter {
	now := time.Now
	return &rateLimiter{limit: limit, window: now(), now: now}
}

func (l *rateLimiter) allow() bool {
	if l.limit <= 0 {
		return true
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if now.Before(l.window) || now.Sub(l.window) >= time.Second {
		l.window = now
		l.count = 0
	}
	if l.count >= l.limit {
		return false
	}
	l.count++
	return true
}

func (l *rateLimiter) retryAfter() time.Duration {
	if l.limit <= 0 {
		return 0
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	remaining := time.Second - l.now().Sub(l.window)
	if remaining <= 0 {
		return time.Second
	}
	return remaining
}
