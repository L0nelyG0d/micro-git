package main

import (
	"bytes"
	"compress/gzip"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func serve(h http.Handler, method, path string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func gunzip(t *testing.T, b []byte) string {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	return string(out)
}

// jsonHandler answers with a JSON body of n bytes and the given status.
func jsonHandler(status, n int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"abc"`)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"x":"` + strings.Repeat("a", n) + `"}`))
	})
}

func TestGzipJSON(t *testing.T) {
	gz := map[string]string{"Accept-Encoding": "gzip"}
	t.Run("large body round trip", func(t *testing.T) {
		rec := serve(gzipJSON(jsonHandler(200, 5000)), "GET", "/", gz)
		if rec.Header().Get("Content-Encoding") != "gzip" || rec.Header().Get("Vary") != "Accept-Encoding" {
			t.Fatalf("headers = %v", rec.Header())
		}
		if got := gunzip(t, rec.Body.Bytes()); got != `{"x":"`+strings.Repeat("a", 5000)+`"}` {
			t.Errorf("round trip mismatch (%d bytes)", len(got))
		}
		if rec.Header().Get("ETag") != `W/"abc"` {
			t.Errorf("etag = %q, want weakened", rec.Header().Get("ETag"))
		}
		if rec.Header().Get("Content-Length") == "" {
			t.Error("no Content-Length")
		}
	})
	t.Run("error status is compressed too", func(t *testing.T) {
		rec := serve(gzipJSON(jsonHandler(404, 3000)), "GET", "/", gz)
		if rec.Code != 404 || rec.Header().Get("Content-Encoding") != "gzip" {
			t.Errorf("code %d headers %v", rec.Code, rec.Header())
		}
	})
	t.Run("tiny body not compressed but Vary set", func(t *testing.T) {
		rec := serve(gzipJSON(jsonHandler(200, 10)), "GET", "/", gz)
		if rec.Header().Get("Content-Encoding") != "" || !strings.HasPrefix(rec.Body.String(), `{"x"`) {
			t.Errorf("tiny body was encoded: %v", rec.Header())
		}
		if rec.Header().Get("Vary") != "Accept-Encoding" {
			t.Errorf("Vary = %q", rec.Header().Get("Vary"))
		}
	})
	t.Run("exactly 1 KB not compressed", func(t *testing.T) {
		rec := serve(gzipJSON(jsonHandler(200, gzipMinSize-8)), "GET", "/", gz)
		if rec.Body.Len() != gzipMinSize || rec.Header().Get("Content-Encoding") != "" {
			t.Errorf("len %d enc %q", rec.Body.Len(), rec.Header().Get("Content-Encoding"))
		}
	})
	t.Run("HEAD not compressed", func(t *testing.T) {
		rec := serve(gzipJSON(jsonHandler(200, 5000)), "HEAD", "/", gz)
		if rec.Header().Get("Content-Encoding") != "" {
			t.Errorf("HEAD encoded: %v", rec.Header())
		}
	})
	t.Run("client without gzip", func(t *testing.T) {
		for _, ae := range []string{"", "identity", "gzip;q=0", "br"} {
			rec := serve(gzipJSON(jsonHandler(200, 5000)), "GET", "/", map[string]string{"Accept-Encoding": ae})
			if rec.Header().Get("Content-Encoding") != "" || rec.Body.Len() < 5000 {
				t.Errorf("Accept-Encoding %q: encoded %v", ae, rec.Header())
			}
			if rec.Header().Get("Vary") != "Accept-Encoding" {
				t.Errorf("Accept-Encoding %q: Vary missing", ae)
			}
		}
	})
	t.Run("accepts gzip among others", func(t *testing.T) {
		rec := serve(gzipJSON(jsonHandler(200, 5000)), "GET", "/", map[string]string{"Accept-Encoding": "br, gzip;q=0.8"})
		if rec.Header().Get("Content-Encoding") != "gzip" {
			t.Error("not compressed")
		}
	})
	t.Run("already encoded", func(t *testing.T) {
		h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Encoding", "br")
			_, _ = w.Write(bytes.Repeat([]byte("a"), 5000))
		})
		rec := serve(gzipJSON(h), "GET", "/", gz)
		if rec.Header().Get("Content-Encoding") != "br" || rec.Body.Len() != 5000 {
			t.Errorf("double encoded: %v", rec.Header())
		}
	})
	t.Run("304 passes through", func(t *testing.T) {
		h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotModified) })
		rec := serve(gzipJSON(h), "GET", "/", gz)
		if rec.Code != 304 || rec.Body.Len() != 0 || rec.Header().Get("Content-Encoding") != "" {
			t.Errorf("code %d body %d", rec.Code, rec.Body.Len())
		}
	})
	t.Run("non JSON not compressed", func(t *testing.T) {
		h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write(bytes.Repeat([]byte("a"), 5000))
		})
		if rec := serve(gzipJSON(h), "GET", "/", gz); rec.Header().Get("Content-Encoding") != "" {
			t.Error("text compressed")
		}
	})
	t.Run("timing still sees the status", func(t *testing.T) {
		var buf bytes.Buffer
		log.SetOutput(&buf)
		t.Cleanup(func() { log.SetOutput(os.Stderr) })
		mux := http.NewServeMux()
		mux.Handle("/x", jsonHandler(418, 3000))
		serve(timing(mux, gzipJSON(mux)), "GET", "/x", gz)
		if !strings.Contains(buf.String(), " 418 ") {
			t.Errorf("log = %q", buf.String())
		}
	})
	t.Run("through the full handler: 404 and health", func(t *testing.T) {
		h := NewHandler()
		rec := serve(h, "GET", "/nope", gz)
		if rec.Code != 404 || rec.Header().Get("Vary") != "Accept-Encoding" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("404: %d %v", rec.Code, rec.Header())
		}
	})
}

