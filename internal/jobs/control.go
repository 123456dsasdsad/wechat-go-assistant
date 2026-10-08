package jobs

import (
	"errors"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/models"
	"strings"
	"time"
)

// Cancel retains the running lease until the worker has stopped its process group.
func (s *Store) Cancel(owner, id string) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.lookup(id)
	if !ok || j.Owner != owner {
		return Job{}, errors.New("task_not_found")
	}
	if j.Status != "queued" && j.Status != "running" {
		if j.Training.State == "running" {
			j.Training.Command = "stop"
			j.Training.CommandID = time.Now().UTC().Format("20060102T150405.000000000")
			return j, s.save(j)
		}
		return j, nil
	}
	j.CancelRequested = true
	cancelQuestions(&j)
	for i := range j.Supplements {
		if j.Supplements[i].State == "pending" {
			j.Supplements[i].State = "canceled"
		}
	}
	if j.Status == "queued" {
		j.Status = "done"
		j.Error = "user_canceled"
	}
	return j, s.save(j)
}
func (s *Store) Cancelled(id, lease string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.lookup(id)
	if !ok || j.Lease != lease || lease == "" {
		return false, errors.New("invalid_task_lease")
	}
	return j.CancelRequested, nil
}
func (s *Store) Find(owner, prefix string) (Job, error) {
	if len(prefix) < 8 {
		return Job{}, errors.New("task_id_too_short")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := s.query("owner=? AND id LIKE ?", owner, prefix+"%")
	if s.readErr != nil {
		return Job{}, s.readErr
	}
	if len(rows) != 1 {
		return Job{}, errors.New("task_not_found_or_ambiguous")
	}
	return rows[0], nil
}
func (s *Store) Retry(owner, id, source, reply string) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	parent, ok := s.lookup(id)
	if !ok || parent.Owner != owner {
		return Job{}, errors.New("task_not_found")
	}
	if parent.Status == "running" || parent.Status == "queued" || parent.Error == "" {
		return Job{}, errors.New("only_failed_tasks_can_retry")
	}
	j, e := s.enqueue("retry:"+source, parent.Input, owner, reply, models.Choice{Model: parent.Model, Effort: parent.Effort}, parent.Attachments, parent.ConversationID)
	if e != nil {
		return Job{}, e
	}
	if j.ParentID == "" && j.Status == "queued" {
		j.ParentID = parent.ID
		j.Kind = parent.Kind
		j.LibraryID = parent.LibraryID
		j.LibraryTopics = append([]string(nil), parent.LibraryTopics...)
		j.Memory = parent.Memory
		j.Project = parent.Project
		j.BudgetUSD = parent.BudgetUSD
		e = s.save(j)
	}
	return j, e
}
func (s *Store) Health() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.db.Ping(); err != nil {
		return err
	}
	var n int
	if err := s.db.QueryRow("SELECT count(*) FROM tasks").Scan(&n); err != nil {
		return err
	}
	return s.readErr
}
func (s *Store) Search(owner, query string, limit int) []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit < 1 || limit > 100 {
		limit = 100
	}
	// User search is literal, never an SQL wildcard expression.
	pattern := "%" + strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(query, "\\", "\\\\"), "%", "\\%"), "_", "\\_") + "%"
	return s.query("id IN (SELECT id FROM tasks WHERE owner=? AND CAST(document AS TEXT) LIKE ? ESCAPE '\\' ORDER BY created DESC LIMIT ?)", owner, pattern, limit)
}
func (s *Store) CancelExpired(now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.query("status='running'") {
		if j.CancelRequested && !now.Before(j.LeaseUntil) {
			j.Status = "done"
			j.Error = "user_canceled"
			if e := s.save(j); e != nil {
				return e
			}
		}
	}
	return s.readErr
}
