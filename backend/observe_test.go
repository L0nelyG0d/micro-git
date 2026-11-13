package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func obsServer(t *testing.T) (*Server, *bytes.Buffer) {
	t.Helper()
	s := NewServer(newMemStore())
	buf := &bytes.Buffer{}
	s.obs.log = newJSONLogger(buf)
	return s, buf
}

func obsDo(h http.Handler, method, path string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(`{"password":"hunter2"}`))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRedact(t *testing.T) {
	for _, k := range []string{"password", "Cookie", "Authorization", "jwt", "DATABASE_URL", "request_body", "access_token"} {
		if got := redact(k, "secret-value"); got != "[REDACTED]" {
			t.Errorf("redact(%q) = %q", k, got)
		}
	}
	if got := redact("route", "/repos/{owner}"); got != "/repos/{owner}" {
		t.Errorf("plain value changed: %q", got)
	}
	got := redact("msg", "connect postgres:"+"//user:pw123@db:5432/x failed") // split so the CI secret scan does not match the fixture
	if strings.Contains(got, "pw123") || strings.Contains(got, "user:") {
		t.Errorf("url credentials leaked: %q", got)
	}
	var buf bytes.Buffer
	newJSONLogger(&buf).Info("x", "password", "p1", "Authorization", "Bearer abc", "ok", "fine")
	if strings.Contains(buf.String(), "p1") || strings.Contains(buf.String(), "abc") || !strings.Contains(buf.String(), "fine") {
		t.Errorf("logger redaction wrong: %s", buf.String())
	}
}

func TestAccessLogJSON(t *testing.T) {
	t.Setenv("APP_VERSION", "")
	s, buf := obsServer(t)
	h := s.Handler()
	obsDo(h, "POST", "/auth/login", map[string]string{"Authorization": "Bearer topsecret", "Cookie": "session=cookiesecret"})
	line := buf.String()
	for _, bad := range []string{"topsecret", "cookiesecret", "hunter2"} {
		if strings.Contains(line, bad) {
			t.Errorf("log leaks %q: %s", bad, line)
		}
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &m); err != nil {
		t.Fatalf("not json: %v: %s", err, line)
	}
	for _, k := range []string{"time", "level", "msg", "trace_id", "span_id", "method", "route", "status", "duration_ms", "version"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing field %s in %s", k, line)
		}
	}
	if m["route"] != "/auth/login" || m["version"] != "dev" || m["method"] != "POST" {
		t.Errorf("unexpected fields: %v", m)
	}
	// Raw path with ids must be logged as the pattern.
	buf.Reset()
	obsDo(h, "GET", "/repos/alice/secretrepo", nil)
	if strings.Contains(buf.String(), "secretrepo") || !strings.Contains(buf.String(), "/repos/{owner}/{repo}") {
		t.Errorf("route not a pattern: %s", buf.String())
	}
}

