package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Observability: structured access logs (slog JSON), Prometheus metrics at
// GET /metrics, W3C trace context and an optional OTLP/HTTP span exporter.
// Standard library only.
//
// Environment:
//
//	APP_VERSION                  version label in logs and build_info (default "dev")
//	RENDER_GIT_COMMIT, APP_COMMIT  commit label in build_info, same id as the X-Build header (default "dev")
//	OTEL_EXPORTER_OTLP_ENDPOINT  if set, one span per request goes to <endpoint>/v1/traces
//	FAULT_ENDPOINT_500           route pattern (e.g. /repos/{owner}/{repo}) that answers 500; for incident drills, off by default
//	                             (logs a WARN when set; refused with an ERROR when COOKIE_SECURE=true)
//	METRICS_ADDR                 e.g. 127.0.0.1:9100; when set, /metrics is served only there and is absent from the public mux.
//	                             With METRICS_ADDR unset and COOKIE_SECURE=true /metrics is disabled (WARN logged).

const serviceName = "micro-git"

var durationBuckets = []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5}

// ---- redaction ----

var sensitiveKeyParts = []string{"password", "passwd", "cookie", "authorization", "jwt", "token", "secret", "database_url", "dsn", "body", "api_key"}

// sensitiveKey reports whether a log field name must never carry its value.
func sensitiveKey(key string) bool {
	k := strings.ToLower(key)
	for _, p := range sensitiveKeyParts {
		if strings.Contains(k, p) {
			return true
		}
	}
	return false
}

