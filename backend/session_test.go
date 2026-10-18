package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var t0 = time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)

// newUser registers a user through the API and returns its id.
func newUser(t *testing.T, s *Server, name, password string) int64 {
	t.Helper()
	cleanupUser(t, s, name)
	rec := post(s, regBody(name, password))
	if rec.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", rec.Code, rec.Body)
	}
	var u userResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &u)
	return u.ID
}

func do(s *Server, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func sessionCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == "session" {
			return c
		}
	}
	t.Fatalf("no session cookie in %v", rec.Header()["Set-Cookie"])
	return nil
}

func signToken(t *testing.T, method jwt.SigningMethod, key any, sub string, exp time.Time) *http.Cookie {
	t.Helper()
	tok, err := jwt.NewWithClaims(method, jwt.RegisteredClaims{
		Subject: sub, ExpiresAt: jwt.NewNumericDate(exp),
	}).SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: "session", Value: tok}
}

func loginBody(u, p string) string { return regBody(u, p) }

func TestLoginSuccess(t *testing.T) {
	s := testServer(t)
	s.Now = func() time.Time { return t0 }
	name := uniqueName(t)
	id := newUser(t, s, name, "correct horse")

	rec := do(s, http.MethodPost, "/auth/login", loginBody(name, "correct horse"), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s", rec.Code, rec.Body)
	}
	want := `{"id":` + strconv.FormatInt(id, 10) + `,"username":"` + name + `"}`
	if rec.Body.String() != want {
		t.Errorf("body = %s, want %s", rec.Body, want)
	}
	c := sessionCookie(t, rec)
	if strings.Contains(rec.Body.String(), c.Value) {
		t.Error("token in body")
	}
	if !c.HttpOnly || c.Path != "/" || c.MaxAge != 10800 || c.SameSite != http.SameSiteLaxMode || c.Secure {
		t.Errorf("cookie = %+v", c)
	}
	raw := rec.Header().Get("Set-Cookie")
	if !strings.Contains(raw, "Max-Age=10800") {
		t.Errorf("Set-Cookie lacks Max-Age=10800: %s", raw)
	}
	if strings.Contains(raw, "Secure") {
		t.Errorf("Secure present: %s", raw)
	}

	var claims jwt.RegisteredClaims
	_, err := jwt.ParseWithClaims(c.Value, &claims, func(*jwt.Token) (any, error) { return s.JWTSecret, nil },
		jwt.WithTimeFunc(s.Now), jwt.WithValidMethods([]string{"HS256"}))
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != strconv.FormatInt(id, 10) {
		t.Errorf("sub = %q", claims.Subject)
	}
	if !claims.ExpiresAt.Time.Equal(t0.Add(3 * time.Hour)) {
		t.Errorf("exp = %v", claims.ExpiresAt)
	}
}

func TestLoginCookieSecure(t *testing.T) {
	s := testServer(t)
	s.CookieSecure = true
	name := uniqueName(t)
	newUser(t, s, name, "correct horse")
	rec := do(s, http.MethodPost, "/auth/login", loginBody(name, "correct horse"), nil)
	if !sessionCookie(t, rec).Secure || !strings.Contains(rec.Header().Get("Set-Cookie"), "Secure") {
		t.Errorf("Set-Cookie = %s", rec.Header().Get("Set-Cookie"))
	}
	rec = do(s, http.MethodPost, "/auth/logout", "", nil)
	if !sessionCookie(t, rec).Secure {
		t.Error("logout cookie not Secure")
	}
}