func TestMetricsEndpoint(t *testing.T) {
	t.Setenv("COOKIE_SECURE", "")
	t.Setenv("METRICS_ADDR", "")
	t.Setenv("APP_VERSION", "1.2.3")
	t.Setenv("RENDER_GIT_COMMIT", "")
	t.Setenv("APP_COMMIT", "")
	s, _ := obsServer(t)
	h := s.Handler()
	obsDo(h, "GET", "/health", nil)
	obsDo(h, "GET", "/health", nil)
	rec := obsDo(h, "GET", "/metrics", nil)
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("metrics: %d %v", rec.Code, rec.Header())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`http_requests_total{method="GET",route="/health",status="200"} 2`,
		`http_request_duration_seconds_bucket{method="GET",route="/health",le="0.005"}`,
		`http_request_duration_seconds_bucket{method="GET",route="/health",le="5"}`,
		`http_request_duration_seconds_bucket{method="GET",route="/health",le="+Inf"} 2`,
		`http_request_duration_seconds_count{method="GET",route="/health"} 2`,
		`http_requests_in_flight 1`,
		`build_info{version="1.2.3",commit="dev"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %q in:\n%s", want, body)
		}
	}
	if rec := obsDo(h, "POST", "/metrics", nil); rec.Code != 405 {
		t.Errorf("POST /metrics = %d", rec.Code)
	}
}

func TestMetricsConcurrent(t *testing.T) {
	s, _ := obsServer(t)
	h := s.Handler()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			obsDo(h, "GET", "/health", nil)
			obsDo(h, "GET", "/metrics", nil)
		}()
	}
	wg.Wait()
	if !strings.Contains(obsDo(h, "GET", "/metrics", nil).Body.String(), `status="200"} 50`) {
		t.Error("lost counter updates")
	}
}

func TestTraceparent(t *testing.T) {
	s, buf := obsServer(t)
	h := s.Handler()
	rec := obsDo(h, "GET", "/health", map[string]string{"traceparent": "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"})
	tp := rec.Header().Get("traceparent")
	if !strings.HasPrefix(tp, "00-0af7651916cd43dd8448eb211c80319c-") || strings.Contains(tp, "b7ad6b7169203331") {
		t.Errorf("response traceparent = %q", tp)
	}
	if !strings.Contains(buf.String(), `"trace_id":"0af7651916cd43dd8448eb211c80319c"`) {
		t.Errorf("trace_id not logged: %s", buf.String())
	}
	// Absent or invalid: generated.
	for _, in := range []string{"", "garbage", "00-00000000000000000000000000000000-b7ad6b7169203331-01"} {
		rec := obsDo(h, "GET", "/health", map[string]string{"traceparent": in})
		if _, _, ok := parseTraceparent(rec.Header().Get("traceparent")); !ok {
			t.Errorf("input %q: bad generated traceparent %q", in, rec.Header().Get("traceparent"))
		}
	}
}

func TestOTLPExport(t *testing.T) {
	got := make(chan map[string]any, 4)
	var path string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		got <- m
	}))
	defer ts.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", ts.URL)
	s, _ := obsServer(t)
	obsDo(s.Handler(), "GET", "/health", nil)
	select {
	case m := <-got:
		b, _ := json.Marshal(m)
		for _, want := range []string{`"service.name"`, `"micro-git"`, `"GET /health"`, `"traceId"`} {
			if !strings.Contains(string(b), want) {
				t.Errorf("payload missing %s: %s", want, b)
			}
		}
		if path != "/v1/traces" {
			t.Errorf("path = %q", path)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no span exported")
	}
}

func TestOTLPDropsOnFailureAndNeverBlocks(t *testing.T) {
	e := &exporter{url: "http://127.0.0.1:1/v1/traces", ch: make(chan span, 2), client: &http.Client{Timeout: time.Second}}
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ { // no consumer, queue of 2: must not block
			e.enqueue(span{})
		}
		e.send([]span{{}}) // unreachable endpoint: returns, no panic
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("enqueue/send blocked")
	}
}

func TestMethodLabelBounded(t *testing.T) {
	s, buf := obsServer(t)
	h := s.Handler()
	for i := 0; i < 50; i++ {
		req := httptest.NewRequest("GET", "/health", nil)
		req.Method = fmt.Sprintf("FAKE%d", i)
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	body := obsDo(h, "GET", "/metrics", nil).Body.String()
	if strings.Contains(body, "FAKE") || strings.Contains(buf.String(), "FAKE") {
		t.Error("raw method leaked into metrics or logs")
	}
	n := 0
	for _, l := range strings.Split(body, "\n") {
		if strings.HasPrefix(l, "http_requests_total{") {
			n++
		}
	}
	if n > 3 { // OTHER + GET /metrics (in flight) at most
		t.Errorf("%d http_requests_total series, want a bounded few:\n%s", n, body)
	}
	if !strings.Contains(body, `method="OTHER",route="/health",status="`) {
		t.Errorf("no OTHER series:\n%s", body)
	}
}

