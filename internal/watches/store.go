// Package watches mirrors an external task without adding it to the AI queue.
package watches

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/conversations"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
)

type Update struct {
	Source       string    `json:"source,omitempty"`
	Conversation string    `json:"conversation"`
	Thread       string    `json:"thread"`
	Title        string    `json:"title"`
	State        string    `json:"state"`
	Text         string    `json:"text"`
	Sequence     uint64    `json:"sequence"`
	Observed     time.Time `json:"observed"`
}
type Event struct {
	At    time.Time `json:"at"`
	State string    `json:"state"`
	Text  string    `json:"text"`
}
type Watch struct {
	Update
	ID     string    `json:"id"`
	Owner  string    `json:"-"`
	Synced time.Time `json:"synced"`
	Events []Event   `json:"events"`
}
type savedWatch struct {
	Watch
	Owner string `json:"owner"`
}
type Store struct {
	mu   sync.Mutex
	path string
	rows map[string]savedWatch
}

func Open(path string) (*Store, error) {
	s := &Store{path: path, rows: map[string]savedWatch{}}
	b, e := metadb.ReadJSON(path)
	if e == nil {
		if json.Unmarshal(b, &s.rows) != nil || s.rows == nil || len(s.rows) > 64 {
			return nil, errors.New("invalid_watches")
		}
		for cid, v := range s.rows {
			if cid != v.Conversation || !valid(v.Update) || v.Owner == "" || v.ID != watchID(v.Owner, cid, v.Thread) {
				return nil, errors.New("invalid_watches")
			}
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	return s, nil
}
func watchID(owner, cid, thread string) string {
	h := sha256.Sum256([]byte(owner + "\x00" + cid + "\x00" + thread))
	return hex.EncodeToString(h[:12])
}
func valid(u Update) bool {
	return (u.Source == "" || u.Source == "training") && conversations.ValidID(u.Conversation) && conversations.ValidThread(u.Thread) && u.Title != "" && len(u.Title) <= 512 && len(u.Text) <= 24000 && !u.Observed.IsZero() && (u.State == "running" || u.State == "done" || u.State == "stopped" || u.State == "waiting_user" || u.State == "failed")
}
func (s *Store) Put(owner string, u Update, now time.Time) (Watch, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if owner == "" || !valid(u) || u.Observed.After(now.Add(time.Minute)) {
		return Watch{}, false, errors.New("invalid_watch")
	}
	old, exists := s.rows[u.Conversation]
	if exists && (old.Owner != owner || old.Thread != u.Thread) {
		return Watch{}, false, errors.New("watch_binding_conflict")
	}
	if exists && (u.Sequence < old.Sequence || u.Observed.Before(old.Observed)) {
		return Watch{}, false, errors.New("stale_watch")
	}
	if exists && u.Sequence == old.Sequence && (u.Source != old.Source || u.State != old.State || u.Text != old.Text || u.Title != old.Title || !u.Observed.Equal(old.Observed)) {
		return Watch{}, false, errors.New("conflicting_watch")
	}
	if !exists && len(s.rows) >= 64 {
		return Watch{}, false, errors.New("too_many_watches")
	}
	v := savedWatch{Watch: Watch{Update: u, ID: watchID(owner, u.Conversation, u.Thread), Synced: now.UTC(), Events: append([]Event{}, old.Events...)}, Owner: owner}
	changed := !exists || old.State != u.State || old.Text != u.Text
	if changed {
		v.Events = append(v.Events, Event{At: u.Observed, State: u.State, Text: u.Text})
		if len(v.Events) > 40 {
			v.Events = v.Events[len(v.Events)-40:]
		}
	}
	next := make(map[string]savedWatch, len(s.rows)+1)
	for k, v := range s.rows {
		next[k] = v
	}
	next[u.Conversation] = v
	if e := metadb.WriteJSON(s.path, next); e != nil {
		return Watch{}, false, e
	}
	s.rows = next
	w := v.Watch
	w.Owner = owner
	return w, changed, nil
}
func (s *Store) Current(owner, cid string) (Watch, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.rows[cid]
	w := v.Watch
	w.Owner = v.Owner
	w.Events = append([]Event{}, w.Events...)
	return w, ok && v.Owner == owner
}
func (s *Store) Find(id string) (Watch, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range s.rows {
		if v.ID == id {
			w := v.Watch
			w.Owner = v.Owner
			w.Events = append([]Event{}, w.Events...)
			return w, true
		}
	}
	return Watch{}, false
}
func (w Watch) Status(now time.Time) string {
	label := map[string]string{"running": "Codex 正在执行", "done": "Codex 本轮已回复", "stopped": "Codex 本轮已停止", "waiting_user": "Codex 等待你回答", "failed": "Codex 本轮失败"}[w.State]
	if w.Source == "training" {
		label = map[string]string{"running": "服务器训练运行中", "done": "服务器训练队列已完成", "failed": "服务器训练队列结束，含失败实验", "stopped": "服务器训练已停止"}[w.State]
	}
	if now.Sub(w.Synced) > 90*time.Second {
		label += "；同步已中断，以下为最后记录"
	}
	return label
}
func (w Watch) Summary(now time.Time) string {
	zone := time.FixedZone("Asia/Shanghai", 8*3600)
	return strings.TrimSpace(w.Title + "\n" + w.Status(now) + "\n进度更新：" + w.Observed.In(zone).Format("01-02 15:04:05") + "\n同步时间：" + w.Synced.In(zone).Format("01-02 15:04:05") + "\n\n" + w.Text)
}
