package jobs

import (
	"testing"
	"time"
)

func TestTrainingWaitsForOriginalCompletionAndAcknowledgesCommands(t *testing.T) {
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	j, _ := s.Enqueue("train", "train", "owner", "reply")
	task, _ := s.Claim(time.Now())
	update := TrainingUpdate{ID: j.ID, Lease: task.Lease, Training: Training{State: "completed", Epoch: 2, Total: 2}}
	if e = s.UpdateTraining(update); e != nil {
		t.Fatal(e)
	}
	c := Completion{ID: j.ID, Lease: task.Lease, Result: "Training done"}
	if e = s.BeginTrainingOutputs(c); e == nil {
		t.Fatal("training overwrote running AI task")
	}
	if e = s.Complete(Completion{ID: j.ID, Lease: task.Lease, Result: "prepared"}, time.Now()); e != nil {
		t.Fatal(e)
	}
	if e = s.BeginTrainingOutputs(c); e != nil {
		t.Fatal(e)
	}
	update.Training = Training{State: "running", Checkpoint: "checkpoint.pt", Resumable: true}
	s.UpdateTraining(update)
	if e = s.RequestTraining("other", j.ID, "stop"); e == nil {
		t.Fatal("foreign command")
	}
	if _, e = s.Cancel("owner", j.ID); e != nil {
		t.Fatal(e)
	}
	rows, e := s.TrainingCommands()
	if e != nil || len(rows) != 1 || rows[0].Training.Command != "stop" {
		t.Fatal(rows, e)
	}
	update.Training.State = "stopped"
	update.Training.Acknowledged = rows[0].Training.CommandID
	s.UpdateTraining(update)
	rows, e = s.TrainingCommands()
	if e != nil || len(rows) != 0 {
		t.Fatal(rows, e)
	}
	if e = s.RequestTraining("owner", j.ID, "resume"); e != nil {
		t.Fatal(e)
	}
}
