package proxy

import (
	"sync"
	"time"
)

type circuitState uint8

const (
	circuitClosed circuitState = iota
	circuitOpen
	circuitHalfOpen
)

type breakerPermit struct {
	generation uint64
}

type circuitBreaker struct {
	mu               sync.Mutex
	failureThreshold int
	cooldown         time.Duration
	failures         int
	openedAt         time.Time
	generation       uint64
	state            circuitState
	now              func() time.Time
}

func newCircuitBreaker(failureThreshold int, cooldown time.Duration) *circuitBreaker {
	if failureThreshold <= 0 {
		failureThreshold = defaultCircuitFailureThreshold
	}
	if cooldown <= 0 {
		cooldown = defaultCircuitCooldown
	}
	return &circuitBreaker{
		failureThreshold: failureThreshold,
		cooldown:         cooldown,
		now:              time.Now,
	}
}

func (b *circuitBreaker) acquire() (breakerPermit, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case circuitClosed:
		return breakerPermit{generation: b.generation}, true
	case circuitOpen:
		if b.now().Sub(b.openedAt) < b.cooldown {
			return breakerPermit{}, false
		}
		// Admit exactly one probe after the cooldown. Other callers wait for
		// the probe's result instead of stampeding the recovering backend.
		b.state = circuitHalfOpen
		b.generation++
		return breakerPermit{generation: b.generation}, true
	case circuitHalfOpen:
		return breakerPermit{}, false
	default:
		return breakerPermit{}, false
	}
}

func (b *circuitBreaker) allow() bool {
	_, allowed := b.acquire()
	return allowed
}

func (b *circuitBreaker) recordSuccess(permit breakerPermit) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if permit.generation != b.generation {
		return
	}

	switch b.state {
	case circuitClosed:
		b.failures = 0
	case circuitHalfOpen:
		b.state = circuitClosed
		b.failures = 0
		b.openedAt = time.Time{}
	}
}

func (b *circuitBreaker) recordFailure(permit breakerPermit) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if permit.generation != b.generation {
		return
	}

	switch b.state {
	case circuitClosed:
		b.failures++
		if b.failures >= b.failureThreshold {
			b.state = circuitOpen
			b.openedAt = b.now()
			b.generation++
		}
	case circuitHalfOpen:
		b.state = circuitOpen
		b.openedAt = b.now()
		b.generation++
	}
}

func (b *circuitBreaker) retryAfter() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.state == circuitOpen {
		remaining := b.cooldown - b.now().Sub(b.openedAt)
		if remaining > 0 {
			return remaining
		}
	}
	return b.cooldown
}
