package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestGormStoreUsers(t *testing.T) {
	for _, d := range dialects {
		t.Run(d.name, func(t *testing.T) {
			db := d.open(t)
			if err := Migrate(db); err != nil {
				t.Fatal(err)
			}
			st := newGormStore(db)
			ctx := context.Background()

			id, err := st.CreateUser(ctx, "Alice", "hash1")
			if err != nil || id == 0 {
				t.Fatalf("CreateUser = %d, %v", id, err)
			}
			for _, dup := range []string{"Alice", "alice", "ALICE"} {
				if _, err := st.CreateUser(ctx, dup, "h"); !errors.Is(err, ErrConflict) {
					t.Errorf("CreateUser(%q) err = %v, want ErrConflict", dup, err)
				}
			}
			if name, err := st.UsernameByID(ctx, id); err != nil || name != "Alice" {
				t.Errorf("UsernameByID = %q, %v", name, err)
			}
			if _, err := st.UsernameByID(ctx, id+1000); !errors.Is(err, ErrNotFound) {
				t.Errorf("UsernameByID unknown err = %v, want ErrNotFound", err)
			}
			gotID, hash, err := st.UserForLogin(ctx, "alice")
			if err != nil || gotID != id || hash != "hash1" {
				t.Errorf("UserForLogin = %d, %q, %v", gotID, hash, err)
			}
			if _, _, err := st.UserForLogin(ctx, "nobody"); !errors.Is(err, ErrNotFound) {
				t.Errorf("UserForLogin unknown err = %v, want ErrNotFound", err)
			}
		})
	}
}

func TestGormStoreRepos(t *testing.T) {
	for _, d := range dialects {
		t.Run(d.name, func(t *testing.T) {
			db := d.open(t)
			if err := Migrate(db); err != nil {
				t.Fatal(err)
			}
			st := newGormStore(db)
			ctx := context.Background()
			now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
			alice, _ := st.CreateUser(ctx, "Alice", "h")
			bob, _ := st.CreateUser(ctx, "bob", "h")

			r, err := st.CreateRepo(ctx, alice, "Proj", "d", false, now)
			if err != nil || r.ID == 0 || r.Owner != "Alice" || r.Name != "Proj" || r.DefaultBranch != "main" ||
				r.Private || !r.Empty || r.CreatedAt != "2026-01-02T03:04:05Z" {
				t.Fatalf("CreateRepo = %+v, %v", r, err)
			}
			if p, err := st.CreateRepo(ctx, alice, "priv", "", true, now); err != nil || !p.Private {
				t.Errorf("private CreateRepo = %+v, %v", p, err)
			}
			for _, dup := range []string{"Proj", "proj", "PROJ"} {
				if _, err := st.CreateRepo(ctx, alice, dup, "", false, now); !errors.Is(err, ErrConflict) {
					t.Errorf("CreateRepo(%q) err = %v, want ErrConflict", dup, err)
				}
			}
			if _, err := st.CreateRepo(ctx, bob, "proj", "", false, now); err != nil {
				t.Errorf("same name, other owner: %v", err)
			}
			if _, err := st.CreateRepo(ctx, 9999, "x", "", false, now); !errors.Is(err, ErrForeignKey) {
				t.Errorf("unknown owner err = %v, want ErrForeignKey", err)
			}
			got, err := st.FindRepo(ctx, "ALICE", "pROJ")
			if err != nil || got.ID != r.ID {
				t.Errorf("FindRepo = %+v, %v", got, err)
			}
			if _, err := st.FindRepo(ctx, "alice", "nope"); !errors.Is(err, ErrNotFound) {
				t.Errorf("FindRepo missing err = %v", err)
			}
			if ok, err := st.RepoExists(ctx, r.ID); err != nil || !ok {
				t.Errorf("RepoExists = %v, %v", ok, err)
			}

			// UpdateRepo: patch only the set fields, atomic conflict and branch check.
			other, _ := st.CreateRepo(ctx, alice, "other", "", false, now)
			s := func(v string) *string { return &v }
			if _, err := st.UpdateRepo(ctx, other.ID, RepoPatch{}); err == nil {
				t.Error("empty patch should fail")
			}
			if _, err := st.UpdateRepo(ctx, other.ID, RepoPatch{Name: s("PROJ"), Description: s("changed")}); !errors.Is(err, ErrConflict) {
				t.Errorf("rename to existing err = %v, want ErrConflict", err)
			}
			if u, _ := st.FindRepo(ctx, "alice", "other"); u.Description != "" {
				t.Errorf("failed rename wrote description %q", u.Description)
			}
			u, err := st.UpdateRepo(ctx, other.ID, RepoPatch{Name: s("Renamed"), Description: s("new")})
			if err != nil || u.Name != "Renamed" || u.Description != "new" || u.DefaultBranch != "main" || u.Private {
				t.Errorf("rename+description = %+v, %v", u, err)
			}
			if u, err := st.UpdateRepo(ctx, other.ID, RepoPatch{Private: new(bool)}); err != nil || u.Private || u.Name != "Renamed" {
				t.Errorf("private only = %+v, %v", u, err)
			}
			yes := true
			if u, err := st.UpdateRepo(ctx, other.ID, RepoPatch{Private: &yes}); err != nil || !u.Private {
				t.Errorf("private on = %+v, %v", u, err)
			}
			if _, err := st.UpdateRepo(ctx, other.ID, RepoPatch{DefaultBranch: s("dev")}); !errors.Is(err, ErrNotFound) {
				t.Errorf("missing branch err = %v, want ErrNotFound", err)
			}
			if _, err := st.UpdateRepo(ctx, 9999, RepoPatch{Description: s("x")}); !errors.Is(err, ErrNotFound) {
				t.Errorf("missing repo err = %v, want ErrNotFound", err)
			}
			// Seed an object and a ref directly (their ref methods are #88).
			oid := OID(strings.Repeat("a", 40))
			if err := db.Create(&ObjectModel{RepoID: other.ID, OID: oid, Type: "blob", Data: []byte("x")}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&RefModel{RepoID: other.ID, Name: "dev", OID: oid}).Error; err != nil {
				t.Fatal(err)
			}
			if u, err := st.UpdateRepo(ctx, other.ID, RepoPatch{DefaultBranch: s("dev")}); err != nil || u.DefaultBranch != "dev" || u.Empty {
				t.Errorf("default branch dev = %+v, %v", u, err)
			}

			count := func(table string) int64 {
				var n int64
				db.Table(table).Where("repo_id = ?", other.ID).Count(&n)
				return n
			}
			if count("objects") != 1 || count("refs") != 1 {
				t.Fatalf("before delete: objects %d refs %d", count("objects"), count("refs"))
			}
			if err := st.DeleteRepo(ctx, other.ID); err != nil {
				t.Fatal(err)
			}
			if count("objects") != 0 || count("refs") != 0 {
				t.Errorf("after delete: objects %d refs %d, want 0 0", count("objects"), count("refs"))
			}
			if ok, err := st.RepoExists(ctx, other.ID); err != nil || ok {
				t.Errorf("RepoExists after delete = %v, %v", ok, err)
			}
			if _, err := st.FindRepo(ctx, "alice", "Renamed"); !errors.Is(err, ErrNotFound) {
				t.Errorf("FindRepo after delete err = %v", err)
			}
			if err := st.DeleteRepo(ctx, other.ID); err != nil {
				t.Errorf("second delete: %v", err)
			}
		})
	}
}

