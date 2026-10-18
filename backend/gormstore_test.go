package main

import (
	"context"
	"errors"
	"testing"

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

func TestGormStoreMapErrPassesOtherErrors(t *testing.T) {
	boom := errors.New("boom")
	if got := mapGormErr(boom); got != boom {
		t.Errorf("mapGormErr(boom) = %v, want unchanged", got)
	}
	if mapGormErr(nil) != nil {
		t.Error("mapGormErr(nil) != nil")
	}
}

func TestGormStoreUnportedMethods(t *testing.T) {
	db := openSQLiteForTest(t)
	st := newGormStore(db)
	ctx := context.Background()
	if _, err := st.FindRepo(ctx, "a", "b"); !errors.Is(err, errNotPorted) {
		t.Errorf("FindRepo err = %v, want errNotPorted", err)
	}
	if err := st.InTx(ctx, func(Store) error { return nil }); !errors.Is(err, errNotPorted) {
		t.Errorf("InTx err = %v, want errNotPorted", err)
	}
	if _, _, err := st.Objects(ctx, 1).GetObject("x"); !errors.Is(err, errNotPorted) {
		t.Errorf("Objects.GetObject err = %v, want errNotPorted", err)
	}
	if got := errNotPorted.Error(); got != "not implemented in gormStore yet" {
		t.Errorf("message = %q", got)
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

func TestIsTestFuncName(t *testing.T) {
	for name, want := range map[string]bool{
		"TestFoo": true, "Test": true, "TestFoo.func1": true, "Test_x": true,
		"TestimonyHelper": false, "testFoo": false, "limitedServer": false,
	} {
		if got := isTestFuncName(name); got != want {
			t.Errorf("isTestFuncName(%q) = %v, want %v", name, got, want)
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
