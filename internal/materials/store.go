// Package materials persists source packs and projects independently of AI runs.
package materials

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
)

const MaxEntries = 1000
const MaxAttachments = 15 // The sixteenth task attachment is the source manifest.
const MaxTextBytes = 2 << 20

type Entry struct {
	ID      string      `json:"id"`
	Kind    string      `json:"kind"`
	Text    string      `json:"text,omitempty"`
	Speaker string      `json:"speaker,omitempty"`
	Time    string      `json:"time,omitempty"`
	QuoteID string      `json:"quote_id,omitempty"`
	Missing []string    `json:"missing,omitempty"`
	Files   []files.Ref `json:"files,omitempty"`
}
type Pack struct {
	ID           string    `json:"id"`
	Owner        string    `json:"owner"`
	Title        string    `json:"title"`
	Conversation string    `json:"conversation"`
	Project      string    `json:"project,omitempty"`
	State        string    `json:"state"`
	Entries      []Entry   `json:"entries"`
	JobID        string    `json:"job_id,omitempty"`
	Instruction  string    `json:"instruction,omitempty"`
	Created      time.Time `json:"created"`
	Updated      time.Time `json:"updated"`
}
type Project struct {
	ID            string    `json:"id"`
	Owner         string    `json:"owner"`
	Title         string    `json:"title"`
	Conversations []string  `json:"conversations"`
	Topics        []string  `json:"topics"`
	Created       time.Time `json:"created"`
	Updated       time.Time `json:"updated"`
}
type Store struct {
	mu   sync.Mutex
	db   *sql.DB
	root string
}

