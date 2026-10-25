package files

import "time"

func (s *Store) alive(r record) bool {
	return s.now().Sub(r.Created) <= 7*24*time.Hour || s.now().Before(r.RetainUntil)
}

// Retain protects references from queued/running jobs and unfinished deliveries.
// After references stop being renewed, normal expiry resumes automatically.
func (s *Store) Retain(ids map[string]bool, until time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.copyState()
	changed := false
	for id, r := range next.Files {
		if ids[id] && r.RetainUntil.Before(until) {
			r.RetainUntil = until
			next.Files[id] = r
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return s.save(next)
}
