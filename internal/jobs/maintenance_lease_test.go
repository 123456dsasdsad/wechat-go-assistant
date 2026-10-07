package jobs

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMaintenanceAtomicClaimGuard(t *testing.T) {
	root := t.TempDir()
	s, _ := Open(filepath.Join(root, "jobs"))
	s.Enqueue("m", "task", "owner", "context")
	if e := s.BeginMaintenance(time.Now().Add(15 * time.Minute)); e != nil {
		t.Fatal(e)
	}
	if task, e := s.Claim(time.Now()); e != nil || task != nil {
		t.Fatal("task slipped through maintenance guard")
	}
	if s.BeginMaintenance(time.Now().Add(15*time.Minute)) == nil {
		t.Fatal("overlapping maintenance accepted")
	}
	os.Remove(filepath.Join(root, "maintenance-drain.flag"))
	s.Claim(time.Now())
	if s.BeginMaintenance(time.Now().Add(15*time.Minute)) == nil {
		t.Fatal("active task can be interrupted")
	}
}
