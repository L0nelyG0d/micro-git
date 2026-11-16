package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientAddr(t *testing.T) {
	const ra = "10.0.0.5:4000"
	long := strings.Repeat("1.1.1.1, ", 40) + "9.9.9.9"
	pad := "9.9.9.9" + strings.Repeat(" ", 5000)
	many := strings.TrimSuffix(strings.Repeat("1.1.1.1,", 33), ",")
	for _, c := range []struct {
		name string
		xff  []string
		hops int
		want string
	}{
		{"hops 0 ignores header", []string{"9.9.9.9"}, 0, "10.0.0.5"},
		{"hops 1 one entry", []string{"9.9.9.9"}, 1, "9.9.9.9"},
		{"hops 1 spoofed left part", []string{"1.2.3.4, 9.9.9.9"}, 1, "9.9.9.9"},
		{"hops 1 several lines", []string{"1.2.3.4", "5.5.5.5, 9.9.9.9"}, 1, "9.9.9.9"},
		{"hops 1 spaces", []string{"  9.9.9.9  "}, 1, "9.9.9.9"},
		{"hops 1 ipv6", []string{"2001:db8::7"}, 1, "2001:db8::7"},
		{"hops 1 bracketed ipv6 port", []string{"[2001:db8::7]:5555"}, 1, "2001:db8::7"},
		{"hops 1 bracketed ipv6", []string{"[2001:db8::7]"}, 1, "2001:db8::7"},
		{"hops 1 ipv4 port", []string{"9.9.9.9:1234"}, 1, "9.9.9.9"},
		{"hops 1 ipv4-mapped", []string{"::ffff:9.9.9.9"}, 1, "9.9.9.9"},
		{"hops 2", []string{"1.2.3.4, 8.8.8.8, 9.9.9.9"}, 2, "8.8.8.8"},
		{"hops 2 across lines", []string{"1.2.3.4, 8.8.8.8", "9.9.9.9"}, 2, "8.8.8.8"},
		{"fewer entries than hops", []string{"9.9.9.9"}, 2, "10.0.0.5"},
		{"no header", nil, 1, "10.0.0.5"},
		{"empty header", []string{""}, 1, "10.0.0.5"},
		{"trailing empty entry", []string{"9.9.9.9,"}, 1, "10.0.0.5"},
		{"garbage entry", []string{"1.2.3.4, evil"}, 1, "10.0.0.5"},
		{"garbage left is ignored", []string{"evil, 9.9.9.9"}, 1, "9.9.9.9"},
		{"zone rejected", []string{"fe80::1%eth0"}, 1, "10.0.0.5"},
		{"bad port rejected", []string{"9.9.9.9:abc"}, 1, "10.0.0.5"},
		{"unclosed bracket", []string{"[2001:db8::7"}, 1, "10.0.0.5"},
		{"bytes cap", []string{pad}, 1, "10.0.0.5"},
		{"entries at cap ok", []string{strings.Repeat("1.1.1.1,", 31) + "9.9.9.9"}, 1, "9.9.9.9"},
		{"too many entries", []string{many}, 1, "10.0.0.5"},
		{"41 entries", []string{long}, 1, "10.0.0.5"},
	} {
		if got := clientAddr(ra, c.xff, c.hops); got != c.want {
			t.Errorf("%s: clientAddr(%q, %d) = %q, want %q", c.name, c.xff, c.hops, got, c.want)
		}
	}
	if got := clientAddr("[2001:db8::1]:80", nil, 1); got != "2001:db8::1" {
		t.Errorf("ipv6 RemoteAddr fallback = %q", got)
	}
}

func TestClientAddrNoAllocs(t *testing.T) {
	xff := []string{"1.2.3.4, 9.9.9.9"}
	if n := testing.AllocsPerRun(100, func() { clientAddr("10.0.0.5:1", xff, 1) }); n > 1 {
		t.Errorf("allocs = %v", n)
	}
}

