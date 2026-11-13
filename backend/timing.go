package main

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
)

// slowRequest is the duration over which the access log carries slow=true.
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