func TestLoginLowercases(t *testing.T) {
	s := testServer(t)
	name := uniqueName(t)
	newUser(t, s, name, "correct horse")
	rec := do(s, http.MethodPost, "/auth/login", loginBody(strings.ToUpper(name), "correct horse"), nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"username":"`+name+`"`) {
		t.Errorf("status %d body %s", rec.Code, rec.Body)
	}
}

func TestLoginRejected(t *testing.T) {
	s := testServer(t)
	name := uniqueName(t)
	newUser(t, s, name, "correct horse")
	tests := []struct {
		label, body string
		status      int
		msg         string
	}{
		{"wrong password", loginBody(name, "wrong horse!"), 401, "invalid username or password"},
		{"unknown user", loginBody(name+"-x", "correct horse"), 401, "invalid username or password"},
		{"empty username", loginBody("", "correct horse"), 400, "username and password are required"},
		{"empty password", loginBody(name, ""), 400, "username and password are required"},
		{"missing fields", `{}`, 400, "username and password are required"},
		{"malformed JSON", `{"username":`, 400, "invalid JSON body"},
		{"wrong type", `{"username":1,"password":"x"}`, 400, "invalid JSON body"},
		{"empty body", ``, 400, "invalid JSON body"},
	}
	for _, tc := range tests {
		t.Run(tc.label, func(t *testing.T) {
			rec := do(s, http.MethodPost, "/auth/login", tc.body, nil)
			wantError(t, rec, tc.status, tc.msg)
			if c := rec.Header().Values("Set-Cookie"); len(c) != 0 {
				t.Errorf("Set-Cookie %v", c)
			}
		})
	}
}

func TestLoginWrongMethod(t *testing.T) {
	s := testServer(t)
	wantError(t, do(s, http.MethodGet, "/auth/login", "", nil), 405, "method not allowed")
}

func TestLogout(t *testing.T) {
	s := testServer(t)
	name := uniqueName(t)
	newUser(t, s, name, "correct horse")
	login := do(s, http.MethodPost, "/auth/login", loginBody(name, "correct horse"), nil)

	for _, tc := range []struct {
		label  string
		cookie *http.Cookie
	}{{"with cookie", sessionCookie(t, login)}, {"without cookie", nil}} {
		t.Run(tc.label, func(t *testing.T) {
			rec := do(s, http.MethodPost, "/auth/logout", "", tc.cookie)
			if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
				t.Errorf("status %d body %q", rec.Code, rec.Body)
			}
			c := sessionCookie(t, rec)
			if c.MaxAge != -1 || c.Value != "" || !strings.Contains(rec.Header().Get("Set-Cookie"), "Max-Age=0") {
				t.Errorf("cookie = %s", rec.Header().Get("Set-Cookie"))
			}
		})
	}
}

func TestMe(t *testing.T) {
	s := testServer(t)
	name := uniqueName(t)
	id := newUser(t, s, name, "correct horse")
	cookie := sessionCookie(t, do(s, http.MethodPost, "/auth/login", loginBody(name, "correct horse"), nil))

	rec := do(s, http.MethodGet, "/auth/me", "", cookie)
	want := `{"id":` + strconv.FormatInt(id, 10) + `,"username":"` + name + `"}`
	if rec.Code != http.StatusOK || rec.Body.String() != want {
		t.Errorf("status %d body %s, want %s", rec.Code, rec.Body, want)
	}
}

func TestMeUnauthorized(t *testing.T) {
	s := testServer(t)
	s.Now = func() time.Time { return t0 }
	name := uniqueName(t)
	id := newUser(t, s, name, "correct horse")
	sub := strconv.FormatInt(id, 10)
	exp := t0.Add(time.Hour)

	valid := signToken(t, jwt.SigningMethodHS256, s.JWTSecret, sub, exp)
	if rec := do(s, http.MethodGet, "/auth/me", "", valid); rec.Code != 200 {
		t.Fatalf("control token rejected: %d", rec.Code)
	}
	noExp, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{Subject: sub}).SignedString(s.JWTSecret)
	none := signToken(t, jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, sub, exp)
	tampered := &http.Cookie{Name: "session", Value: valid.Value[:len(valid.Value)-2] + "xx"}

	tests := []struct {
		label  string
		cookie *http.Cookie
	}{
		{"no cookie", nil},
		{"empty cookie", &http.Cookie{Name: "session", Value: ""}},
		{"garbage token", &http.Cookie{Name: "session", Value: "not.a.jwt"}},
		{"other secret", signToken(t, jwt.SigningMethodHS256, []byte("other"), sub, exp)},
		{"expired", signToken(t, jwt.SigningMethodHS256, s.JWTSecret, sub, t0.Add(-time.Second))},
		{"alg none", none},
		{"HS512", signToken(t, jwt.SigningMethodHS512, s.JWTSecret, sub, exp)},
		{"tampered", tampered},
		{"no exp", &http.Cookie{Name: "session", Value: noExp}},
		{"non-numeric sub", signToken(t, jwt.SigningMethodHS256, s.JWTSecret, "abc", exp)},
		{"missing user", signToken(t, jwt.SigningMethodHS256, s.JWTSecret, "9223372036854775807", exp)},
	}
	for _, tc := range tests {
		t.Run(tc.label, func(t *testing.T) {
			wantError(t, do(s, http.MethodGet, "/auth/me", "", tc.cookie), 401, "authentication required")
		})
	}
}

func TestMeDeletedUser(t *testing.T) {
	s := testServer(t)
	name := uniqueName(t)
	newUser(t, s, name, "correct horse")
	cookie := sessionCookie(t, do(s, http.MethodPost, "/auth/login", loginBody(name, "correct horse"), nil))
	deleteUser(t, s, name)
	wantError(t, do(s, http.MethodGet, "/auth/me", "", cookie), 401, "authentication required")
}

func TestMeExpiresWithClock(t *testing.T) {
	s := testServer(t)
	now := t0
	s.Now = func() time.Time { return now }
	name := uniqueName(t)
	newUser(t, s, name, "correct horse")
	cookie := sessionCookie(t, do(s, http.MethodPost, "/auth/login", loginBody(name, "correct horse"), nil))

	now = t0.Add(3*time.Hour - time.Second)
	if rec := do(s, http.MethodGet, "/auth/me", "", cookie); rec.Code != 200 {
		t.Errorf("just before expiry: %d", rec.Code)
	}
	now = t0.Add(3*time.Hour + time.Second)
	wantError(t, do(s, http.MethodGet, "/auth/me", "", cookie), 401, "authentication required")
	// an owner action with the expired token is refused too
	wantError(t, do(s, http.MethodPost, "/repos", `{"name":"`+name+`"}`, cookie), 401, "authentication required")
}

func TestRequireAndOptionalAuth(t *testing.T) {
	s := testServer(t)
	name := uniqueName(t)
	id := newUser(t, s, name, "correct horse")
	cookie := sessionCookie(t, do(s, http.MethodPost, "/auth/login", loginBody(name, "correct horse"), nil))

	ran := 0
	var got User
	var ok bool
	h := func(w http.ResponseWriter, r *http.Request) {
		ran++
		got, ok = UserFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}
	serve := func(h http.HandlerFunc, c *http.Cookie) int {
		ran, got, ok = 0, User{}, false
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		if c != nil {
			req.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		h(rec, req)
		return rec.Code
	}

	if code := serve(s.requireAuth(h), nil); code != 401 || ran != 0 {
		t.Errorf("require, no cookie: code %d ran %d", code, ran)
	}
	if code := serve(s.requireAuth(h), cookie); code != 204 || ran != 1 || !ok || got != (User{ID: id, Username: name}) {
		t.Errorf("require, cookie: code %d ran %d user %+v ok %v", code, ran, got, ok)
	}
	if code := serve(s.optionalAuth(h), nil); code != 204 || ran != 1 || ok {
		t.Errorf("optional, no cookie: code %d ran %d ok %v", code, ran, ok)
	}
	bad := &http.Cookie{Name: "session", Value: "garbage"}
	if code := serve(s.optionalAuth(h), bad); code != 204 || ran != 1 || ok {
		t.Errorf("optional, bad cookie: code %d ran %d ok %v", code, ran, ok)
	}
	if code := serve(s.optionalAuth(h), cookie); code != 204 || !ok || got.ID != id {
		t.Errorf("optional, cookie: code %d user %+v ok %v", code, got, ok)
	}
}

// A username longer than any real one is answered like an unknown user and
// never reaches the limiter (its key would hold the whole name) or the DB.
func TestLoginOversizedUsername(t *testing.T) {
	s, _ := limitedServer(t)
	const ip = "203.0.113.63"
	longest := pairKey(ip, strings.Repeat("a", maxUsernameLen), false)
	tests := []struct {
		label, user string
		counted     bool // the failed login reaches the limiter
	}{
		{"1 MiB", strings.Repeat("a", 1<<20-100), false},
		{"33 bytes", strings.Repeat("a", maxUsernameLen+1), false},
		{"33 bytes after lowercasing", strings.Repeat("A", maxUsernameLen+1), false},
		{"32 bytes", strings.Repeat("a", maxUsernameLen), true},
	}
	for _, tc := range tests {
		t.Run(tc.label, func(t *testing.T) {
			before := s.limiter.size()
			rec := loginAt(s, ip, tc.user, "whatever password")
			wantError(t, rec, 401, "invalid username or password")
			if got := s.limiter.size() > before; got != tc.counted {
				t.Errorf("limiter grew = %v, want %v", got, tc.counted)
			}
			s.limiter.mu.Lock()
			defer s.limiter.mu.Unlock()
			for k := range s.limiter.entries {
				if len(k) > len(longest) {
					t.Fatalf("limiter key of %d bytes, max %d", len(k), len(longest))
				}
			}
		})
	}
}
