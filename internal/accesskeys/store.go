// Package accesskeys gates public assistant pages with revocable, scoped keys.
package accesskeys

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
)

type Grant struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Kind      string `json:"kind"`
	Scope     string `json:"scope"`
	ReadOnly  bool   `json:"read_only"`
	Expires   int64  `json:"expires"`
	Revoked   bool   `json:"revoked"`
	Delivered bool   `json:"delivered"`
	Created   int64  `json:"created"`
	Owner     string `json:"-"`
}
type Pending struct{ ID, Owner, Text string }
type Store struct {
	mu  sync.Mutex
	db  *sql.DB
	box cipher.AEAD
	now func() time.Time
}

func Open(root, secret string) (*Store, error) {
	if len(secret) < 32 {
		return nil, errors.New("access_secret_required")
	}
	if e := os.MkdirAll(root, 0700); e != nil {
		return nil, e
	}
	db, e := metadb.Open(filepath.Join(root, "accesskeys.sqlite"))
	if e != nil {
		return nil, e
	}
	metadb.KeepOpen(db)
	for _, q := range []string{`CREATE TABLE IF NOT EXISTS access_keys(id TEXT PRIMARY KEY,owner TEXT,label TEXT,kind TEXT,scope TEXT,readonly INTEGER,expires INTEGER,revoked INTEGER DEFAULT 0,delivered INTEGER DEFAULT 0,created INTEGER,hash TEXT UNIQUE)`, `CREATE TABLE IF NOT EXISTS access_sessions(hash TEXT PRIMARY KEY,key_id TEXT,expires INTEGER)`, `CREATE TABLE IF NOT EXISTS access_pending(key_id TEXT PRIMARY KEY,owner TEXT,ciphertext BLOB)`, `CREATE TABLE IF NOT EXISTS access_values(key_id TEXT PRIMARY KEY,ciphertext BLOB)`} {
		if _, e = db.Exec(q); e != nil {
			db.Close()
			return nil, e
		}
	}
	key := sha256.Sum256([]byte("access-key-outbox\x00" + secret))
	block, e := aes.NewCipher(key[:])
	if e != nil {
		db.Close()
		return nil, e
	}
	box, e := cipher.NewGCM(block)
	if e != nil {
		db.Close()
		return nil, e
	}
	return &Store{db: db, box: box, now: time.Now}, nil
}
func (s *Store) Close() error { return s.db.Close() }
func randomToken(prefix string) (string, error) {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b), nil
}
func hash(raw string) string { h := sha256.Sum256([]byte(raw)); return hex.EncodeToString(h[:]) }
func validScope(scope string) bool {
	return strings.HasPrefix(scope, "/wechat-files/") && len(scope) <= 240 && !strings.ContainsAny(scope, "?\x00\r\n#\\%") && path.Clean(scope) == strings.TrimSuffix(scope, "/")
}
func (s *Store) issue(owner, label, kind, scope string, minutes int, readonly, notify bool) (Grant, string, error) {
	if owner == "" || strings.TrimSpace(label) == "" || len(label) > 240 || !validScope(scope) || (kind == "temporary" && (minutes < 1 || minutes > 10080)) {
		return Grant{}, "", errors.New("invalid_access_key")
	}
	raw, e := randomToken("wxk_")
	if e != nil {
		return Grant{}, "", e
	}
	identifier, e := randomToken("")
	if e != nil {
		return Grant{}, "", e
	}
	now := s.now().Unix()
	g := Grant{ID: identifier[:16], Owner: owner, Label: label, Kind: kind, Scope: scope, ReadOnly: readonly, Created: now}
	if kind == "temporary" {
		g.Expires = now + int64(minutes)*60
	}
	tx, e := s.db.Begin()
	if e != nil {
		return g, "", e
	}
	defer tx.Rollback()
	if kind == "primary" {
		if _, e = tx.Exec("UPDATE access_keys SET revoked=1 WHERE owner=? AND kind='primary'", owner); e != nil {
			return g, "", e
		}
		if _, e = tx.Exec("DELETE FROM access_pending WHERE owner=? AND key_id IN (SELECT id FROM access_keys WHERE kind='primary')", owner); e != nil {
			return g, "", e
		}
	}
	if _, e = tx.Exec("INSERT INTO access_keys(id,owner,label,kind,scope,readonly,expires,created,hash) VALUES(?,?,?,?,?,?,?,?,?)", g.ID, owner, label, kind, scope, readonly, g.Expires, now, hash(raw)); e != nil {
		return g, "", e
	}
	sealedValue, e := s.sealValue(owner, g.ID, raw)
	if e != nil {
		return g, "", e
	}
	if _, e = tx.Exec("INSERT INTO access_values(key_id,ciphertext) VALUES(?,?)", g.ID, sealedValue); e != nil {
		return g, "", e
	}
	if notify {
		nonce := make([]byte, s.box.NonceSize())
		if _, e = rand.Read(nonce); e != nil {
			return g, "", e
		}
		text := "网页访问 key 已启用（任务、结果、上传、管理与校园应用）。\n\n" + raw + "\n\n首次打开页面时输入一次；当前浏览器登录保留30天。长期 key 可在管理页重置；临时 key 可设置有效期和范围。请在微信发送“管理页面”进入。"
		sealed := s.box.Seal(nonce, nonce, []byte(text), []byte(owner+":"+g.ID))
		if _, e = tx.Exec("INSERT INTO access_pending(key_id,owner,ciphertext) VALUES(?,?,?)", g.ID, owner, sealed); e != nil {
			return g, "", e
		}
	}
	return g, raw, tx.Commit()
}
func (s *Store) EnsurePrimary(owner string) (Grant, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, e := s.scan(s.db.QueryRow("SELECT id,owner,label,kind,scope,readonly,expires,revoked,delivered,created FROM access_keys WHERE owner=? AND kind='primary' AND revoked=0 ORDER BY created DESC LIMIT 1", owner))
	if e == nil {
		return g, false, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return g, false, e
	}
	g, _, e = s.issue(owner, "长期访问 key", "primary", "/wechat-files/", 0, false, true)
	return g, true, e
}
func (s *Store) Rotate(owner string) (Grant, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.issue(owner, "长期访问 key", "primary", "/wechat-files/", 0, false, true)
}
func (s *Store) Temporary(owner, label, scope string, minutes int, readonly bool) (Grant, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.issue(owner, label, "temporary", scope, minutes, readonly, false)
}
func (s *Store) scan(row interface{ Scan(...any) error }) (Grant, error) {
	var g Grant
	e := row.Scan(&g.ID, &g.Owner, &g.Label, &g.Kind, &g.Scope, &g.ReadOnly, &g.Expires, &g.Revoked, &g.Delivered, &g.Created)
	return g, e
}
func (s *Store) active(g Grant) bool {
	return !g.Revoked && (g.Expires == 0 || g.Expires > s.now().Unix())
}
func (s *Store) Lookup(raw string) (Grant, error) {
	if len(raw) < 32 || len(raw) > 128 {
		return Grant{}, errors.New("access_key_invalid")
	}
	g, e := s.scan(s.db.QueryRow("SELECT id,owner,label,kind,scope,readonly,expires,revoked,delivered,created FROM access_keys WHERE hash=?", hash(raw)))
	if e != nil || !s.active(g) {
		return Grant{}, errors.New("access_key_invalid")
	}
	// Successful login upgrades older hash-only keys without rotating them.
	sealed, e := s.sealValue(g.Owner, g.ID, raw)
	if e != nil {
		return Grant{}, e
	}
	if _, e = s.db.Exec("INSERT INTO access_values(key_id,ciphertext) VALUES(?,?) ON CONFLICT(key_id) DO NOTHING", g.ID, sealed); e != nil {
		return Grant{}, e
	}
	return g, nil
}
func (s *Store) sealValue(owner, id, raw string) ([]byte, error) {
	nonce := make([]byte, s.box.NonceSize())
	if _, e := rand.Read(nonce); e != nil {
		return nil, e
	}
	return s.box.Seal(nonce, nonce, []byte(raw), []byte("value:"+owner+":"+id)), nil
}
func (s *Store) Reveal(owner, id string) (string, error) {
	var ciphertext []byte
	var expected string
	var revoked bool
	var expires int64
	e := s.db.QueryRow("SELECT v.ciphertext,k.hash,k.revoked,k.expires FROM access_values v JOIN access_keys k ON k.id=v.key_id WHERE k.owner=? AND k.id=?", owner, id).Scan(&ciphertext, &expected, &revoked, &expires)
	if e != nil || revoked || (expires > 0 && expires <= s.now().Unix()) {
		return "", errors.New("access_key_display_unavailable")
	}
	if len(ciphertext) < s.box.NonceSize() {
		return "", errors.New("access_key_display_unavailable")
	}
	raw, e := s.box.Open(nil, ciphertext[:s.box.NonceSize()], ciphertext[s.box.NonceSize():], []byte("value:"+owner+":"+id))
	if e != nil || hash(string(raw)) != expected {
		return "", errors.New("access_key_display_unavailable")
	}
	return string(raw), nil
}
func (s *Store) Session(g Grant) (string, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var revoked bool
	if e := s.db.QueryRow("SELECT revoked FROM access_keys WHERE id=?", g.ID).Scan(&revoked); e != nil || revoked || !s.active(g) {
		return "", 0, errors.New("access_key_invalid")
	}
	raw, e := randomToken("")
	if e != nil {
		return "", 0, e
	}
	expires := s.now().Add(30 * 24 * time.Hour).Unix()
	if g.Expires > 0 && g.Expires < expires {
		expires = g.Expires
	}
	_, e = s.db.Exec("INSERT INTO access_sessions(hash,key_id,expires) VALUES(?,?,?)", hash(raw), g.ID, expires)
	if e == nil {
		_, e = s.db.Exec("DELETE FROM access_sessions WHERE expires<=?", s.now().Unix())
	}
	return raw, expires, e
}
func (s *Store) AuthorizeSession(raw string) (Grant, error) {
	if len(raw) < 32 || len(raw) > 128 {
		return Grant{}, errors.New("access_session_invalid")
	}
	g, e := s.scan(s.db.QueryRow("SELECT k.id,k.owner,k.label,k.kind,k.scope,k.readonly,k.expires,k.revoked,k.delivered,k.created FROM access_sessions s JOIN access_keys k ON k.id=s.key_id WHERE s.hash=? AND s.expires>?", hash(raw), s.now().Unix()))
	if e != nil || !s.active(g) {
		return Grant{}, errors.New("access_session_invalid")
	}
	return g, nil
}
func InScope(g Grant, p string) bool {
	if !validScope(g.Scope) || path.Clean(p) != strings.TrimSuffix(p, "/") {
		return false
	}
	if strings.HasSuffix(g.Scope, "/") {
		return strings.HasPrefix(p, g.Scope)
	}
	return p == g.Scope
}
func Allows(g Grant, p, method string) bool {
	return InScope(g, p) && (!g.ReadOnly || method == "GET" || method == "HEAD")
}
func (s *Store) EndSession(raw string) error {
	_, e := s.db.Exec("DELETE FROM access_sessions WHERE hash=?", hash(raw))
	return e
}
func (s *Store) Handoff(g Grant) (string, error) {
	if _, e := s.db.Exec("CREATE TABLE IF NOT EXISTS access_handoffs(hash TEXT PRIMARY KEY,key_id TEXT,expires INTEGER)"); e != nil {
		return "", e
	}
	raw, e := randomToken("")
	if e != nil {
		return "", e
	}
	_, e = s.db.Exec("INSERT INTO access_handoffs(hash,key_id,expires) VALUES(?,?,?)", hash(raw), g.ID, s.now().Add(time.Minute).Unix())
	return raw, e
}
func (s *Store) ConsumeHandoff(raw string) (Grant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.Begin()
	if e != nil {
		return Grant{}, e
	}
	defer tx.Rollback()
	g, e := s.scan(tx.QueryRow("SELECT k.id,k.owner,k.label,k.kind,k.scope,k.readonly,k.expires,k.revoked,k.delivered,k.created FROM access_handoffs h JOIN access_keys k ON k.id=h.key_id WHERE h.hash=? AND h.expires>?", hash(raw), s.now().Unix()))
	if e != nil || !s.active(g) {
		return Grant{}, errors.New("handoff_invalid")
	}
	if _, e = tx.Exec("DELETE FROM access_handoffs WHERE hash=? OR expires<=?", hash(raw), s.now().Unix()); e != nil {
		return Grant{}, e
	}
	return g, tx.Commit()
}
func (s *Store) List(owner string) ([]Grant, error) {
	rows, e := s.db.Query("SELECT id,owner,label,kind,scope,readonly,expires,revoked,delivered,created FROM access_keys WHERE owner=? ORDER BY created DESC LIMIT 100", owner)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Grant{}
	for rows.Next() {
		g, e := s.scan(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
func (s *Store) Revoke(owner, id string) error {
	res, e := s.db.Exec("UPDATE access_keys SET revoked=1 WHERE owner=? AND id=? AND kind='temporary'", owner, id)
	if e != nil {
		return e
	}
	n, e := res.RowsAffected()
	if e == nil && n == 0 {
		return errors.New("access_key_not_found")
	}
	return e
}
func (s *Store) Pending(owner string) ([]Pending, error) {
	rows, e := s.db.Query("SELECT key_id,ciphertext FROM access_pending WHERE owner=?", owner)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Pending{}
	for rows.Next() {
		var id string
		var raw []byte
		if e = rows.Scan(&id, &raw); e != nil {
			return nil, e
		}
		if len(raw) < s.box.NonceSize() {
			return nil, errors.New("access_outbox_invalid")
		}
		text, e := s.box.Open(nil, raw[:s.box.NonceSize()], raw[s.box.NonceSize():], []byte(owner+":"+id))
		if e != nil {
			return nil, errors.New("access_outbox_invalid")
		}
		out = append(out, Pending{ID: id, Owner: owner, Text: string(text)})
	}
	return out, rows.Err()
}
func (s *Store) Delivered(owner, id string) error {
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec("UPDATE access_keys SET delivered=1 WHERE owner=? AND id=?", owner, id); e != nil {
		return e
	}
	if _, e = tx.Exec("DELETE FROM access_pending WHERE owner=? AND key_id=?", owner, id); e != nil {
		return e
	}
	return tx.Commit()
}
