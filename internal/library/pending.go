package library

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// RetireStaging only removes cloud copies after campus ACK and job enqueue.
func (s *Store) RetireStaging(owner, iid string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, e := s.Intake(owner, iid)
	if e != nil {
		return e
	}
	if v.State != "queued" {
		return nil
	}
	assets := v.Assets
	v.Assets = nil
	b, _ := json.Marshal(v)
	if _, e = s.db.Exec("UPDATE intakes SET document=? WHERE owner=? AND id=?", b, owner, iid); e != nil {
		return e
	}
	rows, e := s.db.Query("SELECT document FROM intakes WHERE state IN ('draft','syncing','canceled')")
	if e != nil {
		return e
	}
	used := map[string]bool{}
	for rows.Next() {
		var raw []byte
		var in Intake
		if rows.Scan(&raw) == nil && decode(raw, &in) == nil {
			for _, a := range in.Assets {
				used[a.SHA256] = true
			}
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, a := range assets {
		if !used[a.SHA256] {
			if e = os.Remove(filepath.Join(s.root, "blobs", a.SHA256)); e != nil && !os.IsNotExist(e) {
				return e
			}
		}
	}
	return nil
}

func (s *Store) Pending(owner string) ([]Intake, error) {
	rows, e := s.db.Query("SELECT document FROM intakes WHERE owner=? AND state='syncing'", owner)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Intake{}
	for rows.Next() {
		var b []byte
		var v Intake
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		if e = decode(b, &v); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
