package library

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
)

var ErrNotFound = errors.New("library_not_found")
var ErrStale = errors.New("library_snapshot_stale")

type Store struct {
	mu   sync.Mutex
	db   *sql.DB
	root string
}

func Open(root string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(root, "blobs"), 0700); err != nil {
		return nil, err
	}
	db, err := metadb.Open(filepath.Join(root, "library.sqlite"))
	if err != nil {
		return nil, err
	}
	metadb.KeepOpen(db)
	s := &Store{db: db, root: root}
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS intakes(owner TEXT,id TEXT,key TEXT,conversation TEXT,state TEXT,document BLOB,PRIMARY KEY(owner,id),UNIQUE(owner,key))`,
		`CREATE TABLE IF NOT EXISTS materials(id INTEGER PRIMARY KEY AUTOINCREMENT,owner TEXT,identity TEXT,deleted INTEGER,document BLOB,UNIQUE(owner,identity))`,
		`CREATE TABLE IF NOT EXISTS memberships(owner TEXT,material INTEGER,topic TEXT,PRIMARY KEY(owner,material,topic))`,
		`CREATE INDEX IF NOT EXISTS memberships_topic ON memberships(owner,topic,material)`,
		`CREATE TABLE IF NOT EXISTS topics(owner TEXT,name TEXT,revision INTEGER DEFAULT 0,version INTEGER DEFAULT 0,dirty INTEGER DEFAULT 0,error TEXT DEFAULT '',notes TEXT DEFAULT '',PRIMARY KEY(owner,name))`,
		`CREATE TABLE IF NOT EXISTS reviews(owner TEXT,topic TEXT,version INTEGER,document BLOB,PRIMARY KEY(owner,topic,version))`,
		`CREATE TABLE IF NOT EXISTS terms(owner TEXT,material INTEGER,term TEXT,PRIMARY KEY(owner,material,term))`,
		`CREATE INDEX IF NOT EXISTS terms_lookup ON terms(owner,term,material)`,
		`CREATE TABLE IF NOT EXISTS material_versions(owner TEXT,id INTEGER,revision INTEGER,document BLOB,PRIMARY KEY(owner,id,revision))`,
		`CREATE TABLE IF NOT EXISTS research(owner TEXT,intake TEXT,document BLOB,PRIMARY KEY(owner,intake))`,
	} {
		if _, err = db.Exec(q); err != nil {
			db.Close()
			return nil, err
		}
	}
	return s, nil
}
func (s *Store) Close() error { return s.db.Close() }
func id() string {
	var b [12]byte
	_, e := rand.Read(b[:])
	if e != nil {
		panic(e)
	}
	return hex.EncodeToString(b[:])
}
func validName(s string) bool {
	return strings.TrimSpace(s) != "" && len(s) <= 240 && !strings.ContainsAny(s, "\x00\r\n")
}
func decode(raw []byte, v any) error { return json.Unmarshal(raw, v) }
func (s *Store) Begin(owner, key, conversation, collection string) (Intake, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if owner == "" || key == "" {
		return Intake{}, errors.New("invalid_library_owner_or_key")
	}
	var raw []byte
	err := s.db.QueryRow("SELECT document FROM intakes WHERE owner=? AND key=?", owner, key).Scan(&raw)
	if err == nil {
		var v Intake
		e := decode(raw, &v)
		return v, e
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Intake{}, err
	}
	if collection == "" {
		collection = "待分类"
	}
	if !validName(collection) {
		return Intake{}, errors.New("invalid_collection")
	}
	v := Intake{ID: id(), Owner: owner, Conversation: conversation, Collection: collection, State: "draft", Created: time.Now().UTC(), Updated: time.Now().UTC()}
	return v, s.saveIntake(v, key)
}
func (s *Store) saveIntake(v Intake, key string) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	_, e = s.db.Exec(`INSERT INTO intakes(owner,id,key,conversation,state,document) VALUES(?,?,?,?,?,?) ON CONFLICT(owner,id) DO UPDATE SET conversation=excluded.conversation,state=excluded.state,document=excluded.document`, v.Owner, v.ID, key, v.Conversation, v.State, b)
	return e
}
func (s *Store) Intake(owner, id string) (Intake, error) {
	var raw []byte
	e := s.db.QueryRow("SELECT document FROM intakes WHERE owner=? AND id=?", owner, id).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return Intake{}, ErrNotFound
	}
	var v Intake
	if e == nil {
		e = decode(raw, &v)
	}
	return v, e
}
func (s *Store) Active(owner, cid string) (Intake, error) {
	var raw []byte
	e := s.db.QueryRow("SELECT document FROM intakes WHERE owner=? AND conversation=? AND state='draft' ORDER BY rowid DESC LIMIT 1", owner, cid).Scan(&raw)
	var v Intake
	if errors.Is(e, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	if e == nil {
		e = decode(raw, &v)
	}
	if e == nil && time.Since(v.Updated) > 30*time.Minute {
		return v, ErrNotFound
	}
	return v, e
}
func (s *Store) Resume(owner, iid, cid string) (Intake, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, e := s.Intake(owner, iid)
	if e != nil {
		return v, e
	}
	if v.State != "draft" && v.State != "canceled" {
		return v, errors.New("intake_already_finalized")
	}
	v.State = "draft"
	v.Conversation = cid
	v.Updated = time.Now().UTC()
	return v, s.saveIntake(v, "")
}
func (s *Store) Append(owner, iid, key, text string, assets []Asset) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, e := s.Intake(owner, iid)
	if e != nil {
		return e
	}
	// Message receipts live in documents and are committed with the batch.
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	receipt := "intake:" + owner + ":" + iid + ":" + key
	var exists int
	if e = tx.QueryRow("SELECT count(*) FROM documents WHERE key=?", receipt).Scan(&exists); e != nil {
		return e
	}
	if exists > 0 {
		return nil
	}
	if v.State != "draft" {
		return errors.New("intake_already_finalized")
	}
	if len(text) > 0 {
		v.Text += text + "\n"
	}
	if len(v.Text) > 1<<20 {
		return errors.New("intake_text_too_large")
	}
	for _, a := range assets {
		if !validHash(a.SHA256) || a.Size < 0 || !validName(a.Name) {
			return errors.New("invalid_asset")
		}
		seen := false
		for _, b := range v.Assets {
			if a.SHA256 == b.SHA256 {
				seen = true
			}
		}
		if !seen {
			v.Assets = append(v.Assets, a)
		}
	}
	v.Updated = time.Now().UTC()
	b, _ := json.Marshal(v)
	if _, e = tx.Exec("UPDATE intakes SET document=? WHERE owner=? AND id=?", b, owner, iid); e != nil {
		return e
	}
	if _, e = tx.Exec("INSERT INTO documents VALUES(?,?)", receipt, []byte("ok")); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) State(owner, iid, state string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, e := s.Intake(owner, iid)
	if e != nil {
		return e
	}
	if state == "queued" && v.Text == "" && len(v.Assets) == 0 {
		return errors.New("empty_intake")
	}
	if state == "queued" && (v.State == "ready" || v.State == "recognizing" || v.State == "researching") {
		return nil
	}
	v.State = state
	v.Updated = time.Now().UTC()
	return s.saveIntake(v, "")
}
func validHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, e := hex.DecodeString(s)
	return e == nil
}
func (s *Store) PutBlob(a Asset, r io.Reader) error {
	if !validHash(a.SHA256) || a.Size < 0 || a.Size == math.MaxInt64 {
		return errors.New("invalid_blob")
	}
	dst := filepath.Join(s.root, "blobs", a.SHA256)
	if info, e := os.Stat(dst); e == nil && info.Size() == a.Size {
		return nil
	}
	budget, e := files.DiskBudget(s.root)
	if e != nil || a.Size > budget {
		return errors.New("library_disk_full")
	}
	f, e := os.CreateTemp(filepath.Join(s.root, "blobs"), ".upload-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(f, h), io.LimitReader(r, a.Size+1))
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	if n != a.Size || hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
		return errors.New("blob_integrity_failed")
	}
	return os.Rename(f.Name(), dst)
}
func (s *Store) Blob(owner, sha string) (*os.File, error) {
	if !validHash(sha) {
		return nil, ErrNotFound
	}
	rows, e := s.db.Query("SELECT document FROM intakes WHERE owner=? UNION ALL SELECT document FROM materials WHERE owner=?", owner, owner)
	if e != nil {
		return nil, e
	}
	allowed := false
	for rows.Next() {
		var raw []byte
		var v struct {
			Assets []Asset `json:"assets"`
		}
		if rows.Scan(&raw) == nil && decode(raw, &v) == nil {
			for _, a := range v.Assets {
				if a.SHA256 == sha {
					allowed = true
				}
			}
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	if !allowed {
		return nil, ErrNotFound
	}
	return os.Open(filepath.Join(s.root, "blobs", sha))
}
