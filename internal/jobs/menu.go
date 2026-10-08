package jobs

import (
	"encoding/json"
	"errors"
	"strconv"
	"time"
)

func (s *Store) TaskMenu(owner string) ([]Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := s.query("id IN (SELECT id FROM tasks WHERE owner=? AND verification=0 ORDER BY created DESC LIMIT 20)", owner)
	if s.readErr != nil {
		return nil, s.readErr
	}
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	ids := []string{}
	for _, j := range rows {
		ids = append(ids, j.ID)
	}
	b, _ := json.Marshal(ids)
	_, e := s.db.Exec("CREATE TABLE IF NOT EXISTS task_menus(owner TEXT PRIMARY KEY,expires INTEGER,ids BLOB)")
	if e == nil {
		_, e = s.db.Exec("INSERT INTO task_menus VALUES(?,?,?) ON CONFLICT(owner) DO UPDATE SET expires=excluded.expires,ids=excluded.ids", owner, time.Now().Add(10*time.Minute).Unix(), b)
	}
	return rows, e
}
func (s *Store) ResolveTask(owner, selector string) (Job, error) {
	if len(selector) >= 8 {
		return s.Find(owner, selector)
	}
	n, e := strconv.Atoi(selector)
	if e != nil {
		return s.Find(owner, selector)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var until int64
	var b []byte
	if e = s.db.QueryRow("SELECT expires,ids FROM task_menus WHERE owner=?", owner).Scan(&until, &b); e != nil || time.Now().Unix() > until {
		return Job{}, errors.New("task_menu_expired")
	}
	var ids []string
	if json.Unmarshal(b, &ids) != nil || n < 1 || n > len(ids) {
		return Job{}, errors.New("invalid_task_number")
	}
	j, ok := s.lookup(ids[n-1])
	if !ok || j.Owner != owner {
		return Job{}, errors.New("task_not_found")
	}
	return j, nil
}