func Open(root string) (*Store, error) {
	db, e := metadb.Open(filepath.Join(root, "materials.sqlite"))
	if e != nil {
		return nil, e
	}
	db.SetMaxIdleConns(1)
	s := &Store{db: db, root: root}
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS packs (id TEXT PRIMARY KEY,owner TEXT NOT NULL,state TEXT NOT NULL,updated INTEGER NOT NULL,document BLOB NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS packs_owner ON packs(owner,updated DESC)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS packs_active ON packs(owner) WHERE state='collecting'`,
		`CREATE TABLE IF NOT EXISTS projects (id TEXT PRIMARY KEY,owner TEXT NOT NULL,title TEXT NOT NULL,document BLOB NOT NULL,UNIQUE(owner,title))`,
		`CREATE TABLE IF NOT EXISTS actions (pack TEXT PRIMARY KEY,owner TEXT NOT NULL,document BLOB NOT NULL)`,
	} {
		if _, e = db.Exec(q); e != nil {
			db.Close()
			return nil, e
		}
	}
	return s, nil
}
func (s *Store) Close() error { return s.db.Close() }
func ID(owner, key string) string {
	h := sha256.Sum256([]byte(owner + "\x00" + key))
	return hex.EncodeToString(h[:12])
}
func validTitle(v string) bool {
	return strings.TrimSpace(v) == v && len(v) > 0 && len(v) <= 180 && !strings.ContainsRune(v, 0)
}
func (s *Store) get(owner, id string) (Pack, error) {
	var p Pack
	var b []byte
	e := s.db.QueryRow("SELECT document FROM packs WHERE owner=? AND id=?", owner, id).Scan(&b)
	if e != nil {
		return p, errors.New("material_not_found")
	}
	e = json.Unmarshal(b, &p)
	return p, e
}
func (s *Store) save(p Pack) error {
	p.Updated = time.Now().UTC()
	b, e := json.Marshal(p)
	if e != nil {
		return e
	}
	_, e = s.db.Exec(`INSERT INTO packs VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET state=excluded.state,updated=excluded.updated,document=excluded.document`, p.ID, p.Owner, p.State, p.Updated.UnixNano(), b)
	return e
}
func (s *Store) Get(owner, id string) (Pack, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.get(owner, id)
}
func (s *Store) active(owner string) (Pack, bool, error) {
	var b []byte
	e := s.db.QueryRow("SELECT document FROM packs WHERE owner=? AND state='collecting'", owner).Scan(&b)
	if errors.Is(e, sql.ErrNoRows) {
		return Pack{}, false, nil
	}
	if e != nil {
		return Pack{}, false, e
	}
	var p Pack
	e = json.Unmarshal(b, &p)
	return p, e == nil, e
}
func (s *Store) Active(owner string) (Pack, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active(owner)
}
func (s *Store) Begin(owner, key, title, conversation string) (Pack, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if owner == "" || key == "" || !validTitle(title) {
		return Pack{}, errors.New("invalid_material_title")
	}
	id := ID(owner, key)
	if p, e := s.get(owner, id); e == nil {
		return p, nil
	}
	if _, ok, e := s.active(owner); e != nil {
		return Pack{}, e
	} else if ok {
		return Pack{}, errors.New("material_collection_already_active")
	}
	var n int
	if e := s.db.QueryRow("SELECT count(*) FROM packs WHERE owner=?", owner).Scan(&n); e != nil {
		return Pack{}, e
	}
	if n >= 128 {
		return Pack{}, errors.New("material_capacity_reached")
	}
	now := time.Now().UTC()
	p := Pack{ID: id, Owner: owner, Title: title, Conversation: conversation, State: "collecting", Created: now, Updated: now, Entries: []Entry{}}
	return p, s.save(p)
}
func (s *Store) Append(owner, id string, v Entry) (Pack, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, e := s.get(owner, id)
	if e != nil {
		return p, e
	}
	if p.State != "collecting" {
		return p, errors.New("material_collection_closed")
	}
	if v.ID == "" || len(v.ID) > 512 || len(v.Text) > 256<<10 || len(v.Speaker) > 512 || len(v.Time) > 128 || len(v.QuoteID) > 512 {
		return p, errors.New("invalid_material_entry")
	}
	for _, old := range p.Entries {
		if old.ID == v.ID {
			a, _ := json.Marshal(old)
			b, _ := json.Marshal(v)
			if string(a) != string(b) {
				return p, errors.New("conflicting_material_entry")
			}
			return p, nil
		}
	}
	count, bytes := len(v.Files), len(v.Text)
	seen := map[string]bool{}
	for _, old := range p.Entries {
		bytes += len(old.Text)
		for _, f := range old.Files {
			seen[f.SHA256+"\x00"+f.Name] = true
		}
	}
	for _, f := range v.Files {
		if !files.ValidRef(f) {
			return p, errors.New("invalid_material_file")
		}
		seen[f.SHA256+"\x00"+f.Name] = true
	}
	count = len(seen)
	if len(p.Entries) >= MaxEntries || bytes > MaxTextBytes || count > MaxAttachments {
		return p, errors.New("material_pack_capacity_reached")
	}
	if v.Kind == "" {
		v.Kind = "text"
	}
	p.Entries = append(p.Entries, v)
	return p, s.save(p)
}
func (s *Store) Update(owner, id, state, job, instruction string) (Pack, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, e := s.get(owner, id)
	if e != nil {
		return p, e
	}
	if state != "collecting" && state != "saved" && state != "queued" && state != "submitted" {
		return p, errors.New("invalid_material_state")
	}
	if p.JobID != "" && (state == "collecting" || state == "saved") {
		return p, errors.New("material_already_submitted")
	}
	if len(instruction) > 6000 {
		return p, errors.New("material_instruction_too_long")
	}
	if state == "collecting" {
		if other, ok, e := s.active(owner); e != nil {
			return p, e
		} else if ok && other.ID != id {
			return p, errors.New("material_collection_already_active")
		}
	}
	if p.State == "queued" && state == "queued" && p.JobID != "" && p.JobID != job {
		return p, errors.New("material_already_queued")
	}
	p.State = state
	p.JobID = job
	p.Instruction = instruction
	return p, s.save(p)
}
func (s *Store) List(owner string) ([]Pack, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, e := s.db.Query("SELECT document FROM packs WHERE owner=? ORDER BY updated DESC,id LIMIT 128", owner)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	out := []Pack{}
	for r.Next() {
		var b []byte
		var p Pack
		if e = r.Scan(&b); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(b, &p); e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, r.Err()
}
func (s *Store) Resolve(owner, value string) (Pack, error) {
	ps, e := s.List(owner)
	if e != nil {
		return Pack{}, e
	}
	var found *Pack
	for _, p := range ps {
		if p.ID == value || p.Title == value || (len(value) >= 8 && strings.HasPrefix(p.ID, value)) {
			if found != nil {
				return Pack{}, errors.New("ambiguous_material")
			}
			v := p
			found = &v
		}
	}
	if found != nil {
		return *found, nil
	}
	return Pack{}, errors.New("material_not_found")
}

// PutBlob verifies the complete source before atomic installation. Names never form paths.
func (s *Store) PutBlob(ref files.Ref, r io.Reader) error {
	if !files.ValidRef(ref) {
		return errors.New("invalid_material_file")
	}
	if budget, e := files.DiskBudget(s.root); e != nil || ref.Size > budget {
		return errors.New("material_storage_full")
	}
	dir := filepath.Join(s.root, "blobs")
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	target := filepath.Join(dir, ref.SHA256)
	if st, e := os.Stat(target); e == nil && st.Size() == ref.Size {
		return nil
	}
	f, e := os.CreateTemp(dir, ".incoming-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(f, h), io.LimitReader(r, ref.Size+1))
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
	if n != ref.Size || hex.EncodeToString(h.Sum(nil)) != ref.SHA256 {
		return errors.New("material_integrity_failed")
	}
	return os.Rename(f.Name(), target)
}
func (s *Store) Blob(owner, pack, sha string) (*os.File, files.Ref, error) {
	p, e := s.Get(owner, pack)
	if e != nil {
		return nil, files.Ref{}, e
	}
	for _, v := range p.Entries {
		for _, f := range v.Files {
			if f.SHA256 == sha {
				b, e := os.Open(filepath.Join(s.root, "blobs", f.SHA256))
				return b, f, e
			}
		}
	}
	return nil, files.Ref{}, errors.New("material_file_not_found")
}
func (p Pack) Manifest() []byte { b, _ := json.MarshalIndent(p, "", "  "); return b }
func (p Pack) Preview() string {
	var b strings.Builder
	fmt.Fprintf(&b, "材料 %s · %s\n状态：%s；%d 条来源\n", p.ID[:8], p.Title, p.State, len(p.Entries))
	for i, v := range p.Entries {
		if i >= 12 {
			fmt.Fprintln(&b, "其余来源见管理页面。")
			break
		}
		r := []rune(v.Text)
		if len(r) > 100 {
			r = append(r[:100], '…')
		}
		fmt.Fprintf(&b, "\n[%d] %s %s\n%s\n", i+1, v.Speaker, v.Time, string(r))
		for _, f := range v.Files {
			fmt.Fprintln(&b, "附件："+f.Name)
		}
		for _, m := range v.Missing {
			fmt.Fprintln(&b, "未收到："+m)
		}
	}
	return b.String()
}

func (s *Store) Projects(owner string) ([]Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, e := s.db.Query("SELECT document FROM projects WHERE owner=? ORDER BY title,id", owner)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	out := []Project{}
	for r.Next() {
		var b []byte
		var p Project
		if e = r.Scan(&b); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(b, &p); e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, r.Err()
}
func (s *Store) SaveProject(p Project) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.Owner == "" || !validTitle(p.Title) || len(p.Conversations) > 64 || len(p.Topics) > 32 {
		return p, errors.New("invalid_project")
	}
	p.ID = ID(p.Owner, "project:"+p.Title)
	var old []byte
	e := s.db.QueryRow("SELECT document FROM projects WHERE id=? AND owner=?", p.ID, p.Owner).Scan(&old)
	if e == nil {
		var v Project
		if e = json.Unmarshal(old, &v); e != nil {
			return p, e
		}
		p.Created = v.Created
	} else if !errors.Is(e, sql.ErrNoRows) {
		return p, e
	} else {
		var n int
		if e = s.db.QueryRow("SELECT count(*) FROM projects WHERE owner=?", p.Owner).Scan(&n); e != nil {
			return p, e
		}
		if n >= 64 {
			return p, errors.New("project_capacity_reached")
		}
		p.Created = time.Now().UTC()
	}
	p.Updated = time.Now().UTC()
	b, e := json.Marshal(p)
	if e != nil {
		return p, e
	}
	_, e = s.db.Exec(`INSERT INTO projects VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET document=excluded.document`, p.ID, p.Owner, p.Title, b)
	return p, e
}
func (s *Store) Project(owner, value string) (Project, error) {
	ps, e := s.Projects(owner)
	if e != nil {
		return Project{}, e
	}
	for _, p := range ps {
		if p.ID == value || p.Title == value {
			return p, nil
		}
	}
	return Project{}, errors.New("project_not_found")
}
func (s *Store) Bind(owner, pack, project string) (Pack, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var exists int
	if e := s.db.QueryRow("SELECT count(*) FROM projects WHERE id=? AND owner=?", project, owner).Scan(&exists); e != nil || exists != 1 {
		return Pack{}, errors.New("project_not_found")
	}
	p, e := s.get(owner, pack)
	if e != nil {
		return p, e
	}
	p.Project = project
	return p, s.save(p)
}

func (s *Store) SetConversation(owner, id, conversation string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, e := s.get(owner, id)
	if e != nil {
		return e
	}
	if p.State == "queued" || p.State == "submitted" {
		return errors.New("material_already_submitted")
	}
	p.Conversation = conversation
	return s.save(p)
}
