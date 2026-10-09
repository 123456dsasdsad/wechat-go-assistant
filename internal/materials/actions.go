package materials

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

type Action struct {
	ID       string `json:"id"`
	Text     string `json:"text"`
	Assignee string `json:"assignee"`
	Deadline string `json:"deadline"`
	Source   int    `json:"source_entry"`
	Excerpt  string `json:"excerpt"`
	Status   string `json:"status"`
}

func (s *Store) Actions(owner, pack string) ([]Action, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, e := s.get(owner, pack); e != nil {
		return nil, e
	}
	var raw []byte
	e := s.db.QueryRow("SELECT document FROM actions WHERE pack=? AND owner=?", pack, owner).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return []Action{}, nil
	}
	if e != nil {
		return nil, e
	}
	var rows []Action
	e = json.Unmarshal(raw, &rows)
	return rows, e
}

// Import once. Human completion states survive refreshes/restarts.
func (s *Store) ImportActions(owner, pack string, raw []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, e := s.get(owner, pack)
	if e != nil {
		return e
	}
	var rows []Action
	if len(raw) > 256<<10 || json.Unmarshal(raw, &rows) != nil || len(rows) > 200 {
		return errors.New("invalid_action_list")
	}
	for i := range rows {
		a := &rows[i]
		if a.Source < 1 || a.Source > len(p.Entries) || strings.TrimSpace(a.Text) == "" || len(a.Text) > 2000 || a.Excerpt == "" || len(a.Excerpt) > 500 || !strings.Contains(p.Entries[a.Source-1].Text, a.Excerpt) {
			return errors.New("action_source_unverified")
		}
		a.ID = ID(pack, a.Text+"\x00"+a.Excerpt)
		a.Status = "todo"
	}
	raw, e = json.Marshal(rows)
	if e != nil {
		return e
	}
	_, e = s.db.Exec("INSERT INTO actions(pack,owner,document) VALUES(?,?,?) ON CONFLICT(pack) DO NOTHING", pack, owner, raw)
	return e
}
func (s *Store) ActionStatus(owner, pack, id, status string) error {
	if status != "todo" && status != "done" {
		return errors.New("invalid_action_status")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, e := s.get(owner, pack); e != nil {
		return e
	}
	var raw []byte
	if e := s.db.QueryRow("SELECT document FROM actions WHERE pack=? AND owner=?", pack, owner).Scan(&raw); e != nil {
		return e
	}
	var rows []Action
	if e := json.Unmarshal(raw, &rows); e != nil {
		return e
	}
	found := false
	for i := range rows {
		if rows[i].ID == id {
			rows[i].Status = status
			found = true
		}
	}
	if !found {
		return errors.New("action_not_found")
	}
	raw, e := json.Marshal(rows)
	if e != nil {
		return e
	}
	_, e = s.db.Exec("UPDATE actions SET document=? WHERE pack=? AND owner=?", raw, pack, owner)
	return e
}
