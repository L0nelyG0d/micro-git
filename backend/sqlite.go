package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// OpenSQLite opens (creating it and its directory when missing) the SQLite
// file at path with the pure-Go driver and sqliteDSNParams, and migrates it.
// The caller closes the handle with CloseGorm. The pragmas in sqliteDSNParams
// are set on every pooled connection. The pool is not capped: SQLite allows
// one writer at a time, and the others wait on busy_timeout; a one-connection
// cap would deadlock a write on the outer store while a transaction is open.
func OpenSQLite(path string) (*gorm.DB, error) {
	if path == "" {
		return nil, errors.New("SQLITE_PATH is empty")
	}
	if strings.ContainsAny(path, "?#") {
		return nil, errors.New(`SQLITE_PATH must not contain "?" or "#"`)
	}
	if fi, err := os.Stat(path); err == nil && fi.IsDir() {
		return nil, errors.New("SQLITE_PATH is a directory, it must name a file")
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("create the SQLITE_PATH directory: %w", err)
		}
	}
	db, err := gorm.Open(sqlite.Open(path+"?"+sqliteDSNParams), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}
	return db, nil
}

// CloseGorm releases the connections of a gorm handle.
func CloseGorm(db *gorm.DB) {
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.Close()
	}
}