// ListRepos: the viewer rule, the case-insensitive owner filter, the newest-
// first order with the id tie-break, and limit/offset after the filter.
func TestGormStoreListReposVisibility(t *testing.T) {
	for _, d := range dialects {
		t.Run(d.name, func(t *testing.T) {
			db := d.open(t)
			if err := Migrate(db); err != nil {
				t.Fatal(err)
			}
			st := newGormStore(db)
			ctx := context.Background()
			t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
			alice, _ := st.CreateUser(ctx, "Alice", "h")
			bob, _ := st.CreateUser(ctx, "bob", "h")
			mk := func(owner int64, name string, private bool, sec int) {
				t.Helper()
				if _, err := st.CreateRepo(ctx, owner, name, "", private, t0.Add(time.Duration(sec)*time.Second)); err != nil {
					t.Fatal(err)
				}
			}
			mk(alice, "a1", false, 1)
			mk(alice, "a2-priv", true, 2)
			mk(bob, "b1", false, 3)
			mk(bob, "b2-priv", true, 4)
			mk(alice, "a3", false, 5)
			names := func(viewer int64, owner *string, limit, offset int) string {
				t.Helper()
				rs, err := st.ListRepos(ctx, viewer, owner, limit, offset)
				if err != nil {
					t.Fatal(err)
				}
				var n []string
				for _, r := range rs {
					n = append(n, r.Name)
				}
				return strings.Join(n, ",")
			}
			aliceName := "ALICE"
			if got := names(0, nil, 10, 0); got != "a3,b1,a1" {
				t.Errorf("anonymous = %q", got)
			}
			if got := names(bob, nil, 10, 0); got != "a3,b2-priv,b1,a1" {
				t.Errorf("bob = %q (must not see a2-priv)", got)
			}
			if got := names(alice, nil, 10, 0); got != "a3,b1,a2-priv,a1" {
				t.Errorf("alice = %q (must not see b2-priv)", got)
			}
			if got := names(0, &aliceName, 10, 0); got != "a3,a1" {
				t.Errorf("anonymous, owner ALICE = %q", got)
			}
			if got := names(alice, &aliceName, 10, 0); got != "a3,a2-priv,a1" {
				t.Errorf("alice, owner ALICE = %q", got)
			}
			if got := names(bob, &aliceName, 10, 0); got != "a3,a1" {
				t.Errorf("bob, owner ALICE = %q", got)
			}
			// Pagination applies after the visibility filter: no gaps from hidden rows.
			if got := names(0, nil, 2, 0) + "|" + names(0, nil, 2, 2) + "|" + names(0, nil, 2, 4); got != "a3,b1|a1|" {
				t.Errorf("anonymous pages = %q", got)
			}
			if got := names(alice, nil, 2, 1); got != "b1,a2-priv" {
				t.Errorf("alice limit 2 offset 1 = %q", got)
			}
			// Same created_at: the larger id comes first.
			mk(bob, "t1", false, 9)
			mk(bob, "t2", false, 9)
			if got := names(0, nil, 2, 0); got != "t2,t1" {
				t.Errorf("tie-break = %q", got)
			}
			// Toggling private is visible on the next list.
			r, _ := st.FindRepo(ctx, "bob", "t2")
			yes := true
			if _, err := st.UpdateRepo(ctx, r.ID, RepoPatch{Private: &yes}); err != nil {
				t.Fatal(err)
			}
			if got := names(0, nil, 2, 0); got != "t1,a3" {
				t.Errorf("after making t2 private, anonymous = %q", got)
			}
			if fr, err := st.FindRepo(ctx, "bob", "t2"); err != nil || !fr.Private {
				t.Errorf("FindRepo Private = %+v, %v", fr, err)
			}
		})
	}
}

