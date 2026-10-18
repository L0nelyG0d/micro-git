package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const (
	readHeaderTimeout = 10 * time.Second  // a client that cannot send headers in 10s is stalled
	readTimeout       = 90 * time.Second  // a commit body can be up to 64 MiB
	writeTimeout      = 90 * time.Second  // large diffs and trees take a while to send on slow links
	idleTimeout       = 120 * time.Second // keep-alive connections are dropped after 2 minutes of silence
	shutdownTimeout   = 10 * time.Second  // time given to in-flight requests on SIGINT / SIGTERM
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
	pool, err := NewPool(context.Background(), cfg.DatabaseURL, cfg.Pool)
	if err != nil {
		log.Fatalf("database: %v", err)
	}

	srv := NewServer(newPgxStore(pool))
	srv.JWTSecret = []byte(cfg.JWTSecret)
	srv.CookieSecure = cfg.CookieSecure

	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)
	go func() { errc <- httpSrv.ListenAndServe() }()
	log.Printf("listening on %s", cfg.Addr)

	select {
	case err := <-errc:
		pool.Close()
		log.Fatalf("server: %v", err)
	case <-ctx.Done():
	}
	stop()
	log.Printf("shutting down")
	shutCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := httpSrv.Shutdown(shutCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("shutdown: %v", err)
	}
	pool.Close()
}

// checkStoreAvailable refuses settings whose store is not built yet (#82
// memory, #93 SQLite) so main never falls back to Postgres silently.
func checkStoreAvailable(cfg Config) error {
	if cfg.Store == StoreMemory {
		return errors.New("STORE=memory is not available in this build yet")
	}
	if cfg.DBDriver == DriverSQLite {
		return errors.New("DB_DRIVER=sqlite is not available in this build yet")
	}
	return nil
}
