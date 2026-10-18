package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
)

// HTTP caching and compression for the public read endpoints.
//
// Objects are content-addressed, so a response addressed by a full commit oid
// never changes: it is cached for a year. A response addressed by a branch
// name changes whenever the tip moves, so it is revalidated with a strong ETag
// derived from the resolved commit oid and the path.
//
// Known residual risk: an oid-addressed response is "public, ..., immutable"
// for a year. If its repo is made private afterwards, copies already stored
// by a shared cache (CDN, proxy) can still be served. The contract is
// deliberately unchanged (see _docs/performance.md and _docs/questions.md).

const (
	cacheImmutable = "public, max-age=31536000, immutable"
	// A private repository must never sit in a shared cache.
	cachePrivateImmutable = "private, max-age=31536000, immutable"
	cacheRevalidate       = "no-cache"
	// Revalidate always; "private" keeps a private repo out of shared caches.
	cachePrivateRevalidate = "private, no-cache"

	// gzipMinSize is the largest body that is sent uncompressed.
	gzipMinSize = 1024
)

// cacheHeaders are the validators of one successful read response. The zero
// value sets nothing.
type cacheHeaders struct{ control, etag string }

// immutableCache is for a response addressed by a full commit oid.
func immutableCache(private bool) cacheHeaders {
	if private {
		return cacheHeaders{control: cachePrivateImmutable}
	}
	return cacheHeaders{control: cacheImmutable}
}

// branchCache is for a response addressed by a branch name: the ETag changes
// whenever the resolved commit (or the path or endpoint) changes.
func branchCache(kind, commit, path string, private bool) cacheHeaders {
	control := cacheRevalidate
	if private {
		control = cachePrivateRevalidate
	}
	sum := sha256.Sum256([]byte(kind + "\x00" + commit + "\x00" + path))
	return cacheHeaders{control: control, etag: `"` + hex.EncodeToString(sum[:16]) + `"`}
}

// set writes the headers. Call it only on a 200/304 so errors are never cached.
func (c cacheHeaders) set(w http.ResponseWriter) {
	if c.control != "" {
		w.Header().Set("Cache-Control", c.control)
	}
	if c.etag != "" {
		w.Header().Set("ETag", c.etag)
	}
}

// notModified answers 304 when the request's If-None-Match matches the ETag
// (weak comparison, a list, or "*"). It reports whether it answered.
func (c cacheHeaders) notModified(w http.ResponseWriter, r *http.Request) bool {
	if c.etag == "" || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		return false
	}
	if !etagMatches(r.Header.Get("If-None-Match"), c.etag) {
		return false
	}
	c.set(w)
	w.WriteHeader(http.StatusNotModified)
	return true
}

func etagMatches(header, etag string) bool {
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "*" || strings.TrimPrefix(part, "W/") == etag {
			return true
		}
	}
	return false
}

// acceptsGzip reports whether Accept-Encoding allows gzip (q=0 forbids it).
func acceptsGzip(r *http.Request) bool {
	for _, v := range r.Header.Values("Accept-Encoding") {
		for _, part := range strings.Split(v, ",") {
			name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
			if !strings.EqualFold(strings.TrimSpace(name), "gzip") {
				continue
			}
			params = strings.ReplaceAll(params, " ", "")
			if q, ok := strings.CutPrefix(params, "q="); ok {
				f, err := strconv.ParseFloat(q, 64)
				return err == nil && f > 0
			}
			return true
		}
	}
	return false
}

// gzipJSON compresses JSON bodies over gzipMinSize for clients that accept
// gzip. The body is buffered (every handler writes one JSON document), so the
// size is known before the headers go out. It does not compress HEAD,
// 1xx/204/304, an already encoded body or non-JSON, and sets
// Vary: Accept-Encoding on every response it may have compressed. The status
// code reaches the wrapped writer unchanged, so the timing middleware outside
// it still records it.
func gzipJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Add("Vary", "Accept-Encoding")
		if !acceptsGzip(r) {
			next.ServeHTTP(w, r)
			return
		}
		g := &gzipWriter{ResponseWriter: w}
		next.ServeHTTP(g, r)
		g.finish()
	})
}

type gzipWriter struct {
	http.ResponseWriter
	status int
	sent   bool // headers already forwarded (bodyless status)
	buf    bytes.Buffer
}

func bodyless(code int) bool {
	return code < 200 || code == http.StatusNoContent || code == http.StatusNotModified
}

func (g *gzipWriter) WriteHeader(code int) {
	if g.status != 0 {
		return
	}
	g.status = code
	if bodyless(code) {
		g.sent = true
		g.ResponseWriter.WriteHeader(code)
	}
}

func (g *gzipWriter) Write(b []byte) (int, error) {
	if g.status == 0 {
		g.status = http.StatusOK
	}
	if g.sent {
		return len(b), nil
	}
	return g.buf.Write(b)
}

func (g *gzipWriter) Unwrap() http.ResponseWriter { return g.ResponseWriter }

// finish sends the buffered response, compressed when worth it.
func (g *gzipWriter) finish() {
	if g.sent {
		return
	}
	status := g.status
	if status == 0 {
		status = http.StatusOK // handler wrote nothing
	}
	h := g.Header()
	body := g.buf.Bytes()
	if len(body) > gzipMinSize && h.Get("Content-Encoding") == "" &&
		strings.HasPrefix(h.Get("Content-Type"), "application/json") {
		var out bytes.Buffer
		zw := gzip.NewWriter(&out)
		_, _ = zw.Write(body)
		if zw.Close() == nil && out.Len() < len(body) {
			body = out.Bytes()
			h.Set("Content-Encoding", "gzip")
			// a strong validator must differ per coding: weaken it
			if et := h.Get("ETag"); et != "" && !strings.HasPrefix(et, "W/") {
				h.Set("ETag", "W/"+et)
			}
		}
	}
	if len(body) > 0 {
		h.Set("Content-Length", strconv.Itoa(len(body)))
	}
	g.ResponseWriter.WriteHeader(status)
	_, _ = g.ResponseWriter.Write(body)
}
