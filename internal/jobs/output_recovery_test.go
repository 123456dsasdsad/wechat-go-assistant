package jobs

import (
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/usage"
	"strings"
	"testing"
	"time"
)

func TestIndependentOutputRecoveryAndUsageIdempotency(t *testing.T) {
	s, _ := Open(t.TempDir())
	defer s.Close()
	j, _ := s.Enqueue("task", "hello", "owner", "reply")
	task, _ := s.Claim(time.Now())
	f := files.Ref{ID: strings.Repeat("a", 24), Name: "figure.png", SHA256: strings.Repeat("b", 64), Size: 42}
	c := Completion{ID: j.ID, Lease: task.Lease, Result: "done", ExpectedOutputs: []OutputIntent{{Name: f.Name, SHA256: f.SHA256, Size: f.Size}}, OutputPending: true, Usage: usage.Tokens{Available: true, Input: 100, Cached: 20, Output: 5, Total: 105}}
	if e := s.Complete(c, time.Now()); e != nil {
		t.Fatal(e)
	}
	if _, e := s.CheckOutputUpload(j.ID, task.Lease, 0, f.Name, f.SHA256, 42, time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	if _, e := s.CheckOutputUpload(j.ID, task.Lease, 0, "other", f.SHA256, 42, time.Now()); e == nil {
		t.Fatal("unfrozen output accepted")
	}
	if e := s.PauseOwnerMedia("owner"); e != nil {
		t.Fatal(e)
	}
	done := Completion{ID: j.ID, Lease: task.Lease, Outputs: []files.Ref{f}}
	if e := s.FinishOutputs(done); e != nil {
		t.Fatal(e)
	}
	if e := s.FinishOutputs(done); e != nil {
		t.Fatal(e)
	}
	if e := s.Complete(c, time.Now()); e != nil {
		t.Fatal("original completion replay", e)
	}
	u, e := s.Usage("owner", "")
	if e != nil || u.Tasks != 1 || u.Total != 105 || u.Measured != 1 {
		t.Fatal(u, e)
	}
	v, _ := s.Snapshot(j.ID)
	if v.OutputPending || len(v.Outputs) != 1 || !v.MediaDeferred {
		t.Fatal(v)
	}
}
