package jobs

import (
	"github.com/123456dsasdsad/wechat-go-assistant/internal/conversations"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/models"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/usage"
	"testing"
	"time"
)

func TestUsageCoverageIsolationAndBudget(t *testing.T) {
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	j, e := s.EnqueuePersonalized("first", "hello", "owner", "reply", models.Choice{Model: "gpt-6-sol", Effort: "high"}, nil, "12345678", "", conversations.Profile{BudgetUSD: .001})
	if e != nil {
		t.Fatal(e)
	}
	task, _ := s.Claim(time.Now())
	c := Completion{ID: j.ID, Lease: task.Lease, Result: "done", Usage: usage.Tokens{Available: true, Input: 2000, Output: 100, Total: 2100}}
	if e = s.Complete(c, time.Now()); e != nil {
		t.Fatal(e)
	}
	if e = s.Complete(c, time.Now()); e != nil {
		t.Fatal(e)
	}
	v, _ := s.Snapshot(j.ID)
	if !v.BudgetReached {
		t.Fatal("budget not reached")
	}
	other, _ := s.EnqueueConversation("second", "hello", "owner", "reply", models.Choice{Model: "unknown-model", Effort: "high"}, nil, "abcdef12")
	task, _ = s.Claim(time.Now())
	s.Complete(Completion{ID: other.ID, Lease: task.Lease, Result: "done", Usage: usage.Tokens{Available: true, Input: 10, Total: 10}}, time.Now())
	all, by, e := s.UsageByConversation("owner")
	if e != nil || all.Tasks != 2 || all.Unpriced != 1 || all.Measured != 2 || by["12345678"].Total != 2100 || by["abcdef12"].USD != 0 {
		t.Fatal(all, by, e)
	}
	foreign, e := s.Usage("other", "")
	if e != nil || foreign.Tasks != 0 {
		t.Fatal(foreign, e)
	}
}
