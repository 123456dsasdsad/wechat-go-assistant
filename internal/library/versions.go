package library

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Locked keeps user-owned sections outside automatic synthesis and bumps the corpus epoch.
func (s *Store) Locked(owner, topic string) ([]Section, error) {
	var raw []byte
	e := s.db.QueryRow("SELECT value FROM documents WHERE key=?", "locked:"+owner+":"+topic).Scan(&raw)
	if e != nil {
		return []Section{}, nil
	}
	var sec []Section
	e = decode(raw, &sec)
	return sec, e
}
func (s *Store) Lock(owner, topic, name, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validName(topic) || !validName(name) || len(text) > 256<<10 {
		return errors.New("invalid_locked_section")
	}
	sec, _ := s.Locked(owner, topic)
	found := false
	for i := range sec {
		if sec[i].Name == name {
			sec[i].Text = text
			found = true
		}
	}
	if !found {
		sec = append(sec, Section{Name: name, Text: text})
	}
	raw, _ := json.Marshal(sec)
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec("INSERT OR REPLACE INTO documents VALUES(?,?)", "locked:"+owner+":"+topic, raw); e != nil {
		return e
	}
	if e = dirty(tx, owner, topic); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) Import(owner, topic, text string) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("empty_import")
	}
	return s.Lock(owner, topic, "导入综述（人工内容，引用待核实）", text)
}
func (s *Store) MergeLocked(owner, topic string, r Review) (Review, error) {
	secs, e := s.Locked(owner, topic)
	if e != nil {
		return r, e
	}
	for _, locked := range secs {
		found := false
		for i := range r.Sections {
			if r.Sections[i].Name == locked.Name {
				r.Sections[i] = locked
				found = true
			}
		}
		if !found {
			r.Sections = append(r.Sections, locked)
		}
	}
	return r, nil
}

// Restore creates a new version and preserves the historical text as a locked block.
func (s *Store) RestoreReview(owner, topic string, version int64) (Review, error) {
	old, e := s.Review(owner, topic, version)
	if e != nil {
		return Review{}, e
	}
	name := fmt.Sprintf("恢复的历史综述（v%d，引用待重新核实）", version)
	if e = s.Lock(owner, topic, name, Markdown(old, old.References)); e != nil {
		return Review{}, e
	}
	snap, e := s.Snapshot(owner, topic)
	if e != nil {
		return Review{}, e
	}
	r, e := s.MergeLocked(owner, topic, Review{Topic: topic, Sections: []Section{{Name: "范围与覆盖", Text: fmt.Sprintf("恢复了历史版本 v%d，原文锁定保存。当前类别有 %d 条资料，新增资料尚未合入恢复原文；可点击更新综述整理现有证据。", version, len(snap.Materials))}}, Changes: []string{fmt.Sprintf("恢复 v%d 为新的锁定版本，保留全部历史", version)}})
	if e != nil {
		return Review{}, e
	}
	return s.Publish(owner, snap, r)
}
