package jobs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOldJobWithoutEffortKeepsHigh(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	j, _ := s.Enqueue("legacy", "text", "owner", "ctx")
	path := filepath.Join(dir, j.ID+".json")
	b, _ := os.ReadFile(path)
	var data map[string]any
	json.Unmarshal(b, &data)
	delete(data, "effort")
	b, _ = json.Marshal(data)
	os.WriteFile(path, b, 0600)
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.Claim(time.Now())
	if err != nil || task.Effort != "high" {
		t.Fatal(task, err)
	}
}

func TestDurableDeduplicationAndLease(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	j, err := s.Enqueue("wechat-id", "read fixture", "owner", "context")
	if err != nil {
		t.Fatal(err)
	}
	again, _ := s.Enqueue("wechat-id", "other text", "owner", "context")
	if j.ID != again.ID || again.Input != j.Input {
		t.Fatal("duplicate changed task")
	}
	now := time.Now()
	task, err := s.Claim(now)
	if err != nil || task == nil {
		t.Fatalf("claim: %v", err)
	}
	other, _ := s.Claim(now)
	if other != nil {
		t.Fatal("concurrent lease")
	}
	if err = s.Complete(Completion{ID: j.ID, Lease: "wrong", Result: "answer"}, now); err == nil {
		t.Fatal("accepted foreign lease")
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Complete(Completion{ID: j.ID, Lease: task.Lease, Result: "answer", ToolCount: 1}, now); err != nil {
		t.Fatal(err)
	}
	if len(s.Ready()) != 1 {
		t.Fatal("durable result missing")
	}
	if err = s.Delivered(j.ID); err != nil {
		t.Fatal(err)
	}
	s, _ = Open(dir)
	if len(s.Ready()) != 0 {
		t.Fatal("delivered result replayed")
	}
}
func TestExpiredLeaseCannotOverwriteRetry(t *testing.T) {
	s, _ := Open(t.TempDir())
	j, _ := s.Enqueue("id", "input", "owner", "context")
	now := time.Now()
	a, _ := s.Claim(now)
	b, _ := s.Claim(now.Add(5 * time.Minute))
	if b == nil || a.Lease == b.Lease {
		t.Fatal("lease was not renewed")
	}
	if s.Complete(Completion{ID: j.ID, Lease: a.Lease, Result: "old"}, now.Add(5*time.Minute)) == nil {
		t.Fatal("old lease overwrote retry")
	}
	if err := s.Complete(Completion{ID: j.ID, Lease: b.Lease, Result: "new"}, now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
}

func TestHeartbeatSurvivesRestartAndRejectsStaleLease(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	j, _ := s.Enqueue("large-transfer", "input", "owner", "context")
	now := time.Now()
	task, _ := s.Claim(now)
	if s.Heartbeat(j.ID, "wrong", now.Add(time.Minute)) == nil {
		t.Fatal("foreign heartbeat accepted")
	}
	if err := s.Heartbeat(j.ID, task.Lease, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if newer, _ := s.Claim(now.Add(5 * time.Minute)); newer != nil {
		t.Fatal("renewed task reclaimed")
	}
	if s.History()[0].LeaseRenewals != 1 {
		t.Fatal("renewal not durable")
	}
	if s.Heartbeat(j.ID, task.Lease, now.Add(7*time.Minute)) == nil {
		t.Fatal("expired heartbeat revived task")
	}
	retry, _ := s.Claim(now.Add(7 * time.Minute))
	if retry == nil {
		t.Fatal("expired task not retried")
	}
	if s.Heartbeat(j.ID, task.Lease, now.Add(7*time.Minute)) == nil {
		t.Fatal("old lease heartbeat accepted")
	}
	if err = s.Complete(Completion{ID: j.ID, Lease: retry.Lease, Result: "ok"}, now.Add(7*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if s.Heartbeat(j.ID, retry.Lease, now.Add(7*time.Minute)) == nil {
		t.Fatal("completed task renewed")
	}
}
