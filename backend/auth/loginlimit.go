package auth

import (
	"sync"
	"time"
)

// Basic in-memory brute-force protection for login: after too many failed
// attempts from the same client within a window, further attempts are refused
// until the window elapses. A successful login clears the counter.
//
// Note: behind a reverse proxy the client IP is the proxy's unless trusted
// proxies are configured, so the limit is effectively global in that case.
const (
	loginMaxAttempts = 8
	loginWindow      = 15 * time.Minute
)

type loginAttempt struct {
	fails   int
	resetAt time.Time
}

var loginMu sync.Mutex
var loginAttempts = map[string]*loginAttempt{}

// LoginAllowed reports whether a login attempt from ip is currently allowed,
// and if not, how long until it is.
func LoginAllowed(ip string) (bool, time.Duration) {
	loginMu.Lock()
	defer loginMu.Unlock()
	a := loginAttempts[ip]
	if a == nil || time.Now().After(a.resetAt) {
		return true, 0
	}
	if a.fails >= loginMaxAttempts {
		return false, time.Until(a.resetAt)
	}
	return true, 0
}

// RegisterLoginFailure records a failed attempt for ip.
func RegisterLoginFailure(ip string) {
	loginMu.Lock()
	defer loginMu.Unlock()
	now := time.Now()
	a := loginAttempts[ip]
	if a == nil || now.After(a.resetAt) {
		a = &loginAttempt{}
		loginAttempts[ip] = a
	}
	a.fails++
	a.resetAt = now.Add(loginWindow)

	// Opportunistically drop stale entries so the map cannot grow unbounded.
	for k, v := range loginAttempts {
		if now.After(v.resetAt) {
			delete(loginAttempts, k)
		}
	}
}

// RegisterLoginSuccess clears the counter for ip after a successful login.
func RegisterLoginSuccess(ip string) {
	loginMu.Lock()
	defer loginMu.Unlock()
	delete(loginAttempts, ip)
}
