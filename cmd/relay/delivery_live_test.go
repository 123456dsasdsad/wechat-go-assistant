package main

import (
	"context"
	"errors"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/conversations"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
	"strings"
	"testing"
	"time"
)

type blockedMediaSender struct {
	started chan struct{}
	texts   chan string
}

func (s *blockedMediaSender) SendText(_ context.Context, r weixin.Reply, _ string) (weixin.SendResult, error) {
	s.texts <- r.ClientID
	return weixin.SendResult{}, nil
}
func (s *blockedMediaSender) Upload(ctx context.Context, _ string, _ int, _ []byte) (weixin.Uploaded, error) {
	close(s.started)
	<-ctx.Done()
	return weixin.Uploaded{}, ctx.Err()
}
func (s *blockedMediaSender) SendImage(context.Context, weixin.Reply, weixin.Uploaded) (weixin.SendResult, error) {
	return weixin.SendResult{}, errors.New("not reached")
}
func (s *blockedMediaSender) SendFile(context.Context, weixin.Reply, string, weixin.Uploaded) (weixin.SendResult, error) {
	return weixin.SendResult{}, errors.New("not reached")
}
func startDeliveryForTest(t *testing.T, run func(context.Context)) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("delivery loop did not finish before temporary-store cleanup")
		}
	})
}

func TestSlowOldMediaDoesNotBlockNewTextResult(t *testing.T) {
	defer metadb.CloseAll()
	queue, _ := jobs.Open(t.TempDir())
	outputs, _ := files.Open(t.TempDir())
	sessions, _ := conversations.Open(t.TempDir() + "/sessions.json")
	a, _ := queue.Enqueue("a", "first", "owner", "old")
	task, _ := queue.Claim(time.Now())
	ref, _ := outputs.Save("owner", "result", "file.txt", strings.NewReader("actual result"))
	queue.Complete(jobs.Completion{ID: task.ID, Lease: task.Lease, Result: "first reply", Outputs: []files.Ref{ref}}, time.Now())
	queue.CommitPart(a.ID, "text")
	b, _ := queue.Enqueue("b", "second", "owner", "new")
	task, _ = queue.Claim(time.Now())
	queue.Complete(jobs.Completion{ID: task.ID, Lease: task.Lease, Result: "second reply"}, time.Now())
	sender := &blockedMediaSender{make(chan struct{}), make(chan string, 8)}
	client := &liveResultSender{client: sender, contextFor: func(string) (string, error) { return "latest", nil }}
	startDeliveryForTest(t, func(ctx context.Context) {
		deliver(ctx, client, queue, sessions, outputs, "https://example.com/wechat-files/")
	})
	select {
	case <-sender.started:
	case <-time.After(4 * time.Second):
		t.Fatal("media lane did not start")
	}
	select {
	case id := <-sender.texts:
		if id != "go-result-"+b.ID {
			t.Fatal("wrong result delivered")
		}
	case <-time.After(time.Second):
		t.Fatal("new text blocked behind media upload")
	}
}

type contextSender struct {
	latest  string
	uploads int
	fail    bool
}

func (s *contextSender) SendText(_ context.Context, r weixin.Reply, _ string) (weixin.SendResult, error) {
	s.latest = r.ContextToken
	return weixin.SendResult{}, nil
}
func (s *contextSender) Upload(context.Context, string, int, []byte) (weixin.Uploaded, error) {
	s.uploads++
	return weixin.Uploaded{DownloadParam: "cached"}, nil
}
func (s *contextSender) SendImage(_ context.Context, r weixin.Reply, _ weixin.Uploaded) (weixin.SendResult, error) {
	s.latest = r.ContextToken
	if s.fail {
		return weixin.SendResult{}, &weixin.APIError{Operation: "sendMessage", Ret: -2}
	}
	return weixin.SendResult{}, nil
}
func (s *contextSender) SendFile(_ context.Context, r weixin.Reply, _ string, _ weixin.Uploaded) (weixin.SendResult, error) {
	s.latest = r.ContextToken
	return weixin.SendResult{}, nil
}
func TestRetryUsesFreshContextWithoutUploadingMediaAgain(t *testing.T) {
	defer metadb.CloseAll()
	sender := &contextSender{fail: true}
	current := "first"
	client := &liveResultSender{client: sender, contextFor: func(string) (string, error) { return current, nil }}
	ctx := context.Background()
	reply := weixin.Reply{ToUserID: "owner", ContextToken: "stale", ClientID: "stable"}
	file, _ := client.Upload(ctx, "owner", weixin.UploadImage, []byte("same image"))
	client.SendImage(ctx, reply, file)
	current = "fresh"
	sender.fail = false
	file, _ = client.Upload(ctx, "owner", weixin.UploadImage, []byte("same image"))
	client.SendImage(ctx, reply, file)
	if sender.uploads != 1 || sender.latest != "fresh" {
		t.Fatal("stale context or repeated upload", sender)
	}
	client.Upload(ctx, "another-owner", weixin.UploadImage, []byte("same image"))
	if sender.uploads != 2 {
		t.Fatal("upload cache crossed owner scope")
	}
	parts := textChunks(strings.Repeat("中", 4500))
	if len(parts) != 3 || strings.Join(parts, "") != strings.Repeat("中", 4500) {
		t.Fatal("Unicode text splitting corrupts reply")
	}
}
