package jobs

import (
	"errors"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"reflect"
	"time"
)

type OutputIntent struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

func validIntents(v []OutputIntent) bool {
	if len(v) > files.MaxResults {
		return false
	}
	for _, f := range v {
		if !files.ValidRef(files.Ref{ID: "123456789012345678901234", Name: f.Name, SHA256: f.SHA256, Size: f.Size}) {
			return false
		}
	}
	return true
}
func (s *Store) CheckOutputUpload(id, lease string, index int, name, sha string, size int64, now time.Time) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.lookup(id)
	if !ok || lease == "" || j.Lease != lease || j.CancelRequested {
		return "", errors.New("invalid_output_lease")
	}
	if len(j.ExpectedOutputs) > 0 {
		if index < 0 || index >= len(j.ExpectedOutputs) || j.ExpectedOutputs[index] != (OutputIntent{Name: name, SHA256: sha, Size: size}) {
			return "", errors.New("output_intent_mismatch")
		}
		return j.Owner, nil
	}
	if j.Status != "running" || !now.Before(j.LeaseUntil) {
		return "", errors.New("invalid_output_lease")
	}
	return j.Owner, nil
}
func (s *Store) FinishOutputs(c Completion) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.lookup(c.ID)
	if !ok || j.Lease != c.Lease || c.Lease == "" || j.CancelRequested {
		return errors.New("invalid_output_lease")
	}
	if j.Status != "done" && j.Status != "delivered" {
		return errors.New("task_not_completed")
	}
	if len(c.Outputs) != len(j.ExpectedOutputs) || !files.ValidResults(c.Outputs) {
		return errors.New("output_intent_mismatch")
	}
	for i, f := range c.Outputs {
		if j.ExpectedOutputs[i] != (OutputIntent{Name: f.Name, SHA256: f.SHA256, Size: f.Size}) {
			return errors.New("output_intent_mismatch")
		}
	}
	if !j.OutputPending {
		if reflect.DeepEqual(j.Outputs, c.Outputs) {
			return nil
		}
		return errors.New("conflicting_outputs")
	}
	j.Outputs = append([]files.Ref(nil), c.Outputs...)
	j.OutputPending = false
	j.MediaPackageRequired = len(c.Outputs) > 1
	j.Status = "done"
	return s.save(j)
}
