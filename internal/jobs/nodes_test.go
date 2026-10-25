package jobs

import (
	"github.com/123456dsasdsad/wechat-go-assistant/internal/models"
	"path/filepath"
	"testing"
	"time"
)

func TestNodePriorityAffinityAndLostLeaseNeverRerunBlindly(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "jobs"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	policies := []NodePolicy{{ID: "campus", Priority: 10, Enabled: true}, {ID: "ass", Priority: 20, Enabled: true}, {ID: "icd", Priority: 30, Enabled: false}}
	if err = s.ConfigureNodes(policies, ""); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ass := NodeHeartbeat{ID: "ass", Instance: "aaaaaaaaaaaaaaaaaaaaaaaa", Ready: true, Models: []string{"gpt-6-sol"}}
	campus := NodeHeartbeat{ID: "campus", Instance: "bbbbbbbbbbbbbbbbbbbbbbbb", Ready: true, Models: ass.Models}
	if err = s.RegisterNode(ass, now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Claim(now); err == nil {
		t.Fatal("unregistered legacy worker allowed")
	}
	j, err := s.EnqueueConversation("first", "inspect", "owner", "reply", models.Choice{Model: "gpt-6-sol", Effort: "high"}, nil, "12345678")
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.ClaimFrom(now, Claimant{Node: ass.ID, Instance: ass.Instance})
	if err != nil || task == nil || task.ID != j.ID {
		t.Fatal(task, err)
	}
	later := now.Add(11 * time.Minute)
	_ = s.RegisterNode(campus, later)
	_ = s.RegisterNode(ass, later)
	if task, err = s.ClaimFrom(later, Claimant{Node: ass.ID, Instance: ass.Instance}); err != nil || task != nil {
		t.Fatal("expired task repeated", task, err)
	}
	if task, err = s.ClaimFrom(later, Claimant{Node: campus.ID, Instance: campus.Instance}); err != nil || task != nil {
		t.Fatal("context migrated without restore", task, err)
	}
	_, err = s.EnqueueConversation("new", "new task", "owner", "reply", models.Choice{Model: "gpt-6-sol", Effort: "high"}, nil, "87654321")
	if err != nil {
		t.Fatal(err)
	}
	if task, err = s.ClaimFrom(later, Claimant{Node: ass.ID, Instance: ass.Instance}); err != nil || task != nil {
		t.Fatal("standby took primary work", task, err)
	}
	if task, err = s.ClaimFrom(later, Claimant{Node: campus.ID, Instance: campus.Instance}); err != nil || task == nil || task.ConversationID != "87654321" {
		t.Fatal(task, err)
	}
	if err = s.RegisterNode(NodeHeartbeat{ID: "icd", Instance: ass.Instance, Ready: true}, later); err == nil {
		t.Fatal("disabled node activated")
	}
}
