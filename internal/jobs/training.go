package jobs

import (
	"errors"
	"math"
	"time"
)

type Training struct {
	State          string    `json:"state"`
	Epoch          int       `json:"epoch"`
	Total          int       `json:"total"`
	Loss           *float64  `json:"loss,omitempty"`
	CPUPercent     float64   `json:"cpu_percent"`
	RSSMiB         float64   `json:"rss_mib"`
	ElapsedSeconds int64     `json:"elapsed_seconds"`
	Checkpoint     string    `json:"checkpoint"`
	Resumable      bool      `json:"resumable"`
	Updated        time.Time `json:"updated"`
	Command        string    `json:"command,omitempty"`
	CommandID      string    `json:"command_id,omitempty"`
	Acknowledged   string    `json:"acknowledged,omitempty"`
	Error          string    `json:"error,omitempty"`
}
type TrainingUpdate struct {
	ID       string   `json:"id"`
	Lease    string   `json:"lease"`
	Training Training `json:"training"`
}

func (s *Store) UpdateTraining(u TrainingUpdate) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.lookup(u.ID)
	if !ok || u.Lease == "" || j.Lease != u.Lease {
		return errors.New("invalid_training_lease")
	}
	v := u.Training
	if v.State != "running" && v.State != "stopped" && v.State != "completed" && v.State != "failed" {
		return errors.New("invalid_training_state")
	}
	if v.Epoch < 0 || v.Total < 0 || v.ElapsedSeconds < 0 || v.RSSMiB < 0 || v.CPUPercent < 0 || math.IsInf(v.CPUPercent, 0) || math.IsNaN(v.CPUPercent) || len(v.Checkpoint) > 512 || len(v.Error) > 80 {
		return errors.New("invalid_training_metrics")
	}
	if j.CancelRequested && v.State == "running" {
		v.Command = "stop"
		v.CommandID = "cancel:" + j.ID
	} else {
		v.Command = j.Training.Command
		v.CommandID = j.Training.CommandID
	}
	v.Updated = time.Now().UTC()
	j.Training = v
	return s.save(j)
}
func (s *Store) RequestTraining(owner, id, action string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.lookup(id)
	if !ok || j.Owner != owner {
		return errors.New("task_not_found")
	}
	if action != "stop" && action != "resume" {
		return errors.New("invalid_training_action")
	}
	if action == "resume" && ((j.Training.State != "stopped" && j.Training.State != "failed") || !j.Training.Resumable || j.Training.Checkpoint == "") {
		return errors.New("checkpoint_resume_unavailable")
	}
	if action == "stop" && j.Training.State != "running" {
		return errors.New("training_not_running")
	}
	j.Training.Command = action
	j.Training.CommandID = time.Now().UTC().Format("20060102T150405.000000000")
	return s.save(j)
}
func (s *Store) TrainingCommands() ([]Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := s.query("json_extract(document,'$.training.command_id') IS NOT NULL AND json_extract(document,'$.training.command_id')<>COALESCE(json_extract(document,'$.training.acknowledged'),'')")
	return rows, s.readErr
}
func (s *Store) TrainingState(id, lease string) (Training, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.lookup(id)
	if !ok || j.Lease != lease || lease == "" {
		return Training{}, errors.New("invalid_training_lease")
	}
	return j.Training, nil
}
