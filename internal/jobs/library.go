package jobs

import (
	"errors"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/models"
)

// EnqueueLibrary initializes the specialized job atomically with its enqueue.
// It uses an isolated conversation lane rather than a user's native Codex thread.
func (s *Store) EnqueueLibrary(source, input, owner, reply, kind, target string, choice models.Choice, budget float64) (Job, error) {
	if (kind != "library_intake" && kind != "review_update") || target == "" || len(target) > 240 {
		return Job{}, errors.New("invalid_library_task")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j, e := s.enqueue("library:"+source, input, owner, reply, choice, nil, "")
	if e != nil {
		return j, e
	}
	if !j.Initialized {
		j.Kind = kind
		j.LibraryID = target
		j.BudgetUSD = budget
		j.Initialized = true
		e = s.save(j)
	}
	return j, e
}
