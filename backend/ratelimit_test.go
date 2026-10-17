package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time          { return c.t }
func (c *fakeClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

const (
	ipA = "198.51.100.7"
	ipB = "203.0.113.9"
)

// ---- limiter unit tests (no database) ----

func TestLimiterDelayGrowthAndWindow(t *testing.T) {
	clk := &fakeClock{t: t0}
	l := newLimiter(clk.Now)
	for i := 0; i < loginFreeFailures; i++ {
		if w := l.loginWait(ipA, "bob", false); w != 0 {
			t.Fatalf("failure %d: wait %v before the free failures are used up", i, w)
		}
		l.loginFailed(ipA, "bob", false)
	}
	// Each further failure doubles the wait: 1s, 2s, 4s, 8s ...
	want := loginBaseDelay
	for i := 0; i < 5; i++ {
		if w := l.loginWait(ipA, "bob", false); w != want {
			t.Fatalf("step %d: wait %v, want %v", i, w, want)
		}
		clk.Advance(want - time.Millisecond)
		if w := l.loginWait(ipA, "bob", false); w != time.Millisecond {
			t.Fatalf("step %d: remaining %v, want 1ms", i, w)
		}
		clk.Advance(time.Millisecond)
		if w := l.loginWait(ipA, "bob", false); w != 0 {
			t.Fatalf("step %d: still waiting %v after the delay", i, w)
		}
		l.loginFailed(ipA, "bob", false)
		want *= 2
	}
	// Capped.
	for i := 0; i < 40; i++ {
		l.loginFailed(ipA, "bob", false)
	}
	if w := l.loginWait(ipA, "bob", false); w != loginMaxDelay {
		t.Errorf("wait %v, want cap %v", w, loginMaxDelay)
	}
	// No lockout: after the window without a failure everything is forgotten.
	clk.Advance(limiterWindow)
	if w := l.loginWait(ipA, "bob", false); w != 0 {
		t.Errorf("wait %v after the window", w)
	}
	l.loginFailed(ipA, "bob", false)
	if l.size() != 2 { // pair + ip, old ones replaced
		t.Errorf("size = %d", l.size())
	}
}

func TestLimiterSuccessResetsPair(t *testing.T) {
	l := newLimiter((&fakeClock{t: t0}).Now)
	for i := 0; i < loginFreeFailures; i++ {
		l.loginFailed(ipA, "bob", false)
	}
	if l.loginWait(ipA, "bob", false) == 0 {
		t.Fatal("not throttled")
	}
	l.loginSucceeded(ipA, "bob")
	if w := l.loginWait(ipA, "bob", false); w != 0 {
		t.Errorf("wait %v after success", w)
	}
}

func TestLimiterPairsAreIndependent(t *testing.T) {
	l := newLimiter((&fakeClock{t: t0}).Now)
	for i := 0; i < 10; i++ {
		l.loginFailed(ipA, "victim", false)
	}
	if l.loginWait(ipA, "victim", false) == 0 {
		t.Fatal("attacker pair not throttled")
	}
	if l.loginWait(ipB, "victim", false) != 0 {
		t.Error("another IP is throttled for the same username")
	}
	if l.loginWait(ipA, "other", false) != 0 {
		t.Error("another username on the same IP is throttled by the pair counter")
	}
}

func TestLimiterIPCap(t *testing.T) {
	clk := &fakeClock{t: t0}
	l := newLimiter(clk.Now)
	for i := 0; i < loginIPMaxFailures; i++ {
		l.loginFailed(ipA, "user"+strconv.Itoa(i), false)
	}
	if w := l.loginWait(ipA, "fresh", false); w != 0 {
		t.Fatalf("throttled at exactly the cap: %v", w)
	}
	l.loginFailed(ipA, "last", false)
	if w := l.loginWait(ipA, "fresh", false); w != loginBaseDelay {
		t.Errorf("wait %v, want %v", w, loginBaseDelay)
	}
	if l.loginWait(ipB, "fresh", false) != 0 {
		t.Error("other IP affected")
	}
	if l.loginWait(ipA, "fresh", true) != 0 {
		t.Error("known device is bound by the IP cap")
	}
	clk.Advance(limiterWindow)
	if l.loginWait(ipA, "fresh", false) != 0 {
		t.Error("IP counter not forgotten after the window")
	}
}

func TestLimiterKnownTier(t *testing.T) {
	l := newLimiter((&fakeClock{t: t0}).Now)
	for i := 0; i < loginKnownFailures-1; i++ {
		l.loginFailed(ipA, "bob", true)
	}
	if l.loginWait(ipA, "bob", true) != 0 {
		t.Error("lenient tier throttled early")
	}
	if l.loginWait(ipA, "bob", false) == 0 {
		t.Error("strict tier not throttled")
	}
	l.loginFailed(ipA, "bob", true)
	if l.loginWait(ipA, "bob", true) == 0 {
		t.Error("lenient tier never throttles: a stolen cookie could guess forever")
	}
	if l.size() != 1 {
		t.Errorf("known-device failures touched the IP counter (size %d)", l.size())
	}
}

func TestLimiterRegister(t *testing.T) {
	clk := &fakeClock{t: t0}
	l := newLimiter(clk.Now)
	for i := 0; i < registerMaxPerIP; i++ {
		if w := l.registerAttempt(ipA); w != 0 {
			t.Fatalf("attempt %d throttled", i+1)
		}
		clk.Advance(time.Second)
	}
	w := l.registerAttempt(ipA)
	if want := limiterWindow - registerMaxPerIP*time.Second; w != want {
		t.Errorf("wait %v, want %v", w, want)
	}
	if l.registerAttempt(ipB) != 0 {
		t.Error("other IP throttled")
	}
	clk.Advance(w)
	if l.registerAttempt(ipA) != 0 {
		t.Error("still throttled after the window")
	}
}

func TestLimiterBoundedMemory(t *testing.T) {
	clk := &fakeClock{t: t0}
	l := newLimiter(clk.Now)
	for i := 0; i < limiterMaxEntries+500; i++ {
		l.loginFailed("10.0.0.1", "u"+strconv.Itoa(i), true) // one entry each
		if n := l.size(); n > limiterMaxEntries {
			t.Fatalf("size %d over the cap after %d inserts", n, i+1)
		}
	}
	if l.size() != limiterMaxEntries {
		t.Errorf("size = %d, want %d", l.size(), limiterMaxEntries)
	}
	// The oldest were evicted, the newest kept.
	if l.loginWait("10.0.0.1", "u"+strconv.Itoa(limiterMaxEntries+499), true) != 0 {
		t.Fatal("unexpected throttle")
	}
	l.mu.Lock()
	_, oldest := l.entries[pairKey("10.0.0.1", "u0")]
	_, newest := l.entries[pairKey("10.0.0.1", "u"+strconv.Itoa(limiterMaxEntries+499))]
	l.mu.Unlock()
	if oldest || !newest {
		t.Errorf("eviction order wrong: oldest kept=%v newest kept=%v", oldest, newest)
	}
	// Expired entries are dropped as new ones arrive.
	clk.Advance(limiterWindow)
	for i := 0; i < 100; i++ {
		l.loginFailed("10.0.0.2", "n"+strconv.Itoa(i), true)
	}
	if n := l.size(); n > limiterMaxEntries-limiterSweepBatch*50 {
		t.Errorf("expired entries are not swept: size %d", n)
	}
}

func TestLimiterConcurrent(t *testing.T) {
	l := newLimiter(time.Now)
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				user := "u" + strconv.Itoa(i%7)
				l.loginWait(ipA, user, g%2 == 0)
				l.loginFailed(ipA, user, g%2 == 0)
				l.registerAttempt(ipB)
				if i%50 == 0 {
					l.loginSucceeded(ipA, user)
				}
			}
		}()
	}
	wg.Wait()
	if l.size() > limiterMaxEntries {
		t.Error("over the cap")
	}
}

