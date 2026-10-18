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
	store, closeStore, err := openStore(context.Background(), cfg)
	if err != nil {
		log.Fatalf("database: %v", err)
	}

	srv := NewServer(store)
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
		closeStore()
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
	closeStore()
}

// openStore builds the Store the config names. STORE=memory opens no database
// and no pool; otherwise it opens the Postgres pool. The returned func releases
// the store's resources.
func openStore(ctx context.Context, cfg Config) (Store, func(), error) {
	if cfg.Store == StoreMemory {
		return newMemStore(), func() {}, nil
	}
	pool, err := NewPool(ctx, cfg.DatabaseURL, cfg.Pool)
	if err != nil {
		return nil, nil, err
	}
	return newPgxStore(pool), pool.Close, nil
}

// checkStoreAvailable refuses settings whose store is not built yet (#93
// SQLite) so main never falls back to Postgres silently. STORE=memory needs no
// database, so DB_DRIVER does not matter for it.
func checkStoreAvailable(cfg Config) error {
	if cfg.Store == StoreMemory {
		return nil
	}
	if cfg.DBDriver == DriverSQLite {
		return errors.New("DB_DRIVER=sqlite is not available in this build yet")
	}
	return nil
}