var urlCredsRE = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^/@\s]*@`)

// redact returns the value to log for key: "[REDACTED]" for a sensitive key,
// otherwise the value with any user:password@ part of a URL removed.
func redact(key, value string) string {
	if sensitiveKey(key) {
		return "[REDACTED]"
	}
	return urlCredsRE.ReplaceAllString(value, "${1}[REDACTED]@")
}

func redactAttr(_ []string, a slog.Attr) slog.Attr {
	if a.Value.Kind() == slog.KindString {
		a.Value = slog.StringValue(redact(a.Key, a.Value.String()))
	} else if sensitiveKey(a.Key) {
		a.Value = slog.StringValue("[REDACTED]")
	}
	return a
}

func newJSONLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{ReplaceAttr: redactAttr}))
}

// ---- metrics ----

type reqKey struct{ method, route, status string }
type durKey struct{ method, route string }
type hist struct {
	counts []uint64 // per bucket, non-cumulative
	n      uint64
	sum    float64
}

type metrics struct {
	mu       sync.Mutex
	requests map[reqKey]uint64
	dur      map[durKey]*hist
	inFlight int64
	version  string
	commit   string
}

func newMetrics(version, commit string) *metrics {
	return &metrics{requests: map[reqKey]uint64{}, dur: map[durKey]*hist{}, version: version, commit: commit}
}

func (m *metrics) begin() { m.mu.Lock(); m.inFlight++; m.mu.Unlock() }

func (m *metrics) end(method, route string, status int, d time.Duration) {
	sec := d.Seconds()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inFlight--
	m.requests[reqKey{method, route, strconv.Itoa(status)}]++
	h := m.dur[durKey{method, route}]
	if h == nil {
		h = &hist{counts: make([]uint64, len(durationBuckets))}
		m.dur[durKey{method, route}] = h
	}
	h.n++
	h.sum += sec
	for i, b := range durationBuckets {
		if sec <= b {
			h.counts[i]++
			break
		}
	}
}

// normMethod maps a request method to a bounded label value: clients may send
// any token as a method, which would otherwise create unbounded series.
func normMethod(m string) string {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions:
		return m
	}
	return "OTHER"
}

func promEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s)
}

func (m *metrics) write(w io.Writer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fmt.Fprintln(w, "# HELP http_requests_total Total HTTP requests.")
	fmt.Fprintln(w, "# TYPE http_requests_total counter")
	rk := make([]reqKey, 0, len(m.requests))
	for k := range m.requests {
		rk = append(rk, k)
	}
	sort.Slice(rk, func(i, j int) bool {
		a, b := rk[i], rk[j]
		if a.route != b.route {
			return a.route < b.route
		}
		if a.method != b.method {
			return a.method < b.method
		}
		return a.status < b.status
	})
	for _, k := range rk {
		fmt.Fprintf(w, "http_requests_total{method=\"%s\",route=\"%s\",status=\"%s\"} %d\n", promEscape(k.method), promEscape(k.route), k.status, m.requests[k])
	}
	fmt.Fprintln(w, "# HELP http_request_duration_seconds HTTP request latency in seconds.")
	fmt.Fprintln(w, "# TYPE http_request_duration_seconds histogram")
	dk := make([]durKey, 0, len(m.dur))
	for k := range m.dur {
		dk = append(dk, k)
	}
	sort.Slice(dk, func(i, j int) bool {
		if dk[i].route != dk[j].route {
			return dk[i].route < dk[j].route
		}
		return dk[i].method < dk[j].method
	})
	for _, k := range dk {
		h := m.dur[k]
		lbl := fmt.Sprintf("method=\"%s\",route=\"%s\"", promEscape(k.method), promEscape(k.route))
		var cum uint64
		for i, b := range durationBuckets {
			cum += h.counts[i]
			fmt.Fprintf(w, "http_request_duration_seconds_bucket{%s,le=\"%s\"} %d\n", lbl, strconv.FormatFloat(b, 'g', -1, 64), cum)
		}
		fmt.Fprintf(w, "http_request_duration_seconds_bucket{%s,le=\"+Inf\"} %d\n", lbl, h.n)
		fmt.Fprintf(w, "http_request_duration_seconds_sum{%s} %s\n", lbl, strconv.FormatFloat(h.sum, 'g', -1, 64))
		fmt.Fprintf(w, "http_request_duration_seconds_count{%s} %d\n", lbl, h.n)
	}
	fmt.Fprintln(w, "# HELP http_requests_in_flight Requests currently being served.")
	fmt.Fprintln(w, "# TYPE http_requests_in_flight gauge")
	fmt.Fprintf(w, "http_requests_in_flight %d\n", m.inFlight)
	fmt.Fprintln(w, "# HELP build_info Build information.")
	fmt.Fprintln(w, "# TYPE build_info gauge")
	fmt.Fprintf(w, "build_info{version=\"%s\",commit=\"%s\"} 1\n", promEscape(m.version), promEscape(m.commit))
}

// ---- trace context ----

type traceCtxKey struct{}

// traceInfo is the trace identity of one request; handlers can read it with
// traceFrom.
type traceInfo struct{ TraceID, SpanID, ParentID string }

func traceFrom(ctx context.Context) traceInfo {
	t, _ := ctx.Value(traceCtxKey{}).(traceInfo)
	return t
}

func randHex(nbytes int) string {
	b := make([]byte, nbytes)
	for {
		_, _ = rand.Read(b)
		if s := hex.EncodeToString(b); strings.Trim(s, "0") != "" { // all-zero ids are invalid
			return s
		}
	}
}

var traceparentRE = regexp.MustCompile(`^00-([0-9a-f]{32})-([0-9a-f]{16})-[0-9a-f]{2}$`)

// parseTraceparent returns the trace and parent span id of a valid W3C
// traceparent header.
func parseTraceparent(h string) (traceID, parentID string, ok bool) {
	m := traceparentRE.FindStringSubmatch(strings.TrimSpace(h))
	if m == nil || strings.Trim(m[1], "0") == "" || strings.Trim(m[2], "0") == "" {
		return "", "", false
	}
	return m[1], m[2], true
}

// ---- OTLP exporter ----

type span struct {
	traceID, spanID, parentID string
	name                      string
	start, end                time.Time
	method, route             string
	status                    int
}

// exporter batches spans in a goroutine and posts them as OTLP/HTTP JSON.
// enqueue never blocks; a full queue or a failed POST drops the spans.
type exporter struct {
	url    string
	ch     chan span
	client *http.Client
}

func newExporter(endpoint string) *exporter {
	e := &exporter{
		url:    strings.TrimRight(endpoint, "/") + "/v1/traces",
		ch:     make(chan span, 1024),
		client: &http.Client{Timeout: 3 * time.Second},
	}
	go e.loop(100, 2*time.Second)
	return e
}

func (e *exporter) enqueue(s span) {
	select {
	case e.ch <- s:
	default: // queue full: drop
	}
}

func (e *exporter) loop(maxBatch int, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	var batch []span
	flush := func() {
		if len(batch) > 0 {
			e.send(batch)
			batch = nil
		}
	}
	for {
		select {
		case s := <-e.ch:
			batch = append(batch, s)
			if len(batch) >= maxBatch {
				flush()
			}
		case <-t.C:
			flush()
		}
	}
}

func (e *exporter) send(batch []span) {
	body, err := json.Marshal(otlpPayload(batch))
	if err != nil {
		return
	}
	resp, err := e.client.Post(e.url, "application/json", bytes.NewReader(body))
	if err != nil {
		return // dropped
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

type kv map[string]any

func strAttr(k, v string) kv { return kv{"key": k, "value": kv{"stringValue": v}} }

func otlpPayload(batch []span) kv {
	spans := make([]kv, 0, len(batch))
	for _, s := range batch {
		sp := kv{
			"traceId":           s.traceID,
			"spanId":            s.spanID,
			"name":              s.method + " " + s.route,
			"kind":              2, // SERVER
			"startTimeUnixNano": strconv.FormatInt(s.start.UnixNano(), 10),
			"endTimeUnixNano":   strconv.FormatInt(s.end.UnixNano(), 10),
			"attributes": []kv{
				strAttr("http.request.method", s.method),
				strAttr("http.route", s.route),
				{"key": "http.response.status_code", "value": kv{"intValue": strconv.Itoa(s.status)}},
			},
		}
		if s.parentID != "" {
			sp["parentSpanId"] = s.parentID
		}
		if s.status >= 500 {
			sp["status"] = kv{"code": 2}
		}
		spans = append(spans, sp)
	}
	return kv{"resourceSpans": []kv{{
		"resource":   kv{"attributes": []kv{strAttr("service.name", serviceName)}},
		"scopeSpans": []kv{{"scope": kv{"name": serviceName}, "spans": spans}},
	}}}
}

// ---- middleware ----

// observer bundles logger, metrics, exporter and the fault switch.
type observer struct {
	log         *slog.Logger
	metrics     *metrics
	exp         *exporter // nil when OTLP is off
	version     string
	faultRte    string // FAULT_ENDPOINT_500, "" when off or refused
	metricsAddr string // METRICS_ADDR; when set /metrics is not on the public mux
	// metricsPublic: /metrics is registered on the public mux (METRICS_ADDR unset and COOKIE_SECURE != "true").
	metricsPublic bool
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// accessLogOut is where access logs go; tests swap it.
var accessLogOut io.Writer = os.Stdout

type logWriter struct{}

func (logWriter) Write(p []byte) (int, error) { return accessLogOut.Write(p) }

func newObserver() *observer {
	secure, _ := parseCookieSecure(os.Getenv("COOKIE_SECURE")) // warning is emitted by LoadConfig
	o := &observer{
		log:         newJSONLogger(logWriter{}),
		version:     envOr("APP_VERSION", "dev"),
		faultRte:    os.Getenv("FAULT_ENDPOINT_500"),
		metricsAddr: os.Getenv("METRICS_ADDR"),
	}
	if o.faultRte != "" {
		if secure {
			o.log.Error("FAULT_ENDPOINT_500 refused: COOKIE_SECURE=true marks a production-like deployment", "route", o.faultRte)
			o.faultRte = ""
		} else {
			o.log.Warn("FAULT_ENDPOINT_500 is set: this route answers 500 on purpose (incident drill only)", "route", o.faultRte)
		}
	}
	o.metricsPublic = o.metricsAddr == "" && !secure
	if o.metricsAddr == "" && secure {
		o.log.Warn("metrics are disabled: COOKIE_SECURE=true (production) and METRICS_ADDR is empty; set METRICS_ADDR to serve /metrics on a private listener")
	}
	if o.metricsAddr != "" && secure && hostUnspecified(o.metricsAddr) {
		o.log.Warn("METRICS_ADDR listens on all interfaces; fine inside a container if the port is not published, otherwise bind it to a private address", "addr", o.metricsAddr)
	}
	o.metrics = newMetrics(o.version, buildID(os.Getenv))
	if ep := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); ep != "" {
		o.exp = newExporter(ep)
	}
	return o
}

// hostUnspecified reports whether a listen address has no host or an
// unspecified one (":9100", "0.0.0.0:9100", "[::]:9100").
func hostUnspecified(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsUnspecified()
}

// serveMetrics answers GET /metrics in Prometheus text format.
func (o *observer) serveMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	o.metrics.write(w)
}

// metricsHandler is the handler of the separate METRICS_ADDR listener.
func (o *observer) metricsHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", allow(http.MethodGet, o.serveMetrics))
	return mux
}

// middleware wraps next: trace context, fault injection, metrics, access log
// and span export. mux is only used to look up the matched route pattern.
// Accounting runs in a defer, so a panicking handler is still counted (as 500)
// and the in-flight gauge cannot leak; the panic is then re-raised for the
// server to handle.
func (o *observer) middleware(mux *http.ServeMux, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		o.metrics.begin()
		method := normMethod(r.Method)
		stmts := &reqStmts{}
		ti := traceInfo{SpanID: randHex(8)}
		if tid, pid, ok := parseTraceparent(r.Header.Get("traceparent")); ok {
			ti.TraceID, ti.ParentID = tid, pid
		} else {
			ti.TraceID = randHex(16)
		}
		w.Header().Set("traceparent", "00-"+ti.TraceID+"-"+ti.SpanID+"-01")
		ctx := context.WithValue(r.Context(), traceCtxKey{}, ti)
		r = r.WithContext(context.WithValue(ctx, reqStmtsKey{}, stmts))

		_, route := mux.Handler(r)
		if i := strings.IndexByte(route, ' '); i >= 0 { // drop "GET " method prefix
			route = route[i+1:]
		}
		if route == "" {
			route = "-"
		}
		rec := &statusRecorder{ResponseWriter: w}
		done := false
		defer func() {
			status := rec.status
			if !done { // handler panicked
				status = http.StatusInternalServerError
			} else if status == 0 {
				status = http.StatusOK
			}
			end := time.Now()
			d := end.Sub(start)
			o.metrics.end(method, route, status, d)
			lvl := slog.LevelInfo
			if status >= 500 {
				lvl = slog.LevelError
			}
			attrs := []any{
				"trace_id", ti.TraceID, "span_id", ti.SpanID,
				"method", method, "route", route, "status", status,
				"duration_ms", float64(d.Microseconds()) / 1000, "db_statements", stmts.n.Load(), "version", o.version,
			}
			if d > slowRequest {
				attrs = append(attrs, "slow", true)
			}
			o.log.Log(ctx, lvl, "request", attrs...)
			if o.exp != nil {
				o.exp.enqueue(span{traceID: ti.TraceID, spanID: ti.SpanID, parentID: ti.ParentID,
					name: method + " " + route, start: start, end: end, method: method, route: route, status: status})
			}
		}()
		if o.faultRte != "" && route == o.faultRte {
			writeError(rec, http.StatusInternalServerError, "injected fault")
		} else {
			next.ServeHTTP(rec, r)
		}
		done = true
	})
}
