package main

import (
	"context"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

// startTestServer serves h on a random local port.
func startTestServer(t *testing.T, h http.Handler) (*http.Server, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &http.Server{Handler: h}
	go func() { _ = s.Serve(ln) }()
	t.Cleanup(func() { _ = s.Close() })
	return s, "http://" + ln.Addr().String()
}

func TestGracefulShutdownOrder(t *testing.T) {
	api, _ := startTestServer(t, http.NotFoundHandler())
	metrics, _ := startTestServer(t, http.NotFoundHandler())
	var order []string
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ran := gracefulShutdown(ctx, api, metrics,
		func() { order = append(order, "flush") },
		func() { order = append(order, "close") })
	if !ran || len(order) != 2 || order[0] != "flush" || order[1] != "close" {
		t.Fatalf("ran=%v order=%v, want flush then close", ran, order)
	}
	// Both listeners are down.
	if err := api.Shutdown(ctx); err != nil && err != http.ErrServerClosed {
		t.Errorf("api not shut down: %v", err)
	}
}

func TestGracefulShutdownNilMetrics(t *testing.T) {
	api, _ := startTestServer(t, http.NotFoundHandler())
	var closed atomic.Bool
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if !gracefulShutdown(ctx, api, nil, func() {}, func() { closed.Store(true) }) || !closed.Load() {
		t.Fatal("store not closed with metrics server nil")
	}
}

func TestGracefulShutdownMetricsDownWhileAPIDrains(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	api, base := startTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
	}))
	metrics, murl := startTestServer(t, http.NotFoundHandler())
	go func() { _, _ = http.Get(base) }()
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	var closed atomic.Bool
	done := make(chan bool)
	go func() { done <- gracefulShutdown(ctx, api, metrics, func() {}, func() { closed.Store(true) }) }()

	// /metrics stops accepting while the API is still draining.
	deadline := time.Now().Add(300 * time.Millisecond)
	down := false
	for time.Now().Before(deadline) {
		if _, err := http.Get(murl); err != nil {
			down = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !down {
		t.Error("metrics listener still up while API drains")
	}
	ran := <-done
	close(release)
	if ran || closed.Load() {
		t.Fatalf("closeStore ran (ran=%v closed=%v) although an API handler was still active", ran, closed.Load())
	}
}