func TestClientIP(t *testing.T) {
	for addr, want := range map[string]string{
		"192.0.2.1:1234":   "192.0.2.1",
		"[2001:db8::1]:80": "2001:db8::1",
		"not an address":   "not an address",
		"":                 "",
	} {
		r := &http.Request{RemoteAddr: addr, Header: http.Header{"X-Forwarded-For": {"1.2.3.4"}, "X-Real-Ip": {"5.6.7.8"}, "Forwarded": {"for=9.9.9.9"}}}
		if got := clientIP(r); got != want {
			t.Errorf("clientIP(%q) = %q, want %q", addr, got, want)
		}
	}
}

// ---- known-device cookie unit tests (no database) ----

func cookieServer(secret string, clk *fakeClock) *Server {
	s := NewServer(nil)
	s.JWTSecret = []byte(secret)
	s.Now = clk.Now
	return s
}

func reqWithCookie(v string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	r.AddCookie(&http.Cookie{Name: knownDeviceCookie, Value: v})
	return r
}

func b64(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

func TestKnownDeviceCookie(t *testing.T) {
	clk := &fakeClock{t: t0}
	s := cookieServer("a-secret-of-at-least-32-bytes-long", clk)
	good := s.knownDeviceValue("bob")
	if !s.isKnownDevice(reqWithCookie(good), "bob") {
		t.Fatal("valid cookie rejected")
	}
	if s.isKnownDevice(httptest.NewRequest(http.MethodPost, "/", nil), "bob") {
		t.Error("no cookie accepted")
	}
	if s.isKnownDevice(reqWithCookie(good), "alice") {
		t.Error("cookie for another user accepted")
	}

	other := cookieServer("another-secret-of-32-bytes-or-more!", clk)
	if s.isKnownDevice(reqWithCookie(other.knownDeviceValue("bob")), "bob") {
		t.Error("cookie signed with another key accepted")
	}
	// Signed with the raw secret instead of the derived key: forged.
	payload := "bob|" + strconv.FormatInt(t0.Unix(), 10)
	m := hmac.New(sha256.New, s.JWTSecret)
	m.Write([]byte(payload))
	if s.isKnownDevice(reqWithCookie(b64(payload)+"."+base64.RawURLEncoding.EncodeToString(m.Sum(nil))), "bob") {
		t.Error("the key is the raw secret, not a derived one")
	}
	// Valid signature but edited payload, and malformed values.
	parts := strings.SplitN(good, ".", 2)
	for _, v := range []string{
		b64("alice|"+strconv.FormatInt(t0.Unix(), 10)) + "." + parts[1],
		parts[0], "." + parts[1], parts[0] + ".", "", ".", "%%%.%%%", "a.b.c", parts[0] + "." + parts[1] + "x",
	} {
		if s.isKnownDevice(reqWithCookie(v), "bob") {
			t.Errorf("accepted %q", v)
		}
	}
	// Expiry and issue time in the future.
	clk.Advance(knownDeviceTTL - time.Second)
	if !s.isKnownDevice(reqWithCookie(good), "bob") {
		t.Error("cookie expired early")
	}
	clk.Advance(time.Second)
	if s.isKnownDevice(reqWithCookie(good), "bob") {
		t.Error("expired cookie accepted")
	}
	clk.t = t0.Add(-time.Hour)
	if s.isKnownDevice(reqWithCookie(good), "bob") {
		t.Error("cookie from the future accepted")
	}
}

// ---- handler tests (database) ----

func attempt(s *Server, ip, path, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.RemoteAddr = ip + ":4000"
	for _, c := range cookies {
		if c != nil {
			req.AddCookie(c)
		}
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func loginAt(s *Server, ip, user, pass string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	return attempt(s, ip, "/auth/login", loginBody(user, pass), cookies...)
}

func cookieNamed(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func limitedServer(t *testing.T) (*Server, *fakeClock) {
	s := testServer(t)
	clk := &fakeClock{t: t0}
	s.Now = clk.Now
	return s, clk
}

// brute makes n wrong-password logins and returns the status of the last.
func brute(s *Server, ip, user string, n int) int {
	code := 0
	for i := 0; i < n; i++ {
		code = loginAt(s, ip, user, "wrong password").Code
	}
	return code
}

func wantTooMany(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	wantError(t, rec, http.StatusTooManyRequests, tooManyMessage)
	if n, err := strconv.Atoi(rec.Header().Get("Retry-After")); err != nil || n < 1 {
		t.Errorf("Retry-After = %q", rec.Header().Get("Retry-After"))
	}
}

func TestLoginThrottleShape(t *testing.T) {
	s, clk := limitedServer(t)
	name := uniqueName(t)
	newUser(t, s, name, "correct horse")
	for i := 0; i < loginFreeFailures; i++ {
		wantError(t, loginAt(s, ipA, name, "wrong password"), http.StatusUnauthorized, "invalid username or password")
	}
	rec := loginAt(s, ipA, name, "wrong password")
	wantTooMany(t, rec)
	if got := rec.Header().Get("Retry-After"); got != "1" {
		t.Errorf("Retry-After = %s, want 1", got)
	}
	// The right password is refused too while throttled, without a cookie.
	rec = loginAt(s, ipA, name, "correct horse")
	wantTooMany(t, rec)
	if cookieNamed(rec, "session") != nil || cookieNamed(rec, knownDeviceCookie) != nil {
		t.Error("throttled answer sets cookies")
	}
	clk.Advance(time.Second)
	wantError(t, loginAt(s, ipA, name, "wrong password"), http.StatusUnauthorized, "invalid username or password")
	// Retry-After grows with the fake clock: 2s, then 4s, then 8s.
	for _, want := range []string{"2", "4", "8"} {
		rec := loginAt(s, ipA, name, "wrong password")
		wantTooMany(t, rec)
		if got := rec.Header().Get("Retry-After"); got != want {
			t.Errorf("Retry-After = %s, want %s", got, want)
		}
		clk.Advance(time.Second) // 1s into the wait
		if got, _ := strconv.Atoi(loginAt(s, ipA, name, "wrong password").Header().Get("Retry-After")); got != atoi(want)-1 {
			t.Errorf("Retry-After after 1s = %d, want %d", got, atoi(want)-1)
		}
		clk.Advance(time.Duration(atoi(want)-1) * time.Second)
		if c := loginAt(s, ipA, name, "wrong password").Code; c != http.StatusUnauthorized {
			t.Fatalf("after the wait: %d", c)
		}
	}
	// Past the window the pair starts over, and success resets it.
	clk.Advance(limiterWindow)
	if rec := loginAt(s, ipA, name, "correct horse"); rec.Code != http.StatusOK {
		t.Errorf("after window: %d %s", rec.Code, rec.Body)
	}
	if brute(s, ipA, name, loginFreeFailures) != http.StatusUnauthorized {
		t.Error("success did not reset the counter")
	}
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

func TestLoginSuccessClearsCounter(t *testing.T) {
	s, _ := limitedServer(t)
	name := uniqueName(t)
	newUser(t, s, name, "correct horse")
	brute(s, ipA, name, loginFreeFailures-1)
	if rec := loginAt(s, ipA, name, "correct horse"); rec.Code != http.StatusOK {
		t.Fatalf("login: %d", rec.Code)
	}
	if c := brute(s, ipA, name, loginFreeFailures); c != http.StatusUnauthorized {
		t.Errorf("counter survived a successful login: %d", c)
	}
}

func TestAttackerOnAnotherIPDoesNotBlockVictim(t *testing.T) {
	s, _ := limitedServer(t)
	name := uniqueName(t)
	newUser(t, s, name, "correct horse")
	if c := brute(s, ipA, name, 12); c != http.StatusTooManyRequests {
		t.Fatalf("attacker not throttled: %d", c)
	}
	rec := loginAt(s, ipB, name, "correct horse")
	if rec.Code != http.StatusOK {
		t.Errorf("real user from another IP: %d %s", rec.Code, rec.Body)
	}
}

func TestKnownDeviceKeepsRealUserWorking(t *testing.T) {
	s, clk := limitedServer(t)
	name := uniqueName(t)
	newUser(t, s, name, "correct horse")
	login := loginAt(s, ipA, name, "correct horse")
	known := cookieNamed(login, knownDeviceCookie)
	if known == nil {
		t.Fatal("login did not set known_device")
	}
	// The bot hits the same IP (shared NAT) and the pair is throttled.
	if c := brute(s, ipA, name, 8); c != http.StatusTooManyRequests {
		t.Fatalf("not throttled: %d", c)
	}
	if c := loginAt(s, ipA, name, "correct horse").Code; c != http.StatusTooManyRequests {
		t.Fatalf("no-cookie request not throttled: %d", c)
	}
	if rec := loginAt(s, ipA, name, "correct horse", known); rec.Code != http.StatusOK {
		t.Errorf("known device blocked: %d %s", rec.Code, rec.Body)
	}
	// The IP-wide cap does not stop a known device either.
	for i := 0; i < loginIPMaxFailures+2; i++ {
		loginAt(s, ipA, "nobody-"+strconv.Itoa(i), "wrong password")
	}
	if c := loginAt(s, ipA, "someone-new", "wrong password").Code; c != http.StatusTooManyRequests {
		t.Fatalf("IP cap not reached: %d", c)
	}
	if rec := loginAt(s, ipA, name, "correct horse", known); rec.Code != http.StatusOK {
		t.Errorf("known device blocked by the IP cap: %d", rec.Code)
	}
	// A stolen cookie still counts failures: not unlimited guessing.
	s2, _ := limitedServer(t)
	s2.JWTSecret = s.JWTSecret
	got := 0
	for i := 0; i < loginKnownFailures+5; i++ {
		if loginAt(s2, ipB, name, "wrong password", known).Code == http.StatusTooManyRequests {
			got = i
			break
		}
	}
	if got != loginKnownFailures {
		t.Errorf("lenient tier throttled after %d failures, want %d", got, loginKnownFailures)
	}
	_ = clk
}

func TestForgedForeignAndExpiredCookiesGetNoLeniency(t *testing.T) {
	s, clk := limitedServer(t)
	name, other := uniqueName(t), uniqueName(t)
	newUser(t, s, name, "correct horse")
	newUser(t, s, other, "correct horse")
	valid := cookieNamed(loginAt(s, ipB, name, "correct horse"), knownDeviceCookie)
	foreign := cookieNamed(loginAt(s, ipB, other, "correct horse"), knownDeviceCookie)
	forgedSrv := cookieServer("a-different-secret-of-32-bytes-ok!", clk)
	forged := &http.Cookie{Name: knownDeviceCookie, Value: forgedSrv.knownDeviceValue(name)}
	tampered := &http.Cookie{Name: knownDeviceCookie, Value: valid.Value[:len(valid.Value)-2] + "AA"}
	garbage := &http.Cookie{Name: knownDeviceCookie, Value: "garbage"}

	brute(s, ipA, name, 8)
	for label, c := range map[string]*http.Cookie{
		"forged": forged, "tampered": tampered, "garbage": garbage, "other user": foreign,
	} {
		wantTooMany(t, loginAt(s, ipA, name, "correct horse", c))
		_ = label
	}
	// Expired: the real cookie after 3 hours.
	clk.Advance(knownDeviceTTL)
	brute(s, ipA, name, 8)
	wantTooMany(t, loginAt(s, ipA, name, "correct horse", valid))
}

func TestUnknownAndKnownUsersAreIndistinguishable(t *testing.T) {
	s, clk := limitedServer(t)
	real, ghost := uniqueName(t), uniqueName(t)
	newUser(t, s, real, "correct horse")
	run := func(user string) []string {
		var seq []string
		for i := 0; i < 14; i++ {
			rec := loginAt(s, ipA, user, "wrong password")
			seq = append(seq, strconv.Itoa(rec.Code)+" "+rec.Body.String()+" "+rec.Header().Get("Retry-After")+
				" "+rec.Header().Get("Content-Type"))
			if i%3 == 2 {
				clk.Advance(2 * time.Second)
			}
		}
		return seq
	}
	a, b := run(real), run(ghost)
	for i := range a {
		if a[i] != b[i] {
			t.Errorf("attempt %d differs:\n known:   %s\n unknown: %s", i+1, a[i], b[i])
		}
	}
	if !strings.HasPrefix(a[0], "401 ") || !strings.HasPrefix(a[len(a)-1], "429 ") {
		t.Errorf("sequence did not cover both 401 and 429: %v", a)
	}
}

func TestPerIPCapAcrossUsernames(t *testing.T) {
	s, _ := limitedServer(t)
	victim := uniqueName(t)
	newUser(t, s, victim, "correct horse")
	for i := 0; i < loginIPMaxFailures+1; i++ {
		wantError(t, loginAt(s, ipA, "spray-"+strconv.Itoa(i), "wrong password"), http.StatusUnauthorized, "invalid username or password")
	}
	wantTooMany(t, loginAt(s, ipA, "spray-new", "wrong password"))
	wantTooMany(t, loginAt(s, ipA, victim, "correct horse"))
	if c := loginAt(s, ipB, "spray-new", "wrong password").Code; c != http.StatusUnauthorized {
		t.Errorf("other IP affected: %d", c)
	}
}

func TestForwardedHeadersAreIgnored(t *testing.T) {
	s, _ := limitedServer(t)
	name := uniqueName(t)
	newUser(t, s, name, "correct horse")
	var last *httptest.ResponseRecorder
	for i := 0; i < loginFreeFailures+3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(loginBody(name, "wrong password")))
		req.RemoteAddr = ipA + ":4000"
		req.Header.Set("X-Forwarded-For", "10.9.8."+strconv.Itoa(i))
		req.Header.Set("X-Real-IP", "10.7.7."+strconv.Itoa(i))
		req.Header.Set("Forwarded", "for=10.6.6."+strconv.Itoa(i))
		last = httptest.NewRecorder()
		s.Handler().ServeHTTP(last, req)
	}
	wantTooMany(t, last)
}

func TestLoginCounterKeyIsCaseInsensitiveAndSkipsBadBodies(t *testing.T) {
	s, _ := limitedServer(t)
	name := uniqueName(t)
	newUser(t, s, name, "correct horse")
	for i := 0; i < 20; i++ {
		attempt(s, ipA, "/auth/login", `{"username":"","password":""}`)
		attempt(s, ipA, "/auth/login", `{not json`)
	}
	for i := 0; i < loginFreeFailures; i++ {
		u := name
		if i%2 == 1 {
			u = strings.ToUpper(name)
		}
		wantError(t, loginAt(s, ipA, u, "wrong password"), http.StatusUnauthorized, "invalid username or password")
	}
	wantTooMany(t, loginAt(s, ipA, strings.ToUpper(name), "wrong password"))
}

func TestRegisterLimitPerIP(t *testing.T) {
	s, clk := limitedServer(t)
	for i := 0; i < registerMaxPerIP; i++ {
		// Failed attempts count too (bad body, then a taken name).
		if c := attempt(s, ipA, "/auth/register", `{"username":"x"}`).Code; c != http.StatusBadRequest {
			t.Fatalf("attempt %d: %d", i+1, c)
		}
	}
	rec := attempt(s, ipA, "/auth/register", regBody(uniqueName(t), "correct horse"))
	wantTooMany(t, rec)
	if got := rec.Header().Get("Retry-After"); got != strconv.Itoa(int(limiterWindow/time.Second)) {
		t.Errorf("Retry-After = %s", got)
	}
	name := uniqueName(t)
	cleanupUser(t, s.DB, name)
	if c := attempt(s, ipB, "/auth/register", regBody(name, "correct horse")).Code; c != http.StatusCreated {
		t.Errorf("other IP: %d", c)
	}
	clk.Advance(limiterWindow)
	if c := attempt(s, ipA, "/auth/register", `{"username":"x"}`).Code; c != http.StatusBadRequest {
		t.Errorf("after the window: %d", c)
	}
}

func TestKnownDeviceCookieAttributesAndLogout(t *testing.T) {
	s, _ := limitedServer(t)
	s.CookieSecure = true
	name := uniqueName(t)
	newUser(t, s, name, "correct horse")
	rec := loginAt(s, ipA, name, "correct horse")
	c := cookieNamed(rec, knownDeviceCookie)
	if c == nil || !c.HttpOnly || !c.Secure || c.Path != "/" || c.SameSite != http.SameSiteLaxMode ||
		c.MaxAge != int(sessionTTL/time.Second) {
		t.Fatalf("known_device cookie = %+v", c)
	}
	if strings.Contains(c.Value, string(s.JWTSecret)) {
		t.Error("cookie leaks the secret")
	}
	var u userResponse
	if json.Unmarshal(rec.Body.Bytes(), &u) != nil || strings.Contains(rec.Body.String(), c.Value) {
		t.Error("body")
	}
	rec = attempt(s, ipA, "/auth/logout", "")
	if c := cookieNamed(rec, knownDeviceCookie); c == nil || c.MaxAge != -1 || c.Value != "" || !c.Secure {
		t.Errorf("logout known_device cookie = %+v", c)
	}
	s.CookieSecure = false
	if c := cookieNamed(loginAt(s, ipA, name, "correct horse"), knownDeviceCookie); c == nil || c.Secure {
		t.Errorf("Secure without COOKIE_SECURE: %+v", c)
	}
}
