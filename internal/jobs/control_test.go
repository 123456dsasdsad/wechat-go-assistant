package jobs

import (
	"github.com/123456dsasdsad/wechat-go-assistant/internal/models"
	"strings"
	"testing"
	"time"
)

func TestCancelDropsPendingOutputIntent(t *testing.T) {
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	j, _ := s.Enqueue("cancel", "cancel", "owner", "reply")
	task, _ := s.Claim(time.Now())
	s.Cancel("owner", j.ID)
	c := Completion{ID: j.ID, Lease: task.Lease, Result: "finished concurrently", OutputPending: true, ExpectedOutputs: []OutputIntent{{Name: "result.txt", SHA256: strings.Repeat("a", 64), Size: 3}}}
	if e = s.Complete(c, time.Now()); e != nil {
		t.Fatal(e)
	}
	v, _ := s.Snapshot(j.ID)
	if v.OutputPending || len(v.ExpectedOutputs) > 0 || v.Error != "user_canceled" {
		t.Fatal(v)
	}
}

func TestCancelBlocksSameConversationUntilAcknowledged(t *testing.T) {
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	choice := models.Choice{Model: "gpt-6-sol", Effort: "high"}
	a, _ := s.EnqueueConversation("a", "one", "owner", "reply", choice, nil, "12345678")
	s.EnqueueConversation("b", "two", "owner", "reply", choice, nil, "12345678")
	s.EnqueueConversation("c", "other", "owner", "reply", choice, nil, "abcdef12")
	now := time.Now()
	task, _ := s.Claim(now)
	if task.ID != a.ID {
		t.Fatal(task)
	}
	if _, e = s.Cancel("owner", a.ID); e != nil {
		t.Fatal(e)
	}
	other, _ := s.Claim(now)
	if other == nil || other.ConversationID != "abcdef12" {
		t.Fatal("other conversation must run")
	}
	none, _ := s.Claim(now)
	if none != nil {
		t.Fatal("canceled process not acknowledged")
	}
	if e = s.Complete(Completion{ID: a.ID, Lease: task.Lease, Error: "context canceled"}, now); e != nil {
		t.Fatal(e)
	}
	next, _ := s.Claim(now)
	if next == nil || next.ConversationID != "12345678" {
		t.Fatal("next should run")
	}
}
func TestRetryDeduplicatesMessage(t *testing.T) {
	s, _ := Open(t.TempDir())
	defer s.Close()
	j, _ := s.Enqueue("a", "hello", "owner", "reply")
	task, _ := s.Claim(time.Now())
	s.Complete(Completion{ID: j.ID, Lease: task.Lease, Error: "failed"}, time.Now())
	a, e := s.Retry("owner", j.ID, "retry-message", "reply")
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.Retry("owner", j.ID, "retry-message", "reply")
	if e != nil || a.ID != b.ID || a.ID == j.ID || a.ParentID != j.ID {
		t.Fatal(a, b, e)
	}
	if _, e = s.Cancel("other", a.ID); e == nil {
		t.Fatal("owner check missing")
	}
}
