package jobs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"strings"
)

func (s *Store) BeginTrainingOutputs(c Completion) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.lookup(c.ID)
	if !ok || j.Lease != c.Lease || c.Lease == "" || j.Training.State != "completed" {
		return errors.New("training_not_completed")
	}
	if !validIntents(c.ExpectedOutputs) || len(c.Result) > 8192 {
		return errors.New("invalid_training_outputs")
	}
	raw, _ := json.Marshal(c.ExpectedOutputs)
	h := sha256.Sum256(append(raw, []byte(c.Result)...))
	receipt := hex.EncodeToString(h[:])
	if j.TrainingResult == receipt {
		return nil
	}
	if j.Status != "done" && j.Status != "delivered" {
		return errors.New("task_not_completed")
	}
	if j.CancelRequested {
		return errors.New("task_canceled")
	}
	if j.OutputPending {
		return errors.New("previous_outputs_pending")
	}
	if len(j.Outputs) > len(c.ExpectedOutputs) {
		return errors.New("training_outputs_must_preserve_previous")
	}
	for i, f := range j.Outputs {
		if c.ExpectedOutputs[i] != (OutputIntent{Name: f.Name, SHA256: f.SHA256, Size: f.Size}) {
			return errors.New("training_outputs_must_preserve_previous")
		}
	}
	j.TrainingResult = receipt
	j.ExpectedOutputs = append([]OutputIntent(nil), c.ExpectedOutputs...)
	j.OutputPending = len(c.ExpectedOutputs) > 0
	j.Result = strings.TrimSpace(j.Result + "\n\n" + c.Result)
	if len(j.Result) > 64<<10 {
		j.Result = c.Result
	}
	j.Status = "done"
	j.DeliveryText = ""
	parts := []string{}
	for _, p := range j.DeliveryParts {
		if p != "text" && !strings.HasPrefix(p, "text:") {
			parts = append(parts, p)
		}
	}
	j.DeliveryParts = parts
	j.MediaPackage = files.Ref{}
	j.MediaPackageRequired = false
	j.MediaDeferred = false
	j.MediaReplyContext = ""
	return s.save(j)
}
