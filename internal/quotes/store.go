// Package quotes stores reference material, never authentication or CDN keys.
package quotes

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"io"
	"os"
	"path/filepath"
	"sort"
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
	mu    sync.Mutex
	path  string
	state state
	now   func() time.Time
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
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	s := &Store{path: path, now: time.Now, state: state{Version: 1, Records: map[string]record{}}}
	f, e := os.Open(path)
	if os.IsNotExist(e) {
		return s, nil
	}
	if e != nil {
		return nil, e
	}
	defer f.Close()
	raw, e := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if e != nil || len(raw) > maxBytes || json.Unmarshal(raw, &s.state) != nil || s.state.Version != 1 || s.state.Records == nil || len(s.state.Records) > maxRecords {
		return nil, errors.New("invalid_quote_cache")
	}
	for k, r := range s.state.Records {
		if len(r.Scope) != 64 || len(r.ID) == 0 || len(r.ID) > 256 || k != key(r.Scope, r.ID) || r.Created.IsZero() || !validContent(r.Content) {
			return nil, errors.New("invalid_quote_record")
		}
	}
	return s, nil
}
func (s *Store) Get(bot, peer, id string) (Content, bool) {
	if s == nil || bot == "" || peer == "" || id == "" {
		return Content{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.state.Records[key(scope(bot, peer), id)]
	if !ok || s.now().Sub(r.Created) > retention {
		return Content{}, false
	}
	c := r.Content
	c.Attachments = append([]Attachment(nil), c.Attachments...)
	return c, true
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
	next := state{Version: 1, Records: make(map[string]record)}
	now := s.now()
	for k, r := range s.state.Records {
		if now.Sub(r.Created) <= retention {
			next.Records[k] = r
		}
	}
	sc := scope(bot, peer)
	k := key(sc, id)
	created := now
	if old, ok := next.Records[k]; ok {
		created = old.Created
	}
	c.Attachments = append([]Attachment(nil), c.Attachments...)
	next.Records[k] = record{sc, id, created, c}
	ordered := make([]string, 0, len(next.Records))
	for k := range next.Records {
		ordered = append(ordered, k)
	}
	sort.Slice(ordered, func(i, j int) bool {
		a, b := next.Records[ordered[i]], next.Records[ordered[j]]
		if a.Created.Equal(b.Created) {
			return ordered[i] < ordered[j]
		}
		return a.Created.Before(b.Created)
	})
	// Keep the new record when trimming; eviction never invents a reference.
	for len(next.Records) > maxRecords {
		victim := ordered[0]
		ordered = ordered[1:]
		if victim != k {
			delete(next.Records, victim)
		}
	}
	raw, e := json.Marshal(next)
	if e != nil {
		return e
	}
	for len(raw) > maxBytes && len(ordered) > 0 {
		victim := ordered[0]
		ordered = ordered[1:]
		if victim == k {
			continue
		}
		delete(next.Records, victim)
		raw, e = json.Marshal(next)
		if e != nil {
			return e
		}
	}
	if len(raw) > maxBytes {
		return errors.New("quote_cache_full")
	}
	f, e := os.CreateTemp(filepath.Dir(s.path), ".quotes-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(raw)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e == nil {
		e = os.Rename(f.Name(), s.path)
	}
	if e == nil {
		s.state = next
	}
	return e
}
