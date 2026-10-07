// Package maintenance implements deterministic operations; it never calls a model.
package maintenance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var Beijing = time.FixedZone("Asia/Shanghai", 8*3600)

type Report struct {
	ID          string    `json:"id"`
	Host        string    `json:"host"`
	Kind        string    `json:"kind"`
	Day         string    `json:"day"`
	Text        string    `json:"text"`
	Created     time.Time `json:"created"`
	Accepted    time.Time `json:"accepted,omitempty"`
	Attempts    int       `json:"attempts,omitempty"`
	NextAttempt time.Time `json:"next_attempt,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
}

func NewReport(host, kind, day, text string) Report {
	h := sha256.Sum256([]byte(host + "\n" + kind + "\n" + day + "\n" + text))
	return Report{ID: hex.EncodeToString(h[:12]), Host: host, Kind: kind, Day: day, Text: text, Created: time.Now().UTC()}
}
func (r Report) valid() bool {
	if len(r.ID) != 24 || (r.Host != "cloud" && r.Host != "campus") || len([]rune(r.Text)) > 5000 || r.Text == "" {
		return false
	}
	if _, e := hex.DecodeString(r.ID); e != nil {
		return false
	}
	if _, e := time.Parse("2006-01-02", r.Day); e != nil {
		return false
	}
	return r.Kind == "usage" || r.Kind == "accounts" || r.Kind == "updates" || r.Kind == "status"
}

type Store struct {
	mu  sync.Mutex
	Dir string
}

func Open(dir string) (*Store, error) {
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	return &Store{Dir: dir}, nil
}
func AtomicJSON(path string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".maintenance-*")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
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
	return os.Rename(tmp, path)
}
func (s *Store) Put(r Report) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !r.valid() {
		return errors.New("invalid_report")
	}
	p := filepath.Join(s.Dir, r.ID+".json")
	if _, e := os.Stat(p); e == nil {
		return nil
	}
	r.Accepted = time.Time{}
	r.Attempts = 0
	r.NextAttempt = time.Time{}
	r.LastError = ""
	return AtomicJSON(p, r)
}
func (s *Store) list() []Report {
	entries, _ := os.ReadDir(s.Dir)
	var out []Report
	for _, f := range entries {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		b, e := os.ReadFile(filepath.Join(s.Dir, f.Name()))
		var r Report
		if e == nil && json.Unmarshal(b, &r) == nil && r.valid() {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out
}
func (s *Store) Pending(now time.Time) []Report {
	s.mu.Lock()
	defer s.mu.Unlock()
	all := s.list()
	latest := map[string]string{}
	for _, r := range all {
		latest[r.Host+":"+r.Kind] = r.ID
	}
	var out []Report
	for _, r := range all {
		if latest[r.Host+":"+r.Kind] == r.ID && r.Accepted.IsZero() && !now.Before(r.NextAttempt) {
			out = append(out, r)
		}
	}
	return out
}
func (s *Store) Receipt(id string, accepted bool, category string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(id) != 24 {
		return errors.New("invalid_report")
	}
	if _, e := hex.DecodeString(id); e != nil {
		return errors.New("invalid_report")
	}
	p := filepath.Join(s.Dir, id+".json")
	b, e := os.ReadFile(p)
	var r Report
	if e != nil || json.Unmarshal(b, &r) != nil {
		return errors.New("report_unavailable")
	}
	r.Attempts++
	if accepted {
		r.Accepted = now
		r.LastError = ""
	} else {
		r.NextAttempt = now.Add(30 * time.Minute)
		r.LastError = category
	}
	return AtomicJSON(p, r)
}
func (s *Store) Latest(kind string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	all := s.list()
	latest := map[string]Report{}
	for _, r := range all {
		if kind == "" || r.Kind == kind {
			latest[r.Host+":"+r.Kind] = r
		}
	}
	var keys []string
	for k := range latest {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		r := latest[k]
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(r.Text)
	}
	if b.Len() == 0 {
		return "暂时还没有这类运维报告。每天北京时间 07:00 更新和账号检查，00:00 统计前一天用量。"
	}
	return b.String()
}
