package jobs

import (
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"path/filepath"
	"testing"
	"time"
)

func TestProgressPersistsWhileRunningAndRejectsStaleUpdates(t *testing.T) {
	defer metadb.CloseAll()
	dir := filepath.Join(t.TempDir(), "jobs")
	s, _ := Open(dir)
	j, _ := s.Enqueue("source", "task", "owner", "context")
	task, _ := s.Claim(time.Now())
	u := ProgressUpdate{ID: j.ID, Lease: task.Lease, Sequence: 1, Text: "正在检查资料"}
	if e := s.UpdateProgress(u, time.Now()); e != nil {
		t.Fatal(e)
	}
	if e := s.UpdateProgress(u, time.Now()); e != nil {
		t.Fatal("retry not idempotent", e)
	}
	reopened, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	got, _ := reopened.Snapshot(j.ID)
	if got.Status != "running" || got.Progress != u.Text || got.Result != "" {
		t.Fatal("progress conflated with final result", got)
	}
	u.Text = "冲突"
	if s.UpdateProgress(u, time.Now()) == nil {
		t.Fatal("same sequence overwritten")
	}
	u.Sequence = 2
	u.Lease = "wrong"
	if s.UpdateProgress(u, time.Now()) == nil {
		t.Fatal("stale lease accepted")
	}
	u.Lease = task.Lease
	if s.UpdateProgress(u, time.Now().Add(time.Hour)) == nil {
		t.Fatal("expired lease accepted")
	}
	s.Complete(Completion{ID: j.ID, Lease: task.Lease, Result: "final"}, time.Now())
	if s.UpdateProgress(u, time.Now()) == nil {
		t.Fatal("completed result changed by late progress")
	}
}
