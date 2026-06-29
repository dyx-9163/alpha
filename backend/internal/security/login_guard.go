package security

import (
	"strings"
	"sync"
	"time"
)

type LoginGuard struct {
	mu          sync.Mutex
	maxFailures int
	lockout     time.Duration
	now         func() time.Time
	attempts    map[string]loginAttempt
}

type loginAttempt struct {
	Failures    int
	LockedUntil time.Time
}

func NewLoginGuard(maxFailures int, lockout time.Duration) *LoginGuard {
	return &LoginGuard{
		maxFailures: maxFailures,
		lockout:     lockout,
		now:         time.Now,
		attempts:    map[string]loginAttempt{},
	}
}

func (g *LoginGuard) LockedUntil(key string) (time.Time, bool) {
	if !g.enabled() {
		return time.Time{}, false
	}
	key = normalizeGuardKey(key)
	now := g.now()
	g.mu.Lock()
	defer g.mu.Unlock()
	attempt := g.attempts[key]
	if attempt.LockedUntil.After(now) {
		return attempt.LockedUntil, true
	}
	if !attempt.LockedUntil.IsZero() {
		delete(g.attempts, key)
	}
	return time.Time{}, false
}

func (g *LoginGuard) RecordFailure(key string) (time.Time, bool) {
	if !g.enabled() {
		return time.Time{}, false
	}
	key = normalizeGuardKey(key)
	now := g.now()
	g.mu.Lock()
	defer g.mu.Unlock()
	attempt := g.attempts[key]
	if attempt.LockedUntil.After(now) {
		return attempt.LockedUntil, true
	}
	attempt.Failures++
	if attempt.Failures >= g.maxFailures {
		attempt.Failures = 0
		attempt.LockedUntil = now.Add(g.lockout)
		g.attempts[key] = attempt
		return attempt.LockedUntil, true
	}
	g.attempts[key] = attempt
	return time.Time{}, false
}

func (g *LoginGuard) RecordSuccess(key string) {
	if !g.enabled() {
		return
	}
	key = normalizeGuardKey(key)
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.attempts, key)
}

func (g *LoginGuard) enabled() bool {
	return g != nil && g.maxFailures > 0 && g.lockout > 0
}

func normalizeGuardKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return "unknown"
	}
	return key
}