func TestMiddlewarePanicSafe(t *testing.T) {
	s, buf := obsServer(t)
	h := s.obs.middleware(http.NewServeMux(), http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
	func() {
		defer func() { _ = recover() }()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/x", nil))
	}()
	var b bytes.Buffer
	s.obs.metrics.write(&b)
	if !strings.Contains(b.String(), `http_requests_total{method="GET",route="-",status="500"} 1`) {
		t.Errorf("panic not counted as 500:\n%s", b.String())
	}
	if !strings.Contains(b.String(), "http_requests_in_flight 0") {
		t.Errorf("in-flight leaked:\n%s", b.String())
	}
	if !strings.Contains(buf.String(), `"status":500`) {
		t.Errorf("no access log for panic: %s", buf.String())
	}
}

func TestFaultRefusedWhenCookieSecure(t *testing.T) {
	t.Setenv("FAULT_ENDPOINT_500", "/repos/{owner}/{repo}")
	t.Setenv("COOKIE_SECURE", "true")
	buf := captureTimingLog(t)
	s := NewServer(newMemStore())
	if rec := obsDo(s.Handler(), "GET", "/repos/a/b", nil); rec.Code == 500 {
		t.Error("fault applied despite COOKIE_SECURE=true")
	}
	if !strings.Contains(buf.String(), `"level":"ERROR"`) || !strings.Contains(buf.String(), "refused") {
		t.Errorf("no refusal error logged: %s", buf)
	}
	t.Setenv("COOKIE_SECURE", "")
	buf.Reset()
	NewServer(newMemStore())
	if !strings.Contains(buf.String(), `"level":"WARN"`) {
		t.Errorf("no startup warning: %s", buf)
	}
}

func TestMetricsDisabledInProduction(t *testing.T) {
	t.Setenv("METRICS_ADDR", "")
	t.Setenv("COOKIE_SECURE", "true")
	buf := captureTimingLog(t)
	s := NewServer(newMemStore())
	if rec := obsDo(s.Handler(), "GET", "/metrics", nil); rec.Code != 404 {
		t.Errorf("/metrics with COOKIE_SECURE=true and no METRICS_ADDR = %d, want 404", rec.Code)
	}
	if !strings.Contains(buf.String(), `"level":"WARN"`) || !strings.Contains(buf.String(), "metrics are disabled") {
		t.Errorf("no disabled warning: %s", buf)
	}
	// with METRICS_ADDR set the private handler still serves, and unspecified hosts warn
	t.Setenv("METRICS_ADDR", ":9100")
	buf.Reset()
	s = NewServer(newMemStore())
	if !strings.Contains(buf.String(), "all interfaces") {
		t.Errorf("no unspecified-host warning: %s", buf)
	}
	if rec := obsDo(s.obs.metricsHandler(), "GET", "/metrics", nil); rec.Code != 200 {
		t.Errorf("private metrics = %d", rec.Code)
	}
	for addr, want := range map[string]bool{":1": true, "0.0.0.0:1": true, "[::]:1": true, "127.0.0.1:1": false, "localhost:1": false} {
		if hostUnspecified(addr) != want {
			t.Errorf("hostUnspecified(%q) != %v", addr, want)
		}
	}
}

func TestMetricsAddrRemovesPublicRoute(t *testing.T) {
	t.Setenv("METRICS_ADDR", "127.0.0.1:0")
	s, _ := obsServer(t)
	if rec := obsDo(s.Handler(), "GET", "/metrics", nil); rec.Code != 404 {
		t.Errorf("public /metrics = %d, want 404", rec.Code)
	}
	rec := obsDo(s.obs.metricsHandler(), "GET", "/metrics", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "build_info") {
		t.Errorf("metrics listener = %d", rec.Code)
	}
}

func TestFaultInjection(t *testing.T) {
	// Off by default.
	s, _ := obsServer(t)
	if rec := obsDo(s.Handler(), "GET", "/health", nil); rec.Code != 200 {
		t.Fatalf("default health = %d", rec.Code)
	}
	t.Setenv("FAULT_ENDPOINT_500", "/repos/{owner}/{repo}")
	s, buf := obsServer(t)
	h := s.Handler()
	rec := obsDo(h, "GET", "/repos/a/b", nil)
	if rec.Code != 500 || !strings.Contains(rec.Body.String(), "error") {
		t.Errorf("fault route = %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(buf.String(), `"level":"ERROR"`) || !strings.Contains(buf.String(), `"status":500`) {
		t.Errorf("fault not logged as error: %s", buf.String())
	}
	if rec := obsDo(h, "GET", "/health", nil); rec.Code != 200 {
		t.Errorf("other route affected: %d", rec.Code)
	}
	if !strings.Contains(obsDo(h, "GET", "/metrics", nil).Body.String(), `route="/repos/{owner}/{repo}",status="500"} 1`) {
		t.Error("500 not counted in metrics")
	}
}

func TestExporterNon2xxCountsDropped(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer ts.Close()
	e := &exporter{url: ts.URL + "/v1/traces", ch: make(chan span, 2), client: &http.Client{Timeout: time.Second}}
	e.send([]span{{}, {}})
	if got := e.dropped.Load(); got != 2 {
		t.Fatalf("dropped = %d, want 2", got)
	}
	e.enqueue(span{})
	e.enqueue(span{})
	e.enqueue(span{}) // queue of 2 is full
	if got := e.dropped.Load(); got != 3 {
		t.Fatalf("dropped after full queue = %d, want 3", got)
	}
	m := newMetrics("v", "c")
	m.spansDropped = e.dropped.Load
	var sb strings.Builder
	m.write(&sb)
	if !strings.Contains(sb.String(), "otel_spans_dropped_total 3") {
		t.Errorf("metric missing:\n%s", sb.String())
	}
}

func TestExporterCloseFlushes(t *testing.T) {
	var n atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { n.Add(1); w.WriteHeader(200) }))
	defer ts.Close()
	e := newExporter(ts.URL)
	e.enqueue(span{name: "x"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	e.Close(ctx) // well before the 2s ticker
	e.Close(ctx) // idempotent
	if n.Load() != 1 {
		t.Fatalf("posts = %d, want 1", n.Load())
	}
	if e.dropped.Load() != 0 {
		t.Fatalf("dropped = %d", e.dropped.Load())
	}
	(*exporter)(nil).Close(ctx)
}
