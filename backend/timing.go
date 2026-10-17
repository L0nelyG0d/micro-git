package main

import (
	"context"
	"log"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
)

// slowRequest is the duration over which a request is logged with "slow:".
const slowRequest = 500 * time.Millisecond

type reqStmtsKey struct{}

// reqStmts counts the DB statements of one request.
type reqStmts struct{ n atomic.Int64 }

// stmtTracer is a pgx tracer that counts statements into the reqStmts
// carried by the context. It records nothing else: no SQL text, no arguments.
type stmtTracer struct{}

func (stmtTracer) count(ctx context.Context) {
	if c, ok := ctx.Value(reqStmtsKey{}).(*reqStmts); ok {
		c.n.Add(1)
	}
}

func (t stmtTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	t.count(ctx)
	return ctx
}

func (stmtTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (stmtTracer) TraceBatchStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceBatchStartData) context.Context {
	return ctx
}

func (t stmtTracer) TraceBatchQuery(ctx context.Context, _ *pgx.Conn, _ pgx.TraceBatchQueryData) {
	t.count(ctx)
}

func (stmtTracer) TraceBatchEnd(context.Context, *pgx.Conn, pgx.TraceBatchEndData) {}

// statusRecorder remembers the status code a handler wrote.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusRecorder) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// timing logs one line per request: method, route pattern, status, duration
// and DB statement count. The pattern (e.g. "/repos/{owner}/{repo}/commits")
// is logged instead of the raw path, and no body, cookie or query value is.
func timing(mux *http.ServeMux, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		c := &reqStmts{}
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r.WithContext(context.WithValue(r.Context(), reqStmtsKey{}, c)))
		d := time.Since(start)
		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		_, route := mux.Handler(r) // pattern that matched, e.g. "GET /x/{id}"
		if route == "" {
			route = "-"
		}
		prefix := ""
		if d > slowRequest {
			prefix = "slow: "
		}
		log.Printf("%s%s %s %d %.1fms stmts=%d", prefix, r.Method, route, status, float64(d.Microseconds())/1000, c.n.Load())
	})
}
