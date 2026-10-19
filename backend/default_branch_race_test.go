package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/L0nelyG0d/micro-git/backend/gitobj"
	"github.com/jackc/pgx/v5/pgxpool"
)

// raceStores lists every store the default-branch race runs on, independent
// of TEST_STORE. The two Postgres stores need TEST_DATABASE_URL and each get
// a fresh schema (openPostgresForTest), so they never touch shared tables.
func raceStores(t *testing.T) map[string]func(t *testing.T) Store {
	return map[string]func(t *testing.T) Store{
		"memory": func(t *testing.T) Store { return newMemStore() },
		"sqlite": func(t *testing.T) Store {
			db := openSQLiteForTest(t)
			if err := Migrate(db); err != nil {
				t.Fatal(err)
			}
			return newGormStore(db)
		},
		"gorm-postgres": func(t *testing.T) Store {
			db, _ := openPostgresForTest(t, "race")
			if err := Migrate(db); err != nil {
				t.Fatal(err)
			}
			return newGormStore(db)
		},
		"pgx": func(t *testing.T) Store {
			db, schema := openPostgresForTest(t, "racepgx")
			if err := Migrate(db); err != nil {
				t.Fatal(err)
			}
			pool, err := NewPool(context.Background(), pgDSN(t, schema), DefaultPoolConfig())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { var p *pgxpool.Pool = pool; p.Close() })
			return newPgxStore(pool)
		},
	}
}

// TestDeleteNonDefaultRefRacingSetDefault runs DeleteNonDefaultRef("dev")
// against UpdateRepo(default_branch="dev") and requires that the repo never
// ends with a default branch that does not exist (#122). On Postgres the two
// statements used to run unserialized, so both could succeed. The race window
// is small, so many repos race in parallel for many rounds.
func TestDeleteNonDefaultRefRacingSetDefault(t *testing.T) {
	const workers, rounds = 8, 40
	for name, mk := range raceStores(t) {
		t.Run(name, func(t *testing.T) {
			st := mk(t)
			ctx := context.Background()
			uid, err := st.CreateUser(ctx, "racer", "h")
			if err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			bad := make(chan string, workers*rounds)
			for w := 0; w < workers; w++ {
				repo, err := st.CreateRepo(ctx, uid, fmt.Sprintf("r%d", w), "", false, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				oid, err := st.Objects(ctx, repo.ID).PutObject(gitobj.TypeBlob, []byte("x"))
				if err != nil {
					t.Fatal(err)
				}
				wg.Add(1)
				go func() {
					defer wg.Done()
					dev := "dev"
					for i := 0; i < rounds; i++ {
						if err := st.CreateRef(ctx, repo.ID, "main", oid); err != nil && !errors.Is(err, ErrConflict) {
							bad <- "create main: " + err.Error()
							return
						}
						if err := st.CreateRef(ctx, repo.ID, dev, oid); err != nil {
							bad <- "create dev: " + err.Error()
							return
						}
						start := make(chan struct{})
						var rg sync.WaitGroup
						var delErr, updErr error
						rg.Add(2)
						go func() { defer rg.Done(); <-start; delErr = st.DeleteNonDefaultRef(ctx, repo.ID, dev) }()
						go func() {
							defer rg.Done()
							<-start
							_, updErr = st.UpdateRepo(ctx, repo.ID, RepoPatch{DefaultBranch: &dev})
						}()
						close(start)
						rg.Wait()
						// Each call either wins or fails with its documented error.
						if delErr != nil && !errors.Is(delErr, errDefaultBranch) {
							bad <- "delete: " + delErr.Error()
						}
						if updErr != nil && !errors.Is(updErr, ErrNotFound) {
							bad <- "update: " + updErr.Error()
						}
						got, err := st.FindRepo(ctx, "racer", repo.Name)
						if err != nil {
							bad <- "get repo: " + err.Error()
							return
						}
						if _, err := st.GetRef(ctx, repo.ID, got.DefaultBranch); err != nil {
							bad <- fmt.Sprintf("round %d: default_branch %q has no ref (%v); delete=%v update=%v", i, got.DefaultBranch, err, delErr, updErr)
						}
						// Reset: default back to main, drop dev if it survived.
						main := "main"
						if _, err := st.UpdateRepo(ctx, repo.ID, RepoPatch{DefaultBranch: &main}); err != nil {
							bad <- "reset default: " + err.Error()
							return
						}
						if err := st.DeleteNonDefaultRef(ctx, repo.ID, dev); err != nil && !errors.Is(err, ErrNotFound) {
							bad <- "reset delete: " + err.Error()
							return
						}
					}
				}()
			}
			wg.Wait()
			close(bad)
			n := 0
			for msg := range bad {
				if n++; n <= 5 {
					t.Error(msg)
				}
			}
			if n > 5 {
				t.Errorf("... %d failures in %d rounds", n, workers*rounds)
			}
		})
	}
}
