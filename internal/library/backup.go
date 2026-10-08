package library

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Backup creates a consistent standalone SQLite snapshot including committed WAL data.
func (s *Store) Backup(dest string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if filepath.Ext(dest) != ".sqlite" {
		return errors.New("invalid_backup_path")
	}
	if e := os.MkdirAll(filepath.Dir(dest), 0700); e != nil {
		return e
	}
	if _, e := os.Stat(dest); !os.IsNotExist(e) {
		return errors.New("backup_exists")
	}
	_, e := s.db.Exec("VACUUM INTO '" + strings.ReplaceAll(dest, "'", "''") + "'")
	if e == nil {
		e = os.Chmod(dest, 0600)
	}
	return e
}