func TestETagMatching(t *testing.T) {
	for in, want := range map[string]bool{
		`"a"`: true, `W/"a"`: true, `"b", "a"`: true, `*`: true, `"b"`: false, ``: false, `"ab"`: false,
	} {
		if got := etagMatches(in, `"a"`); got != want {
			t.Errorf("etagMatches(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestBranchCacheETag(t *testing.T) {
	a := branchCache("tree", "c1", "src", false)
	if a != branchCache("tree", "c1", "src", false) {
		t.Error("not deterministic")
	}
	for _, b := range []cacheHeaders{branchCache("tree", "c2", "src", false), branchCache("tree", "c1", "other", false), branchCache("blob", "c1", "src", false)} {
		if b.etag == a.etag {
			t.Error("etag collision across commit/path/kind")
		}
	}
}

// --- database tests ---

func (f *readFixture) req(method, path string, hdr map[string]string) *httptest.ResponseRecorder {
	return serve(f.s.Handler(), method, f.base+path, hdr)
}

func TestReadCacheHeaders(t *testing.T) {
	f := newReadFixture(t)
	c1, c2 := f.c1.OID, f.c2.OID

	t.Run("full oid is immutable", func(t *testing.T) {
		for _, p := range []string{"/tree/" + c1, "/tree/" + c1 + "/src", "/blob/" + c1 + "/README.md", "/commits/" + c2} {
			rec := f.req("GET", p, nil)
			if rec.Code != 200 {
				t.Fatalf("%s: %d %s", p, rec.Code, rec.Body)
			}
			if got := rec.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
				t.Errorf("%s: Cache-Control = %q", p, got)
			}
		}
	})
	t.Run("branch name revalidates with a strong etag", func(t *testing.T) {
		for _, p := range []string{"/tree/main", "/tree/main/src", "/blob/main/README.md", "/blob/dev/README.md"} {
			rec := f.req("GET", p, nil)
			et := rec.Header().Get("ETag")
			if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-cache" || !strings.HasPrefix(et, `"`) {
				t.Errorf("%s: %d cc=%q etag=%q", p, rec.Code, rec.Header().Get("Cache-Control"), et)
			}
		}
	})
	t.Run("errors carry no cache headers", func(t *testing.T) {
		for _, p := range []string{"/tree/nope", "/tree/main/missing", "/blob/main/src", "/tree/" + strings.Repeat("0", 40), "/commits/" + strings.Repeat("0", 40), "/commits/xyz"} {
			rec := f.req("GET", p, nil)
			if rec.Code < 400 {
				t.Fatalf("%s: %d", p, rec.Code)
			}
			if rec.Header().Get("Cache-Control") != "" || rec.Header().Get("ETag") != "" {
				t.Errorf("%s: cached error %v", p, rec.Header())
			}
		}
	})
	t.Run("auth and health are not cached", func(t *testing.T) {
		if rec := serve(f.s.Handler(), "GET", "/auth/me", nil); rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("/auth/me Cache-Control = %q", rec.Header().Get("Cache-Control"))
		}
	})
}

func TestRead304(t *testing.T) {
	f := newReadFixture(t)
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	first := f.req("GET", "/blob/main/README.md", nil)
	et := first.Header().Get("ETag")
	logs.Reset()
	rec := f.req("GET", "/blob/main/README.md", map[string]string{"If-None-Match": et})
	if rec.Code != 304 || rec.Body.Len() != 0 || rec.Header().Get("ETag") != et || rec.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("304 path: %d body=%d %v", rec.Code, rec.Body.Len(), rec.Header())
	}
	// the statement count only means something on Postgres
	if !strings.Contains(logs.String(), " 304 ") || testStoreKind() == "postgres" && !strings.Contains(logs.String(), "stmts=1\n") {
		t.Errorf("304 should cost exactly the ref lookup statement: %q", logs.String())
	}

	t.Run("stale etag gets the body", func(t *testing.T) {
		rec := f.req("GET", "/blob/main/README.md", map[string]string{"If-None-Match": `"stale"`})
		if rec.Code != 200 || rec.Body.Len() == 0 {
			t.Errorf("%d", rec.Code)
		}
	})
	t.Run("etag differs per path and endpoint", func(t *testing.T) {
		if f.req("GET", "/blob/main/empty.txt", nil).Header().Get("ETag") == et {
			t.Error("same etag for another path")
		}
		if f.req("GET", "/blob/main/README.md", map[string]string{"If-None-Match": et}).Code != 304 {
			t.Error("lost 304")
		}
	})
	t.Run("etag changes when the tip moves", func(t *testing.T) {
		// dev still points at c1 while main is at c2
		onC1 := f.req("GET", "/blob/dev/README.md", nil).Header().Get("ETag")
		onC2 := f.req("GET", "/blob/main/README.md", nil).Header().Get("ETag")
		if onC1 == onC2 {
			t.Fatal("etag does not depend on the commit")
		}
	})
	t.Run("404s survive a matching If-None-Match", func(t *testing.T) {
		h := map[string]string{"If-None-Match": et}
		if rec := f.req("GET", "/blob/gone/README.md", h); rec.Code != 404 {
			t.Errorf("missing ref: %d", rec.Code)
		}
		if rec := serve(f.s.Handler(), "GET", "/repos/"+f.owner+"/nope/blob/main/README.md", h); rec.Code != 404 {
			t.Errorf("missing repo: %d", rec.Code)
		}
		if rec := f.req("GET", "/blob/main/a%2e%2egit", h); rec.Code == 304 {
			t.Errorf("invalid path answered 304")
		}
	})
	t.Run("full oid ignores If-None-Match", func(t *testing.T) {
		rec := f.req("GET", "/blob/"+f.c1.OID+"/README.md", map[string]string{"If-None-Match": "*"})
		if rec.Code != 200 {
			t.Errorf("%d", rec.Code)
		}
	})
}

func TestReadGzipEndToEnd(t *testing.T) {
	f := newReadFixture(t)
	big := strings.Repeat("line of text\n", 400)
	owner, c := newUserCookie(t, f.s)
	repo := createRepo(t, f.s, c, `{"name":"big"}`)
	mustCommit(t, f.s, owner, c, repo, commitJSON("main", "", "big", up("big.txt", big)))
	base := "/repos/" + owner + "/big"
	gz := map[string]string{"Accept-Encoding": "gzip"}

	rec := serve(f.s.Handler(), "GET", base+"/blob/main/big.txt", gz)
	if rec.Code != 200 || rec.Header().Get("Content-Encoding") != "gzip" || rec.Header().Get("Vary") != "Accept-Encoding" {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
	if !strings.Contains(gunzip(t, rec.Body.Bytes()), `"size":`) {
		t.Error("decoded body is not the blob response")
	}
	// a 304 stays bodyless and unencoded
	et := strings.TrimPrefix(rec.Header().Get("ETag"), "W/")
	rec = serve(f.s.Handler(), "GET", base+"/blob/main/big.txt", map[string]string{"Accept-Encoding": "gzip", "If-None-Match": et})
	if rec.Code != 304 || rec.Body.Len() != 0 {
		t.Errorf("gzip client 304: %d body %d", rec.Code, rec.Body.Len())
	}
	// HEAD on a read route is a 405 (GET only) and never encoded
	if rec := serve(f.s.Handler(), "HEAD", base+"/blob/main/big.txt", gz); rec.Header().Get("Content-Encoding") != "" {
		t.Error("HEAD encoded")
	}
}

func TestBranchCachePrivate(t *testing.T) {
	pub, priv := branchCache("tree", "c1", "src", false), branchCache("tree", "c1", "src", true)
	if pub.control != "no-cache" || priv.control != "private, no-cache" {
		t.Errorf("control: public %q, private %q", pub.control, priv.control)
	}
	if pub.etag == "" || pub.etag != priv.etag {
		t.Errorf("etag must be set and independent of visibility: %q %q", pub.etag, priv.etag)
	}
}
