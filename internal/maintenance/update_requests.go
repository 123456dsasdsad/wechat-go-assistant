package maintenance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// UpdateRequest contains no prompt or executable. Only the native maintenance
// runners may consume it, using their existing managed-software allowlist.
type UpdateRequest struct {
	ID        string    `json:"id"`
	Host      string    `json:"host"`
	Created   time.Time `json:"created"`
	Completed time.Time `json:"completed,omitempty"`
}

func (s *Store) RequestUpdates(message string, hosts ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if message == "" || len(hosts) == 0 {
		return errors.New("invalid_update_request")
	}
	for _, host := range hosts {
		if host != "cloud" && host != "campus" {
			return errors.New("invalid_update_host")
		}
	}
	for _, host := range hosts {
		h := sha256.Sum256([]byte(host + "\nupdates\n" + message))
		id := hex.EncodeToString(h[:12])
		p := filepath.Join(s.Dir, "update-requests", id+".json")
		if _, e := os.Stat(p); e == nil {
			continue
		}
		if e := AtomicJSON(p, UpdateRequest{ID: id, Host: host, Created: time.Now().UTC()}); e != nil {
			return e
		}
	}
	return nil
}

func (s *Store) PendingUpdates(host string) []UpdateRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []UpdateRequest
	if host != "cloud" && host != "campus" {
		return out
	}
	entries, _ := os.ReadDir(filepath.Join(s.Dir, "update-requests"))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		b, e := os.ReadFile(filepath.Join(s.Dir, "update-requests", entry.Name()))
		var r UpdateRequest
		if e == nil && json.Unmarshal(b, &r) == nil && r.Host == host && r.Completed.IsZero() && entry.Name() == r.ID+".json" {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out
}

func (s *Store) CompleteUpdate(host, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if (host != "cloud" && host != "campus") || len(id) != 24 {
		return errors.New("invalid_update_request")
	}
	if _, e := hex.DecodeString(id); e != nil {
		return errors.New("invalid_update_request")
	}
	p := filepath.Join(s.Dir, "update-requests", id+".json")
	b, e := os.ReadFile(p)
	var r UpdateRequest
	if e != nil || json.Unmarshal(b, &r) != nil || r.ID != id || r.Host != host {
		return errors.New("update_request_unavailable")
	}
	if !r.Completed.IsZero() {
		return nil
	}
	r.Completed = time.Now().UTC()
	return AtomicJSON(p, r)
}