func TestGormStoreMapErrPassesOtherErrors(t *testing.T) {
	boom := errors.New("boom")
	if got := mapGormErr(boom); got != boom {
		t.Errorf("mapGormErr(boom) = %v, want unchanged", got)
	}
	if mapGormErr(nil) != nil {
		t.Error("mapGormErr(nil) != nil")
	}
}

// The sqlite driver reports constraint failures as text; pin the wording we
// match so a driver upgrade that changes it fails here.
func TestMapGormErrDriverMessages(t *testing.T) {
	tests := []struct {
		name string
		in   error
		want error
	}{
		{"sqlite unique", errors.New("constraint failed: UNIQUE constraint failed: users.username (2067)"), ErrConflict},
		{"sqlite foreign key", errors.New("constraint failed: FOREIGN KEY constraint failed (787)"), ErrForeignKey},
		{"gorm not found", gorm.ErrRecordNotFound, ErrNotFound},
		{"gorm duplicated", gorm.ErrDuplicatedKey, ErrConflict},
		{"gorm foreign key", gorm.ErrForeignKeyViolated, ErrForeignKey},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := mapGormErr(tc.in); !errors.Is(got, tc.want) {
				t.Errorf("mapGormErr(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// Every sqlitePending key must be a Test function that exists, and every
// reason must name a later issue or Postgres, so stale entries are caught.
func TestSqlitePendingIsValid(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	var src strings.Builder
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src.Write(b)
	}
	reason := regexp.MustCompile(`#8[89]|#9[0-3]|needs Postgres`)
	for name, why := range sqlitePending {
		if !strings.Contains(src.String(), "\nfunc "+name+"(t *testing.T)") {
			t.Errorf("sqlitePending[%q]: no such Test function", name)
		}
		if !reason.MatchString(why) {
			t.Errorf("sqlitePending[%q] = %q: reason must name #88 to #93 or say needs Postgres", name, why)
		}
	}
}

// A real foreign-key failure on each dialect must map to ErrForeignKey.
func TestMapGormErrRealForeignKey(t *testing.T) {
	for _, d := range dialects {
		t.Run(d.name, func(t *testing.T) {
			db := d.open(t)
			if err := Migrate(db); err != nil {
				t.Fatal(err)
			}
			err := db.Exec(`INSERT INTO repos (owner_id, name) VALUES (999999, 'x')`).Error
			if err == nil {
				t.Fatal("insert with unknown owner succeeded")
			}
			if got := mapGormErr(err); !errors.Is(got, ErrForeignKey) {
				t.Errorf("mapGormErr(%v) = %v, want ErrForeignKey", err, got)
			}
		})
	}
}
