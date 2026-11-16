package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

const (
	readHeaderTimeout    = 10 * time.Second  // a client that cannot send headers in 10s is stalled
	readTimeout          = 90 * time.Second  // a commit body can be up to 64 MiB
	writeTimeout         = 90 * time.Second  // large diffs and trees take a while to send on slow links
	idleTimeout          = 120 * time.Second // keep-alive connections are dropped after 2 minutes of silence
	shutdownTimeout      = 10 * time.Second  // time given to in-flight requests on SIGINT / SIGTERM
	exporterFlushTimeout = 3 * time.Second   // separate budget for flushing queued spans after the drain
)

func main() {
	cfg, err := LoadConfig(os.Getenv)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	for _, w := range cfg.Warnings {
		log.Printf("config warning: %s", w)
	}
	if err := checkStoreAvailable(cfg); err != nil {
		log.Fatalf("config: %v", err)
	}
	store, closeStore, err := openStore(context.Background(), cfg)
	if err != nil {
		log.Fatalf("database: %v", err)
	}

	srv := NewServer(store, cfg.observerConfig())
	srv.JWTSecret = []byte(cfg.JWTSecret)
	srv.CookieSecure = cfg.CookieSecure
	srv.TrustedProxyHops = cfg.TrustedProxyHops

	staticDir := os.Getenv("STATIC_DIR") // built frontend; empty = API only
	if staticDir != "" {
		if !isRegularFile(filepath.Join(staticDir, "index.html")) {
			log.Fatalf("STATIC_DIR %q has no index.html", staticDir)
		}
	}

	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           newHTTPHandler(srv, staticDir, buildID(os.Getenv)),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Bind synchronously so a bad address fails before "listening on" is logged.
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		closeStore()
		log.Fatalf("server: %v", err)
	}
	// METRICS_ADDR: /metrics on its own listener, not on the public one.
	var metricsSrv *http.Server
	var mln net.Listener
	if addr := cfg.MetricsAddr; addr != "" {
		mln, err = net.Listen("tcp", addr)
		if err != nil {
			_ = ln.Close()
			closeStore()
			log.Fatalf("metrics: %v", err)
		}
		metricsSrv = &http.Server{
			Handler:           srv.obs.metricsHandler(),
			ReadHeaderTimeout: readHeaderTimeout,
			ReadTimeout:       readHeaderTimeout,
			WriteTimeout:      readHeaderTimeout,
			IdleTimeout:       idleTimeout,
		}
	}

	errc := make(chan error, 2)
	go func() { errc <- httpSrv.Serve(ln) }()
	log.Printf("listening on %s", cfg.Addr)
	if metricsSrv != nil {
		go func() { errc <- fmt.Errorf("metrics: %w", metricsSrv.Serve(mln)) }()
		log.Printf("metrics listening on %s", cfg.MetricsAddr)
	}

	select {
	case err := <-errc:
		closeStore()
		log.Fatalf("server: %v", err)
	case <-ctx.Done():
	}
	stop()
	log.Printf("shutting down")
	shutCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	gracefulShutdown(shutCtx, httpSrv, metricsSrv, func() {
		// Flush after the drain so spans from requests that were still in flight are
		// included, with its own budget so a slow drain cannot starve the flush.
		flushCtx, flushCancel := context.WithTimeout(context.Background(), exporterFlushTimeout)
		defer flushCancel()
		srv.obs.exp.Close(flushCtx) // nil-safe when the exporter is disabled
	}, closeStore)
}

// gracefulShutdown stops both listeners in parallel (so a slow API drain does
// not keep /metrics up; metrics may be nil), then runs flush, then closeStore.
// closeStore runs only when the API drained: if api.Shutdown gave up (its ctx
// ended), handlers may still be running and must not lose the store under them;
// the process is about to exit, so the OS reclaims the connections. It returns
// whether closeStore ran.
func gracefulShutdown(ctx context.Context, api, metrics *http.Server, flush, closeStore func()) bool {
	var wg sync.WaitGroup
	if metrics != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := metrics.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("metrics shutdown: %v", err)
			}
		}()
	}
	err := api.Shutdown(ctx)
	wg.Wait()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("shutdown: %v", err)
		flush()
		log.Printf("store left open: API drain did not finish, handlers may still be active")
		return false
	}
	flush()
	closeStore()
	return true
}

// openStore builds the Store the config names. STORE=memory opens no database
// and no pool; DB_DRIVER=sqlite opens SQLITE_PATH through GORM; otherwise it
// opens the Postgres pool. The returned func releases
// the store's resources.
func openStore(ctx context.Context, cfg Config) (Store, func(), error) {
	if cfg.Store == StoreMemory {
		return newMemStore(), func() {}, nil
	}
	if cfg.DBDriver == DriverSQLite {
		db, err := OpenSQLite(cfg.SQLitePath)
		if err != nil {
			return nil, nil, err
		}
		if err := Migrate(db); err != nil {
			CloseGorm(db)
			return nil, nil, err
		}
		return newGormStore(db), func() { CloseGorm(db) }, nil
	}
	pool, err := NewPool(ctx, cfg.DatabaseURL, cfg.Pool)
	if err != nil {
		return nil, nil, err
	}
	if cfg.ApplySchema {
		if err := ApplySchema(ctx, pool); err != nil {
			pool.Close()
			return nil, nil, err
		}
		log.Printf("schema: applied db/schema.sql")
	}
	return newPgxStore(pool), pool.Close, nil
}

// checkStoreAvailable refuses settings that name a store this build cannot
// open. Every store is built now (memory, SQLite, Postgres), so it accepts all
// valid configs; LoadConfig has already rejected unknown values.
func checkStoreAvailable(cfg Config) error {
	if cfg.Store != StoreMemory && cfg.DBDriver != DriverSQLite && cfg.DBDriver != DriverPostgres {
		return fmt.Errorf("unknown DB_DRIVER %q", cfg.DBDriver)
	}
	return nil
}

// newHTTPHandler is the full handler: API, optional static frontend, and the
// X-Build header on every response.
func newHTTPHandler(srv *Server, staticDir, id string) http.Handler {
	return withBuildHeader(spaHandler(srv.Handler(), staticDir), id)
}
