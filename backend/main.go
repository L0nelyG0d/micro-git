package main

import (
	"context"
	"log"
	"net/http"
	"os"
)

func main() {
	cfg, err := LoadConfig(os.Getenv)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	pool, err := NewPool(context.Background(), cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer pool.Close()

	log.Printf("listening on %s", cfg.Addr)
	log.Fatal(http.ListenAndServe(cfg.Addr, NewServer(pool).Handler()))
}
