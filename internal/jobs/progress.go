package jobs

import (
	"errors"
	"time"
	"unicode/utf8"
)

type ProgressUpdate struct {
	ID       string `json:"id"`
	Lease    string `json:"lease"`
	Sequence uint64 `json:"sequence"`
	Text     string `json:"text"`
}

func (s *Store) UpdateProgress(u ProgressUpdate, now time.Time) error {
	if u.Sequence == 0 || len(u.Text) > 256<<10 || !utf8.ValidString(u.Text) {
		return errors.New("invalid_progress")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.lookup(u.ID)
	if !ok || j.Status != "running" || u.Lease == "" || u.Lease != j.Lease || !now.Before(j.LeaseUntil) {
		return errors.New("invalid_progress_lease")
	}
	if u.Sequence < j.ProgressSequence {
		return errors.New("stale_progress_sequence")
	}
	if u.Sequence == j.ProgressSequence {
		if u.Text == j.Progress {
			return nil
		}
		return errors.New("conflicting_progress")
	}
	j.Progress = u.Text
	j.ProgressSequence = u.Sequence
	j.ProgressUpdated = now
	return s.save(j)
}
