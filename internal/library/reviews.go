package library

import (
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func number(n int64) string { return strconv.FormatInt(n, 10) }

func (s *Store) Topics(owner string) ([]Topic, error) {
	rows, e := s.db.Query(`SELECT name,revision,version,dirty,error,notes,(SELECT count(*) FROM memberships m WHERE m.owner=t.owner AND m.topic=t.name) FROM topics t WHERE owner=? ORDER BY name`, owner)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Topic{}
	for rows.Next() {
		var t Topic
		if e = rows.Scan(&t.Name, &t.Revision, &t.Version, &t.Dirty, &t.LastError, &t.Notes, &t.Count); e != nil {
			return nil, e
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s *Store) Review(owner, topic string, version int64) (Review, error) {
	if version == 0 {
		if e := s.db.QueryRow("SELECT version FROM topics WHERE owner=? AND name=?", owner, topic).Scan(&version); e != nil {
			return Review{}, ErrNotFound
		}
	}
	var b []byte
	e := s.db.QueryRow("SELECT document FROM reviews WHERE owner=? AND topic=? AND version=?", owner, topic, version).Scan(&b)
	if errors.Is(e, sql.ErrNoRows) {
		return Review{}, ErrNotFound
	}
	var r Review
	if e == nil {
		e = decode(b, &r)
	}
	return r, e
}
func (s *Store) Snapshot(owner, topic string) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var snap Snapshot
	e := s.db.QueryRow("SELECT name,revision,version,dirty,error,notes FROM topics WHERE owner=? AND name=?", owner, topic).Scan(&snap.Topic.Name, &snap.Topic.Revision, &snap.Topic.Version, &snap.Topic.Dirty, &snap.Topic.LastError, &snap.Topic.Notes)
	if e != nil {
		return snap, ErrNotFound
	}
	rows, e := s.db.Query("SELECT document FROM materials WHERE owner=? AND deleted=0 AND id IN (SELECT material FROM memberships WHERE owner=? AND topic=?) ORDER BY id", owner, owner, topic)
	if e != nil {
		return snap, e
	}
	for rows.Next() {
		var b []byte
		var m Material
		if e = rows.Scan(&b); e != nil {
			rows.Close()
			return snap, e
		}
		if e = decode(b, &m); e != nil {
			rows.Close()
			return snap, e
		}
		snap.Materials = append(snap.Materials, m)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return snap, e
	}
	snap.Previous, _ = s.Review(owner, topic, 0)
	return snap, nil
}

// Publish checks the current corpus and base version within the publishing transaction.
func (s *Store) Publish(owner string, snap Snapshot, r Review) (Review, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Topic != snap.Topic.Name {
		return r, errors.New("review_topic_mismatch")
	}
	allowed := map[int64]bool{}
	locked, _ := s.Locked(owner, snap.Topic.Name)
	isLocked := map[string]string{}
	for _, sec := range locked {
		isLocked[sec.Name] = sec.Text
	}
	for _, m := range snap.Materials {
		allowed[m.ID] = true
	}
	for _, sec := range r.Sections {
		if text, ok := isLocked[sec.Name]; ok {
			if text != sec.Text {
				return r, errors.New("locked_section_changed")
			}
			continue
		}
		if sec.Name == "" || len(sec.Text) > 256<<10 {
			return r, errors.New("invalid_review_section")
		}
		if sec.Text != "" && len(sec.MaterialIDs) == 0 && sec.Name != "范围与覆盖" {
			return r, errors.New("review_citation_missing")
		}
		for _, id := range sec.MaterialIDs {
			if !allowed[id] {
				return r, errors.New("review_foreign_citation")
			}
		}
		listed := map[int64]bool{}
		for _, id := range sec.MaterialIDs {
			listed[id] = true
		}
		for _, match := range inlineCitation.FindAllStringSubmatch(sec.Text, -1) {
			id, _ := strconv.ParseInt(match[1], 10, 64)
			if !allowed[id] || !listed[id] {
				return r, errors.New("review_inline_citation_invalid")
			}
		}
	}
	if len(r.Sections) == 0 {
		return r, errors.New("empty_review")
	}
	tx, e := s.db.Begin()
	if e != nil {
		return r, e
	}
	defer tx.Rollback()
	var rev, version int64
	if e = tx.QueryRow("SELECT revision,version FROM topics WHERE owner=? AND name=?", owner, r.Topic).Scan(&rev, &version); e != nil {
		return r, e
	}
	if rev != snap.Topic.Revision || version != snap.Topic.Version {
		return r, ErrStale
	}
	r.Version = version + 1
	r.CorpusRevision = rev
	r.Notes = snap.Topic.Notes
	r.Created = time.Now().UTC()
	r.References = nil
	for _, m := range snap.Materials {
		r.References = append(r.References, Material{ID: m.ID, Title: m.Title, Revision: m.Revision, Sources: m.Sources})
	}
	b, _ := json.Marshal(r)
	if _, e = tx.Exec("INSERT INTO reviews VALUES(?,?,?,?)", owner, r.Topic, r.Version, b); e != nil {
		return r, e
	}
	if _, e = tx.Exec("UPDATE topics SET version=?,dirty=0,error='' WHERE owner=? AND name=?", r.Version, owner, r.Topic); e != nil {
		return r, e
	}
	return r, tx.Commit()
}

var inlineCitation = regexp.MustCompile(`\[资料\s*(\d+)\]`)

func (s *Store) ReviewError(owner, topic, text string) error {
	_, e := s.db.Exec("UPDATE topics SET error=? WHERE owner=? AND name=?", text, owner, topic)
	return e
}
func (s *Store) Notes(owner, topic, text string) error {
	if len(text) > 64<<10 {
		return errors.New("notes_too_large")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result, e := s.db.Exec("UPDATE topics SET notes=?,revision=revision+1,dirty=1 WHERE owner=? AND name=?", text, owner, topic)
	if e != nil {
		return e
	}
	n, e := result.RowsAffected()
	if e == nil && n == 0 {
		return ErrNotFound
	}
	return e
}
func Markdown(r Review, materials []Material) string {
	var b strings.Builder
	b.WriteString("# " + r.Topic + "\n\n")
	for _, sec := range r.Sections {
		b.WriteString("## " + sec.Name + "\n\n" + sec.Text + "\n\n")
	}
	b.WriteString("## 我的笔记\n\n" + r.Notes + "\n\n## 参考资料\n\n")
	for _, m := range materials {
		b.WriteString("- 资料 " + number(m.ID) + "：" + m.Title + "\n")
		for _, s := range m.Sources {
			b.WriteString("  " + s.URL + " · " + s.ReadingScope + "\n")
		}
	}
	return b.String()
}
