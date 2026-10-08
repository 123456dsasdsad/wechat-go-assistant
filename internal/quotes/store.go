// Package quotes stores reference material, never authentication or CDN keys.
package quotes

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"io"
	"os"
	"sync"
	"time"
)

const maxBytes = 16 << 20
const maxRecords = 10000
const retention = 30 * 24 * time.Hour

type Attachment struct {
	Ref   files.Ref `json:"ref"`
	Store string    `json:"store"` // input or output; validated again when resolved
}
type Content struct {
	Text        string       `json:"text,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
}
type record struct {
	Scope   string    `json:"scope"`
	ID      string    `json:"id"`
	Created time.Time `json:"created"`
	Content
}
type state struct {
	Version int               `json:"version"`
	Records map[string]record `json:"records"`
}
type Store struct {
	mu   sync.Mutex
	path string
	db   *sql.DB
	now  func() time.Time
}

func digest(v string) string        { sum := sha256.Sum256([]byte(v)); return hex.EncodeToString(sum[:]) }
func scope(bot, peer string) string { return digest(bot + "\x00" + peer) }
func key(scope, id string) string   { return digest(scope + "\x00" + id) }
func validContent(c Content) bool {
	if len(c.Text) > 256<<10 || len(c.Attachments) > 4 {
		return false
	}
	for _, a := range c.Attachments {
		if !files.ValidRef(a.Ref) || (a.Store != "input" && a.Store != "output") {
			return false
		}
	}
	return true
}
func Open(path string) (*Store, error) {
	db, e := metadb.Open(path + ".sqlite")
	if e != nil {
		return nil, e
	}
	metadb.KeepOpen(db)
	s := &Store{path: path, db: db, now: time.Now}
	if _, e = db.Exec(`CREATE TABLE IF NOT EXISTS quotes (key TEXT PRIMARY KEY, scope TEXT NOT NULL, id TEXT NOT NULL, created INTEGER NOT NULL, bytes INTEGER NOT NULL, document BLOB NOT NULL)`); e != nil {
		return nil, e
	}
	if _, e = db.Exec("CREATE INDEX IF NOT EXISTS quotes_created ON quotes(created,key)"); e != nil {
		return nil, e
	}
	var marker []byte
	e = db.QueryRow("SELECT value FROM documents WHERE key='legacy_import_v1'").Scan(&marker)
	if e == nil {
		return s, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return nil, e
	}
	legacy := state{Version: 1, Records: map[string]record{}}
	f, e := os.Open(path)
	if e == nil {
		raw, readErr := io.ReadAll(io.LimitReader(f, maxBytes+1))
		f.Close()
		if readErr != nil || len(raw) > maxBytes || json.Unmarshal(raw, &legacy) != nil || legacy.Version != 1 || legacy.Records == nil || len(legacy.Records) > maxRecords {
			return nil, errors.New("invalid_quote_cache")
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	tx, e := db.Begin()
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	for k, r := range legacy.Records {
		if len(r.Scope) != 64 || r.ID == "" || len(r.ID) > 256 || k != key(r.Scope, r.ID) || r.Created.IsZero() || !validContent(r.Content) {
			return nil, errors.New("invalid_quote_record")
		}
		raw, e := json.Marshal(r)
		if e != nil {
			return nil, e
		}
		if _, e = tx.Exec("INSERT INTO quotes VALUES(?,?,?,?,?,?)", k, r.Scope, r.ID, r.Created.UnixNano(), len(raw), raw); e != nil {
			return nil, e
		}
	}
	if _, e = tx.Exec("INSERT INTO documents VALUES('legacy_import_v1','complete')"); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return s, nil
}
func (s *Store) Get(bot, peer, id string) (Content, bool) {
	if s == nil || bot == "" || peer == "" || id == "" {
		return Content{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var raw []byte
	e := s.db.QueryRow("SELECT document FROM quotes WHERE key=? AND created>=?", key(scope(bot, peer), id), s.now().Add(-retention).UnixNano()).Scan(&raw)
	var r record
	if e != nil || json.Unmarshal(raw, &r) != nil {
		return Content{}, false
	}
	return r.Content, true
}
func (s *Store) Put(bot, peer, id string, c Content) error {
	if s == nil || id == "" {
		return nil
	}
	if bot == "" || peer == "" || len(id) > 256 || !validContent(c) {
		return errors.New("invalid_quote_content")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	now := s.now()
	sc := scope(bot, peer)
	k := key(sc, id)
	created := now.UnixNano()
	if _, e = tx.Exec("DELETE FROM quotes WHERE created<?", now.Add(-retention).UnixNano()); e != nil {
		return e
	}
	var old int64
	e = tx.QueryRow("SELECT created FROM quotes WHERE key=?", k).Scan(&old)
	if e == nil {
		created = old
	} else if !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	r := record{Scope: sc, ID: id, Created: time.Unix(0, created), Content: c}
	raw, e := json.Marshal(r)
	if e != nil {
		return e
	}
	if _, e = tx.Exec("INSERT INTO quotes VALUES(?,?,?,?,?,?) ON CONFLICT(key) DO UPDATE SET bytes=excluded.bytes,document=excluded.document", k, sc, id, created, len(raw), raw); e != nil {
		return e
	}
	var count, bytes int
	if e = tx.QueryRow("SELECT count(*),coalesce(sum(bytes),0) FROM quotes").Scan(&count, &bytes); e != nil {
		return e
	}
	for count > maxRecords || bytes > maxBytes {
		var victim string
		var size int
		if e = tx.QueryRow("SELECT key,bytes FROM quotes WHERE key<>? ORDER BY created,key LIMIT 1", k).Scan(&victim, &size); e != nil {
			return errors.New("quote_cache_full")
		}
		if _, e = tx.Exec("DELETE FROM quotes WHERE key=?", victim); e != nil {
			return e
		}
		count--
		bytes -= size
	}
	return tx.Commit()
}
