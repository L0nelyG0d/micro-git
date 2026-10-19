package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

// Every pooled connection, not just the first one, carries the pragmas.
func TestSQLitePragmasOnEveryConnection(t *testing.T) {
	db, err := OpenSQLite(filepath.Join(t.TempDir(), "mg.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { CloseGorm(db) })
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	const n = 8
	sqlDB.SetMaxOpenConns(n)
	ctx := context.Background()
	conns := make([]*sql.Conn, n)
	for i := range conns { // all held at once, so the pool opens n distinct connections
		if conns[i], err = sqlDB.Conn(ctx); err != nil {
			t.Fatal(err)
		}
		defer conns[i].Close()
	}
	want := map[string]string{
		"foreign_keys": "1",
		"journal_mode": "wal",
		"synchronous":  "1", // NORMAL
		"busy_timeout": "10000",
	}
	for i, c := range conns {
		for pragma, w := range want {
			var got string
			if err := c.QueryRowContext(ctx, "PRAGMA "+pragma).Scan(&got); err != nil {
				t.Fatalf("conn %d PRAGMA %s: %v", i, pragma, err)
			}
			if got != w {
				t.Errorf("conn %d PRAGMA %s = %q, want %q", i, pragma, got, w)
			}
		}
	}
}

// A second writer waits for the first one instead of failing with SQLITE_BUSY.
func TestSQLiteWriterWaitsInsteadOfBusy(t *testing.T) {
	db, err := OpenSQLite(filepath.Join(t.TempDir(), "mg.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { CloseGorm(db) })
	sqlDB, _ := db.DB()
	ctx := context.Background()
	first, err := sqlDB.BeginTx(ctx, nil) // _txlock=immediate: takes the write lock now
	if err != nil {
		t.Fatal(err)
	}
	const hold = 400 * time.Millisecond
	done := make(chan error, 1)
	var waited time.Duration
	go func() {
		start := time.Now()
		tx, err := sqlDB.BeginTx(ctx, nil)
		if err == nil {
			err = tx.Commit()
		}
		waited = time.Since(start)
		done <- err
	}()
	time.Sleep(hold)
	if err := first.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("second writer: %v (want it to wait, not fail)", err)
	}
	if waited < hold/2 {
		t.Errorf("second writer waited %v, want it blocked while the first held the lock", waited)
	}
}

// 100 commits to 100 different repos at the same moment all succeed.
func TestSQLite100ParallelCommitsNoBusy(t *testing.T) {
	s, closeStore := sqliteServer(t, filepath.Join(t.TempDir(), "mg.db"))
	defer closeStore()
	name := "par"
	if rec := do(s, http.MethodPost, "/auth/register", regBody(name, "correct horse"), nil); rec.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", rec.Code, rec.Body)
	}
	c := login(t, s, name)
	const n = 100
	repos := make([]Repo, n)
	for i := range repos {
		repos[i] = createRepo(t, s, c, fmt.Sprintf(`{"name":"r%d"}`, i))
	}
	recs := fireTogether(n, func(i int) *httptest.ResponseRecorder {
		body := commitJSON("main", "", "first", up("a.txt", fmt.Sprint(i)))
		return do(s, http.MethodPost, commitsURL(name, repos[i]), body, c)
	})
	for i, rec := range recs {
		if rec.Code != http.StatusCreated {
			t.Errorf("commit %d: %d %s", i, rec.Code, rec.Body)
		}
	}
}