func TestLoadConfigTrustedProxyHops(t *testing.T) {
	load := func(v string) Config {
		cfg, err := LoadConfig(func(k string) string {
			switch k {
			case "STORE":
				return "memory"
			case "JWT_SECRET":
				return strings.Repeat("x", 32)
			case "TRUSTED_PROXY_HOPS":
				return v
			}
			return ""
		})
		if err != nil {
			t.Fatalf("%q: %v", v, err)
		}
		return cfg
	}
	hasWarn := func(c Config) bool {
		for _, w := range c.Warnings {
			if strings.Contains(w, "TRUSTED_PROXY_HOPS") {
				return true
			}
		}
		return false
	}
	for _, c := range []struct {
		in   string
		want int
		warn bool
	}{
		{"", 0, false}, {"0", 0, false}, {"1", 1, false}, {"5", 5, false},
		{"-1", 0, true}, {"6", 0, true}, {"two", 0, true}, {"1.5", 0, true}, {" 1", 0, true},
	} {
		cfg := load(c.in)
		if cfg.TrustedProxyHops != c.want || hasWarn(cfg) != c.warn {
			t.Errorf("%q: hops=%d warn=%v, want %d %v", c.in, cfg.TrustedProxyHops, hasWarn(cfg), c.want, c.warn)
		}
	}
}

// fwd sends a request from RemoteAddr proxy with the given X-Forwarded-For.
func fwd(s *Server, proxy, xff, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.RemoteAddr = proxy + ":4000"
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func TestRegisterBudgetPerForwardedClient(t *testing.T) {
	s, _ := limitedServer(t)
	s.TrustedProxyHops = 1
	bad := `{"username":"x"}`
	for i := 0; i < registerMaxPerIP; i++ {
		// The left part changes every time: it must not buy a fresh budget.
		xff := "7.7.7." + string(rune('0'+i)) + ", 198.51.100.1"
		if c := fwd(s, ipA, xff, "/auth/register", bad).Code; c != http.StatusBadRequest {
			t.Fatalf("attempt %d: %d", i+1, c)
		}
	}
	wantTooMany(t, fwd(s, ipA, "6.6.6.6, 198.51.100.1", "/auth/register", bad))
	// Another client behind the same proxy has its own budget.
	if c := fwd(s, ipA, "198.51.100.2", "/auth/register", bad).Code; c != http.StatusBadRequest {
		t.Errorf("second client: %d", c)
	}
	// No header: counted against the proxy bucket, separate from both.
	if c := fwd(s, ipA, "", "/auth/register", bad).Code; c != http.StatusBadRequest {
		t.Errorf("proxy bucket: %d", c)
	}
}

func TestLoginBudgetPerForwardedClient(t *testing.T) {
	s, _ := limitedServer(t)
	s.TrustedProxyHops = 1
	user := uniqueName(t)
	for i := 0; i < loginFreeFailures; i++ {
		xff := "7.7.7." + string(rune('0'+i)) + ", 198.51.100.1"
		if c := fwd(s, ipA, xff, "/auth/login", loginBody(user, "wrong password")).Code; c != http.StatusUnauthorized {
			t.Fatalf("failure %d: %d", i+1, c)
		}
	}
	wantTooMany(t, fwd(s, ipA, "5.5.5.5, 198.51.100.1", "/auth/login", loginBody(user, "wrong password")))
	if c := fwd(s, ipA, "198.51.100.2", "/auth/login", loginBody(user, "wrong password")).Code; c != http.StatusUnauthorized {
		t.Errorf("second client: %d", c)
	}
}

func TestHopsZeroIgnoresForwardedHeader(t *testing.T) {
	s, _ := limitedServer(t)
	bad := `{"username":"x"}`
	for i := 0; i < registerMaxPerIP; i++ {
		fwd(s, ipA, "9.9.9."+string(rune('0'+i)), "/auth/register", bad)
	}
	wantTooMany(t, fwd(s, ipA, "8.8.8.8", "/auth/register", bad))
}
