package jobs

import (
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/models"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSupplementOwnershipReplayAndCompletionRace(t *testing.T) {
	defer metadb.CloseAll()
	path := filepath.Join(t.TempDir(), "jobs")
	s, _ := Open(path)
	choice := models.Choice{Model: "gpt-6-sol", Effort: "high"}
	j, _ := s.EnqueueConversation("initial", "original", "owner", "context", choice, nil, "abcdef01")
	task, _ := s.Claim(time.Now())
	parent, v, active, e := s.SupplementMessage("msg", "new request", "owner", "context", "abcdef01", choice)
	if e != nil || !active || parent.ID != j.ID {
		t.Fatal(parent, v, e)
	}
	again, v2, _, _ := s.SupplementMessage("msg", "changed replay", "owner", "context", "abcdef01", choice)
	if again.ID != j.ID || v2.ID != v.ID || len(again.Supplements) != 1 {
		t.Fatal("duplicate supplement")
	}
	other, _, active, e := s.SupplementMessage("other", "other conversation", "owner", "context", "abcdef02", choice)
	if e != nil || active || other.ConversationID != "abcdef02" || other.Status != "queued" {
		t.Fatal("wrong conversation")
	}
	if _, e = s.PendingSupplements(j.ID, "stale", time.Now()); e == nil {
		t.Fatal("stale lease")
	}
	if e = s.Complete(Completion{ID: j.ID, Lease: task.Lease, Result: "done"}, time.Now()); e != nil {
		t.Fatal(e)
	}
	s, _ = Open(path)
	saved, _ := s.Snapshot(j.ID)
	if saved.Supplements[0].State != "queued" || saved.Supplements[0].FollowupID == "" {
		t.Fatal(saved)
	}
	replay, _, active, _ := s.SupplementMessage("msg", "new request", "owner", "context", "abcdef02", choice)
	if !active || replay.ID != j.ID || len(s.History()) != 3 {
		t.Fatal("replay moved conversation or duplicated followup")
	}
}
func TestReservedSupplementsNeverReplayOnAmbiguousSend(t *testing.T) {
	defer metadb.CloseAll()
	s, _ := Open(t.TempDir())
	choice := models.Choice{Model: "gpt-6-sol", Effort: "high"}
	j, _ := s.EnqueueConversation("a", "initial", "owner", "ctx", choice, nil, "abcdef01")
	task, _ := s.Claim(time.Now())
	_, v, _, _ := s.SupplementMessage("s", "supplement", "owner", "ctx", "abcdef01", choice)
	req := SteerRequest{ID: j.ID, Lease: task.Lease, SupplementID: v.ID, State: "dispatching", TurnID: "turn-1"}
	if e := s.AckSupplement(req, time.Now()); e != nil {
		t.Fatal(e)
	}
	if e := s.AckSupplement(req, time.Now()); e == nil {
		t.Fatal("reservation replay accepted")
	}
	pending, _ := s.PendingSupplements(j.ID, task.Lease, time.Now())
	if len(pending) != 0 {
		t.Fatal("reserved instruction polled twice")
	}
	s.Complete(Completion{ID: j.ID, Lease: task.Lease, Result: "done"}, time.Now())
	saved, _ := s.Snapshot(j.ID)
	if saved.Supplements[0].State != "uncertain" || len(s.History()) != 1 {
		t.Fatal("uncertain instruction queued twice")
	}
}
func TestCompletionRecoversLostAcceptanceAck(t *testing.T) {
	defer metadb.CloseAll()
	s, _ := Open(t.TempDir())
	choice := models.Choice{Model: "gpt-6-sol", Effort: "high"}
	j, _ := s.EnqueueConversation("a", "initial", "owner", "ctx", choice, nil, "abcdef01")
	task, _ := s.Claim(time.Now())
	_, v, _, _ := s.SupplementMessage("s", "supplement", "owner", "ctx", "abcdef01", choice)
	s.AckSupplement(SteerRequest{ID: j.ID, Lease: task.Lease, SupplementID: v.ID, State: "dispatching", TurnID: "turn"}, time.Now())
	c := Completion{ID: j.ID, Lease: task.Lease, Result: "done", SteerReceipts: []SteerReceipt{{ID: v.ID, State: "accepted", TurnID: "turn"}}}
	if e := s.Complete(c, time.Now()); e != nil {
		t.Fatal(e)
	}
	saved, _ := s.Snapshot(j.ID)
	if saved.Supplements[0].State != "accepted" || len(s.History()) != 1 {
		t.Fatal(saved)
	}
	if e := s.Complete(c, time.Now()); e != nil {
		t.Fatal("completion not idempotent")
	}
}
func TestSteeringHTTPRequiresBearerAndLease(t *testing.T) {
	defer metadb.CloseAll()
	s, _ := Open(t.TempDir())
	h := Handler(s, "secret")
	for _, auth := range []string{"", "Bearer secret"} {
		r := httptest.NewRequest(http.MethodPost, "/jobs/steering/poll", strings.NewReader(`{"id":"unknown","lease":"stale"}`))
		r.Header.Set("Authorization", auth)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := 401
		if auth != "" {
			want = 409
		}
		if w.Code != want {
			t.Fatal(w.Code)
		}
	}
}

func TestIdleSupplementReplayDoesNotSteerItsOwnQueuedFollowup(t *testing.T) {
	defer metadb.CloseAll()
	s, _ := Open(t.TempDir())
	choice := models.Choice{Model: "gpt-6-sol", Effort: "high"}
	first, _, active, e := s.SupplementMessage("idle-message", "do this", "owner", "ctx", "abcdef01", choice)
	if e != nil || active {
		t.Fatal(e)
	}
	s.Claim(time.Now())
	replay, _, active, e := s.SupplementMessage("idle-message", "do this", "owner", "ctx", "abcdef01", choice)
	if e != nil || active || replay.ID != first.ID || len(replay.Supplements) != 0 || len(s.History()) != 1 {
		t.Fatal("redelivery executed the idle supplement twice", replay, e)
	}
}
