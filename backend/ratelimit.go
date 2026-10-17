package main

import (
	"container/list"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Rate limiter tuning. Every number of the limiter lives in this block.
const (
	// limiterWindow: failures are forgotten after this long without a new
	// one; the register counter is a fixed window of this length.
	limiterWindow = 15 * time.Minute
	// loginFreeFailures: failed logins per (IP, username) pair answered
	// normally before the delay starts. Requests without a valid known-device
	// cookie.
	loginFreeFailures = 5
	// loginKnownFailures: the same, for a request carrying a valid
	// known-device cookie bound to the username (lenient tier).
	loginKnownFailures = 20
	// loginIPMaxFailures: failures from one IP across all usernames; more
	// than this and every username from that IP is throttled (except
	// requests with a valid known-device cookie).
	loginIPMaxFailures = 30
	// loginBaseDelay is the wait after the free failures are used up; it
	// doubles with each further failure up to loginMaxDelay.
	loginBaseDelay = 1 * time.Second
	loginMaxDelay  = 15 * time.Minute
	// registerMaxPerIP: register attempts (successful or not, each costs a
	// bcrypt hash) per IP and window.
	registerMaxPerIP = 10
	// limiterMaxEntries bounds memory: the oldest entry is evicted beyond it.
	limiterMaxEntries = 10000

	knownDeviceCookie = "known_device"
	knownDeviceTTL    = sessionTTL // same lifetime as the session
	knownDeviceLabel  = "known-device-v1"
	limiterSweepBatch = 8 // old entries examined per insert
)

const tooManyMessage = "too many attempts, try again later"

type limitEntry struct {
	key     string
	count   int
	last    time.Time // last counted failure
	expires time.Time // forgotten from this moment on
	el      *list.Element
}

// limiter keeps failure counters in memory, bounded by maxEntries. Entries are
// created only when a failure (or a register attempt) is recorded, so a
// throttled client cannot grow the map. Safe for concurrent use.
type limiter struct {
	now        func() time.Time
	maxEntries int

	mu      sync.Mutex
	entries map[string]*limitEntry
	order   *list.List // front = least recently touched
}

func newLimiter(now func() time.Time) *limiter {
	return &limiter{
		now: now, maxEntries: limiterMaxEntries,
		entries: map[string]*limitEntry{}, order: list.New(),
	}
}

func (l *limiter) size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}

// delay is the wait after the k-th failure beyond the free ones (k >= 0):
// loginBaseDelay doubling, capped at loginMaxDelay.
func delay(k int) time.Duration {
	d := loginBaseDelay
	for ; k > 0 && d < loginMaxDelay; k-- {
		d *= 2
	}
	return min(d, loginMaxDelay)
}

func pairKey(ip, user string, known bool) string {
	tier := "s"
	if known {
		tier = "k"
	}
	return "p\x00" + tier + "\x00" + ip + "\x00" + user
}
func ipKey(ip string) string  { return "i\x00" + ip }
func regKey(ip string) string { return "r\x00" + ip }

// get returns the live entry for key or nil; an expired one is dropped.
// l.mu must be held.
func (l *limiter) get(key string, now time.Time) *limitEntry {
	e := l.entries[key]
	if e != nil && !now.Before(e.expires) {
		l.remove(e)
		return nil
	}
	return e
}

func (l *limiter) remove(e *limitEntry) {
	l.order.Remove(e.el)
	delete(l.entries, e.key)
}

// touch returns the entry for key, creating it when missing, and marks it
// most recently used. l.mu must be held.
func (l *limiter) touch(key string, now time.Time) *limitEntry {
	e := l.get(key, now)
	if e != nil {
		l.order.MoveToBack(e.el)
		return e
	}
	// Make room: drop a few expired entries from the old end, then the
	// oldest one if still full.
	for i := 0; i < limiterSweepBatch && l.order.Len() > 0; i++ {
		f := l.order.Front().Value.(*limitEntry)
		if now.Before(f.expires) {
			break
		}
		l.remove(f)
	}
	for len(l.entries) >= l.maxEntries && l.order.Len() > 0 {
		l.remove(l.order.Front().Value.(*limitEntry))
	}
	e = &limitEntry{key: key}
	e.el = l.order.PushBack(e)
	l.entries[key] = e
	return e
}

// pairWait is the wait imposed by the pair counter and the IP counter.
// l.mu must be held.
func (l *limiter) pairWait(ip, user string, known bool, now time.Time) time.Duration {
	free := loginFreeFailures
	if known {
		free = loginKnownFailures
	}
	var wait time.Duration
	if e := l.get(pairKey(ip, user, known), now); e != nil && e.count >= free {
		wait = e.last.Add(delay(e.count - free)).Sub(now)
	}
	if !known {
		if e := l.get(ipKey(ip), now); e != nil && e.count > loginIPMaxFailures {
			wait = max(wait, e.last.Add(delay(e.count-loginIPMaxFailures-1)).Sub(now))
		}
	}
	return max(wait, 0)
}

// loginWait returns how long the (ip, user) pair must wait before another
// login attempt (0 = go ahead). It only reads; it never creates entries.
func (l *limiter) loginWait(ip, user string, known bool) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.pairWait(ip, user, known, l.now())
}

