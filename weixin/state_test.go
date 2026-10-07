package weixin

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestStateAtomicReplacementAndMalformedState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "state.json")
	s := NewState(Account{BotToken: "token", BotID: "bot", OwnerID: "owner", BaseURL: DefaultAPI})
	if err := SaveState(path, s); err != nil {
		t.Fatal(err)
	}
	s.Cursor = "new-cursor"
	if err := SaveState(path, s); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadState(path)
	if err != nil || loaded.Cursor != "new-cursor" {
		t.Fatalf("state replacement failed: %v", err)
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(path)
		if info.Mode().Perm() != 0600 {
			t.Error("credentials too broadly readable")
		}
	}
	os.WriteFile(path, []byte(`{"version":1,"account":{"bot_token":"secret-token"}}`), 0600)
	if _, err := LoadState(path); err == nil {
		t.Error("incomplete account accepted")
	}
}

func TestDrainPersistenceRetryAndOwnerIsolation(t *testing.T) {
	polls := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		polls++
		w.Write([]byte(`{"ret":0,"get_updates_buf":"next","msgs":[{"message_id":1,"message_type":1,"from_user_id":"stranger","context_token":"bad"},{"message_id":2,"message_type":1,"from_user_id":"owner","group_id":"group","context_token":"bad"},{"message_id":3,"message_type":1,"from_user_id":"owner","context_token":"ctx"},{"message_id":3,"message_type":1,"from_user_id":"owner","context_token":"ctx"}]}`))
	}, Options{})
	s := NewState(Account{BotToken: c.opts.Token, BotID: "bot", OwnerID: "owner", BaseURL: c.opts.BaseURL})
	commits, handled := 0, 0
	commit := func(*State) error { commits++; return nil }
	failure := errors.New("worker unavailable")
	if err := c.Drain(context.Background(), s, func(ctx context.Context, m Message) error {
		if commits < 2 || !s.BatchActive || len(s.Pending) == 0 || s.Cursor != "" {
			t.Error("message was handled before durable stage")
		}
		handled++
		return failure
	}, commit); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if len(s.Pending) != 2 || s.Contexts["owner"] != "ctx" || s.Cursor != "" || handled != 1 {
		t.Fatalf("failed message was lost: %+v", s)
	}
	if err := c.Drain(context.Background(), s, func(ctx context.Context, m Message) error { handled++; return nil }, commit); err != nil {
		t.Fatal(err)
	}
	if polls != 1 || handled != 2 || s.Cursor != "next" || s.BatchActive {
		t.Fatalf("retry or duplicate filtering failed: polls=%d handled=%d state=%+v", polls, handled, s)
	}
	if _, ok := s.Contexts["stranger"]; ok {
		t.Error("foreign user was authorized")
	}
}

func TestDrainStopsOnCommitFailureAndCancellation(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ret":0,"get_updates_buf":"next","msgs":[{"message_id":"1","message_type":1,"from_user_id":"owner","context_token":"ctx"}]}`))
	}, Options{})
	s := NewState(Account{BotToken: c.opts.Token, BotID: "bot", OwnerID: "owner", BaseURL: c.opts.BaseURL})
	called := false
	err := c.Drain(context.Background(), s, func(context.Context, Message) error { called = true; return nil }, func(*State) error { return errors.New("disk full") })
	if err == nil || called {
		t.Error("business action ran despite failed persistence")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Drain(ctx, s, func(context.Context, Message) error { return nil }, func(*State) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost %v", err)
	}
}
