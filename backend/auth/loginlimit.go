package auth

import (
	"sync"
	"time"
)

// Brute-force protection for password logins, in memory: failures are counted
// per client address and per account, and past the limit further attempts are
// refused until the window elapses. A successful login clears both counters.
//
// The account's limit sits above the address's, so one address alone never
// locks an account out, only a spread attack does. API tokens are not logins
// and keep working meanwhile.
//
// Behind a reverse proxy the client address is the proxy's unless trusted
// proxies are configured, so that limit is effectively global in that case.
const (
	loginMaxAttempts   = 8
	accountMaxAttempts = 12
	loginWindow        = 15 * time.Minute
)

type loginAttempt struct {
	fails   int
	resetAt time.Time
}

var loginMu sync.Mutex
var loginAttempts = map[string]*loginAttempt{}

// accountKey is the counter of an account. A name no account can have gets
// none, so made-up names cannot fill the map.
func accountKey(username string) (string, bool) {
	if username == "" {
		username = OwnerAccount
	}
	if !usernameRe.MatchString(username) {
		return "", false
	}
	return "account:" + username, true
}

func blockedFor(key string, max int, now time.Time) time.Duration {
	a := loginAttempts[key]
	if a == nil || now.After(a.resetAt) || a.fails < max {
		return 0
	}
	return a.resetAt.Sub(now)
}

// LoginAllowed reports whether a login from ip as username may be tried now,
// and if not, how long until it may.
func LoginAllowed(ip, username string) (bool, time.Duration) {
	loginMu.Lock()
	defer loginMu.Unlock()
	now := time.Now()
	wait := blockedFor(ip, loginMaxAttempts, now)
	if key, ok := accountKey(username); ok {
		if w := blockedFor(key, accountMaxAttempts, now); w > wait {
			wait = w
		}
	}
	return wait == 0, wait
}

func countFailure(key string, now time.Time) {
	a := loginAttempts[key]
	if a == nil || now.After(a.resetAt) {
		a = &loginAttempt{}
		loginAttempts[key] = a
	}
	a.fails++
	a.resetAt = now.Add(loginWindow)
}

// RegisterLoginFailure records a failed attempt from ip as username.
func RegisterLoginFailure(ip, username string) {
	loginMu.Lock()
	defer loginMu.Unlock()
	now := time.Now()
	countFailure(ip, now)
	if key, ok := accountKey(username); ok {
		countFailure(key, now)
	}

	// Opportunistically drop stale entries so the map cannot grow unbounded.
	for k, v := range loginAttempts {
		if now.After(v.resetAt) {
			delete(loginAttempts, k)
		}
	}
}

// RegisterLoginSuccess clears the counters of ip and of the account.
func RegisterLoginSuccess(ip, username string) {
	loginMu.Lock()
	defer loginMu.Unlock()
	delete(loginAttempts, ip)
	if key, ok := accountKey(username); ok {
		delete(loginAttempts, key)
	}
}