// loginAttempt is the atomic check-and-reserve done before the password is
// checked. A throttled request gets the wait and changes nothing. An admitted
// request is counted as a failure right away (so a burst of concurrent
// requests cannot all pass the check); the handler calls loginSucceeded or
// loginAborted to take the reservation back. Cookie-less and known-device
// requests use separate pair counters, so a bot's burst cannot eat the
// budget of a real user holding a valid cookie; a known-device request does
// not touch the IP-wide counter.
func (l *limiter) loginAttempt(ip, user string, known bool) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if wait := l.pairWait(ip, user, known, now); wait > 0 {
		return wait
	}
	keys := []string{pairKey(ip, user, known)}
	if !known {
		keys = append(keys, ipKey(ip))
	}
	for _, k := range keys {
		e := l.touch(k, now)
		e.count++
		e.last = now
		e.expires = now.Add(limiterWindow)
	}
	return 0
}

// release takes one reserved attempt back from key. l.mu must be held.
func (l *limiter) release(key string) {
	e := l.entries[key]
	if e == nil {
		return
	}
	if e.count--; e.count <= 0 {
		l.remove(e)
	}
}

// loginAborted undoes the reservation of an admitted attempt that did not
// end in a verdict about the password (internal error).
func (l *limiter) loginAborted(ip, user string, known bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.release(pairKey(ip, user, known))
	if !known {
		l.release(ipKey(ip))
	}
}

// loginSucceeded undoes the reservation and forgets the pair's failures.
func (l *limiter) loginSucceeded(ip, user string, known bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !known {
		l.release(ipKey(ip))
	}
	for _, k := range []bool{false, true} {
		if e := l.entries[pairKey(ip, user, k)]; e != nil {
			l.remove(e)
		}
	}
}

// registerAttempt counts one register attempt from ip and returns how long
// the client must wait (0 = allowed). Throttled attempts are not counted.
func (l *limiter) registerAttempt(ip string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	e := l.get(regKey(ip), now)
	if e != nil && e.count >= registerMaxPerIP {
		return e.expires.Sub(now)
	}
	e = l.touch(regKey(ip), now)
	if e.count == 0 {
		e.expires = now.Add(limiterWindow)
	}
	e.count++
	e.last = now
	return 0
}

// clientIP is the host part of RemoteAddr and nothing else. X-Forwarded-For,
// X-Real-IP and Forwarded are ignored on purpose: a client-controlled header
// would let an attacker pick a fresh IP for every request. Behind a reverse
// proxy all clients look like the proxy (the app is local-only).
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// tooMany writes the 429 answer with Retry-After in whole seconds (>= 1).
func tooMany(w http.ResponseWriter, wait time.Duration) {
	secs := int((wait + time.Second - 1) / time.Second)
	w.Header().Set("Retry-After", strconv.Itoa(max(secs, 1)))
	writeError(w, http.StatusTooManyRequests, tooManyMessage)
}

// limitRegister wraps the register handler with the per-IP limit.
func (s *Server) limitRegister(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if wait := s.limiter.registerAttempt(clientIP(r)); wait > 0 {
			tooMany(w, wait)
			return
		}
		h(w, r)
	}
}

// knownDeviceKey is derived from JWT_SECRET so the raw secret is never used
// as the cookie key.
func (s *Server) knownDeviceKey() []byte {
	m := hmac.New(sha256.New, s.JWTSecret)
	m.Write([]byte(knownDeviceLabel))
	return m.Sum(nil)
}

func (s *Server) signKnownDevice(payload string) []byte {
	m := hmac.New(sha256.New, s.knownDeviceKey())
	m.Write([]byte(payload))
	return m.Sum(nil)
}

// knownDeviceValue builds "<payload>.<sig>" for username, issued now.
func (s *Server) knownDeviceValue(username string) string {
	payload := username + "|" + strconv.FormatInt(s.Now().Unix(), 10)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." +
		base64.RawURLEncoding.EncodeToString(s.signKnownDevice(payload))
}

// isKnownDevice reports whether r carries a genuine, unexpired known_device
// cookie issued for username. Anything else is simply false.
func (s *Server) isKnownDevice(r *http.Request, username string) bool {
	c, err := r.Cookie(knownDeviceCookie)
	if err != nil {
		return false
	}
	p, sig, ok := strings.Cut(c.Value, ".")
	if !ok {
		return false
	}
	payload, err1 := base64.RawURLEncoding.DecodeString(p)
	got, err2 := base64.RawURLEncoding.DecodeString(sig)
	if err1 != nil || err2 != nil || !hmac.Equal(got, s.signKnownDevice(string(payload))) {
		return false
	}
	name, ts, ok := strings.Cut(string(payload), "|")
	if !ok || name != username {
		return false
	}
	issued, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false
	}
	now := s.Now()
	t := time.Unix(issued, 0)
	return !t.After(now) && now.Before(t.Add(knownDeviceTTL))
}

func (s *Server) setKnownDeviceCookie(w http.ResponseWriter, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name: knownDeviceCookie, Value: value, Path: "/", MaxAge: maxAge,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.CookieSecure,
	})
}
