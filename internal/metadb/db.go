// Package metadb persists assistant metadata; blobs and credentials stay in files.
package metadb

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"
)

var persistent sync.Map

// KeepOpen is for frequently queried stores. Owners close these on shutdown.
func KeepOpen(db *sql.DB) { db.SetMaxIdleConns(1); persistent.Store(db, true) }
func CloseAll() {
	persistent.Range(func(key, value any) bool {
		db := key.(*sql.DB)
		persistent.Delete(db)
		_ = db.Close()
		return true
	})
}

// Open bounds concurrent connections and releases file handles when idle.
// Per-connection pragmas bound the active page cache to 2 MiB.
func Open(path string) (*sql.DB, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	f.Close()
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=cache_size(-2048)&_pragma=temp_store(FILE)&_pragma=mmap_size(0)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0)
	if _, err = db.Exec("CREATE TABLE IF NOT EXISTS documents (key TEXT PRIMARY KEY, value BLOB NOT NULL)"); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// ReadJSON imports a legacy file once and then reads the database exclusively.
func ReadJSON(path string) ([]byte, error) {
	db, err := Open(path + ".sqlite")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var raw []byte
	err = db.QueryRow("SELECT value FROM documents WHERE key='state'").Scan(&raw)
	if err == nil {
		return raw, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	raw, err = os.ReadFile(path)
	// Validation remains the caller's responsibility before WriteJSON commits it.
	return raw, err
}

func WriteJSON(path string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	db, err := Open(path + ".sqlite")
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec("INSERT INTO documents(key,value) VALUES('state',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", raw)
	return err
}
