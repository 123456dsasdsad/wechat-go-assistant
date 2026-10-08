package jobs

import (
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/models"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/userinput"
)

func enqueueParallel(t *testing.T, s *Store, source, owner, cid string) Job {
	t.Helper()
	j, err := s.EnqueueConversation(source, source, owner, "context-"+source, models.Choice{Model: "gpt-6-sol", Effort: "high"}, nil, cid)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func TestParallelConversationsPreserveOrderAcrossRestart(t *testing.T) {
	defer metadb.CloseAll()
	dir := filepath.Join(t.TempDir(), "jobs")
	s, _ := Open(dir)
	a1 := enqueueParallel(t, s, "a1", "owner", "aaaaaaaa")
	a2 := enqueueParallel(t, s, "a2", "owner", "aaaaaaaa")
	b1 := enqueueParallel(t, s, "b1", "owner", "bbbbbbbb")
	now := time.Now()
	first, err := s.Claim(now)
	if err != nil || first == nil || first.ID != a1.ID {
		t.Fatal("first conversation did not start", first, err)
	}
	other, err := s.Claim(now)
	if err != nil || other == nil || other.ID != b1.ID {
		t.Fatal("unrelated conversation blocked behind a2", other, err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if task, err := s.Claim(now); err != nil || task != nil {
		t.Fatal("same conversation ran concurrently after restart", task, err)
	}
	if err = s.Complete(Completion{ID: first.ID, Lease: first.Lease, Result: "a1 answer"}, now); err != nil {
		t.Fatal(err)
	}
	next, err := s.Claim(now)
	if err != nil || next == nil || next.ID != a2.ID {
		t.Fatal("same conversation did not resume in order", next, err)
	}
	if err = s.Complete(Completion{ID: other.ID, Lease: first.Lease, Result: "wrong lease"}, now); err == nil {
		t.Fatal("parallel completion accepted another task's lease")
	}
	if err = s.Complete(Completion{ID: other.ID, Lease: other.Lease, Result: "b1 answer"}, now); err != nil {
		t.Fatal(err)
	}
	a, _ := s.Snapshot(a1.ID)
	b, _ := s.Snapshot(b1.ID)
	if a.ReplyContext != "context-a1" || b.ReplyContext != "context-b1" || a.Result != "a1 answer" || b.Result != "b1 answer" {
		t.Fatal("parallel replies crossed conversation boundaries")
	}
}

func TestParallelClaimCallersLeaseOneTaskPerConversation(t *testing.T) {
	defer metadb.CloseAll()
	s, _ := Open(filepath.Join(t.TempDir(), "jobs"))
	for _, cid := range []string{"aaaaaaaa", "bbbbbbbb", "cccccccc"} {
		for _, suffix := range []string{"1", "2", "3"} {
			enqueueParallel(t, s, cid+suffix, "owner", cid)
		}
	}
	now := time.Now()
	results := make(chan *Task, 12)
	failures := make(chan error, 12)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			task, err := s.Claim(now)
			if err != nil {
				failures <- err
			}
			results <- task
		}()
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for task := range results {
		if task != nil {
			if seen[task.ConversationID] {
				t.Fatal("duplicate active conversation", task.ConversationID)
			}
			seen[task.ConversationID] = true
		}
	}
	if len(seen) != 3 {
		t.Fatalf("want 3 simultaneous conversations, got %d", len(seen))
	}
}

func TestParallelExpiredLeaseRetriesBeforeSameConversationFollowup(t *testing.T) {
	defer metadb.CloseAll()
	s, _ := Open(filepath.Join(t.TempDir(), "jobs"))
	a := enqueueParallel(t, s, "expired-a", "owner", "aaaaaaaa")
	enqueueParallel(t, s, "followup-a", "owner", "aaaaaaaa")
	b := enqueueParallel(t, s, "live-b", "owner", "bbbbbbbb")
	now := time.Now()
	first, _ := s.Claim(now)
	other, _ := s.Claim(now)
	if other == nil || other.ID != b.ID {
		t.Fatal("b did not start")
	}
	later := now.Add(5 * time.Minute)
	if err := s.Heartbeat(other.ID, other.Lease, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	retry, err := s.Claim(later)
	if err != nil || retry == nil || retry.ID != a.ID || retry.Lease == first.Lease {
		t.Fatal("expired a did not retry first", retry, err)
	}
	if task, _ := s.Claim(later); task != nil {
		t.Fatal("followup bypassed retry")
	}
	if s.Complete(Completion{ID: first.ID, Lease: first.Lease, Result: "stale"}, later) == nil {
		t.Fatal("old lease accepted")
	}
}

func TestParallelWaitingQuestionDoesNotBlockOtherConversation(t *testing.T) {
	defer metadb.CloseAll()
	s, _ := Open(filepath.Join(t.TempDir(), "jobs"))
	enqueueParallel(t, s, "waiting", "owner", "aaaaaaaa")
	now := time.Now()
	task, _ := s.Claim(now)
	_, err := s.PublishQuestion(QuestionRequest{ID: task.ID, Lease: task.Lease, Request: userinput.Request{ID: "pick", Questions: []userinput.Question{{ID: "format", Header: "Format", Question: "Which format?", Options: []userinput.Option{{Label: "PNG", Description: "Raster"}, {Label: "SVG", Description: "Vector"}}}}}}, now)
	if err != nil {
		t.Fatal(err)
	}
	enqueueParallel(t, s, "waiting-followup", "owner", "aaaaaaaa")
	other := enqueueParallel(t, s, "independent", "owner", "bbbbbbbb")
	next, err := s.Claim(now)
	if err != nil || next == nil || next.ID != other.ID {
		t.Fatal("waiting question blocked other conversation", next, err)
	}
	snapshot, _ := s.Snapshot(task.ID)
	if !snapshot.WaitingForUser() {
		t.Fatal("waiting question changed")
	}
}

func TestParallelLegacyLaneIsSequentialPerOwner(t *testing.T) {
	defer metadb.CloseAll()
	s, _ := Open(filepath.Join(t.TempDir(), "jobs"))
	enqueueParallel(t, s, "legacy1", "owner", "")
	enqueueParallel(t, s, "legacy2", "owner", "")
	other := enqueueParallel(t, s, "other-owner", "another", "")
	now := time.Now()
	s.Claim(now)
	task, err := s.Claim(now)
	if err != nil || task == nil || task.ID != other.ID {
		t.Fatal("legacy owner lanes crossed", task, err)
	}
	if task, _ := s.Claim(now); task != nil {
		t.Fatal("legacy same owner ran concurrently")
	}
}
