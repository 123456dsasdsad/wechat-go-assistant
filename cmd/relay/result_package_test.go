package main

import (
	"archive/zip"
	"bytes"
	"context"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
	"io"
	"strings"
	"testing"
	"time"
)

func TestCompletePackageContainsEveryOriginalAndKeepsHonestReceipts(t *testing.T) {
	queue, _ := jobs.Open(t.TempDir())
	outputs, _ := files.Open(t.TempDir())
	j, _ := queue.Enqueue("package", "request", "owner", "original-context")
	task, _ := queue.Claim(time.Now())
	a, _ := outputs.Save("owner", "a", "a.txt", strings.NewReader("first original"))
	b, _ := outputs.Save("owner", "b", "b.txt", strings.NewReader("second original"))
	queue.Complete(jobs.Completion{ID: task.ID, Lease: task.Lease, Result: "final answer", Outputs: []files.Ref{a, b}}, time.Now())
	j, _ = queue.Snapshot(j.ID)
	j, e := ensureResultPackage(queue, outputs, j)
	if e != nil {
		t.Fatal(e)
	}
	f, e := outputs.OpenBlob(j.MediaPackage)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	z, e := zip.NewReader(f, j.MediaPackage.Size)
	if e != nil {
		t.Fatal(e)
	}
	got := map[string]string{}
	for _, entry := range z.File {
		r, e := entry.Open()
		if e != nil {
			t.Fatal(e)
		}
		data, e := io.ReadAll(r)
		r.Close()
		if e != nil {
			t.Fatal(e)
		}
		got[entry.Name] = string(data)
	}
	if got["files/001_a.txt"] != "first original" || got["files/002_b.txt"] != "second original" || !strings.Contains(got["result.txt"], "final answer") || !strings.Contains(got["manifest.json"], a.SHA256) {
		t.Fatal("incomplete package", got)
	}
	again, e := ensureResultPackage(queue, outputs, j)
	if e != nil || again.MediaPackage != j.MediaPackage {
		t.Fatal("package regenerated")
	}
	queue.CommitPart(j.ID, "text")
	queue.CommitPart(j.ID, j.MediaPackage.ID)
	markFullyDelivered(queue, j.ID)
	j, _ = queue.Snapshot(j.ID)
	if j.Status != "delivered" || !j.PackageDelivered() || j.PartDelivered(a.ID) || j.PartDelivered(b.ID) || hasUnsentOutputs(j) {
		t.Fatal("individual receipts fabricated or package incomplete", j)
	}
}

func TestMediaNeverBorrowsNewMessageContextOrResumesAfterFailure(t *testing.T) {
	queue, _ := jobs.Open(t.TempDir())
	outputs, _ := files.Open(t.TempDir())
	j, _ := queue.Enqueue("single", "request", "owner", "original-context")
	task, _ := queue.Claim(time.Now())
	// Valid small PNG, so the production media lane uses SendImage.
	png := []byte{137, 80, 78, 71, 13, 10, 26, 10, 0, 0, 0, 13, 73, 72, 68, 82, 0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0, 31, 21, 196, 137}
	ref, _ := outputs.Save("owner", "image", "plot.png", bytes.NewReader(png))
	queue.Complete(jobs.Completion{ID: task.ID, Lease: task.Lease, Result: "answer", Outputs: []files.Ref{ref}}, time.Now())
	queue.CommitPart(j.ID, "text")
	sender := &contextSender{fail: true}
	client := &liveResultSender{client: sender, contextFor: func(string) (string, error) { return "new-unrelated-message", nil }, pauseMedia: queue.PauseOwnerMedia}
	client.mediaAllowed = func(r weixin.Reply) bool {
		s, ok := queue.Snapshot(outboundJobID(r.ClientID))
		return ok && !s.MediaDeferred && s.MediaContext() == r.ContextToken
	}
	startDeliveryForTest(t, func(ctx context.Context) {
		deliver(ctx, client, queue, nil, outputs, "")
	})
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		s, _ := queue.Snapshot(j.ID)
		if s.MediaDeferred {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	s, _ := queue.Snapshot(j.ID)
	if !s.MediaDeferred || sender.latest != "original-context" || s.PartDelivered(ref.ID) {
		t.Fatal("failed media refreshed or falsely committed", sender.latest, s.MediaDeferred)
	}
	if e := client.PauseMedia("owner"); e != nil {
		t.Fatal(e)
	}
	if len(selectMediaJobs(queue.History())) != 0 {
		t.Fatal("ordinary inbound revived old media")
	}
	s, e := queue.RequestMedia("owner", j.ID[:8], "explicit-replay-context")
	if e != nil {
		t.Fatal(e)
	}
	if s.MediaDeferred || s.MediaContext() != "explicit-replay-context" {
		t.Fatal("explicit replay not authorized")
	}
	if e = queue.DeferMedia(j.ID, "original-context"); e != nil {
		t.Fatal(e)
	}
	s, _ = queue.Snapshot(j.ID)
	if s.MediaDeferred {
		t.Fatal("stale failure paused newer explicit request")
	}
	queue.PauseOwnerMedia("owner")
	s, _ = queue.Snapshot(j.ID)
	if !s.MediaDeferred {
		t.Fatal("new ordinary message did not pause unfinished batch")
	}
}

func TestFutureMultiFileTaskDeliversOneCompletePackageAutomatically(t *testing.T) {
	queue, _ := jobs.Open(t.TempDir())
	outputs, _ := files.Open(t.TempDir())
	j, _ := queue.Enqueue("future", "new task", "owner", "original")
	task, _ := queue.Claim(time.Now())
	a, _ := outputs.Save("owner", "a", "a.txt", strings.NewReader("a"))
	b, _ := outputs.Save("owner", "b", "b.txt", strings.NewReader("b"))
	queue.Complete(jobs.Completion{ID: task.ID, Lease: task.Lease, Result: strings.Repeat("结果", 3000), Outputs: []files.Ref{a, b}}, time.Now())
	sender := &fakeResultSender{}
	client := &liveResultSender{client: sender, contextFor: func(string) (string, error) { return "original", nil }}
	startDeliveryForTest(t, func(ctx context.Context) {
		deliver(ctx, client, queue, nil, outputs, "https://example.com/wechat-files/")
	})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s, _ := queue.Snapshot(j.ID)
		if s.Status == "delivered" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	s, _ := queue.Snapshot(j.ID)
	if s.Status != "delivered" || !s.PackageDelivered() || sender.files != 1 || sender.images != 0 || sender.texts != 1 || len([]rune(s.DeliveryText)) > 2000 {
		t.Fatal("future task still depends on multiple partial sends", s.Status, sender.files, sender.texts)
	}
}

func TestOrdinaryStatusDoesNotAuthorizeUnfinishedOldImages(t *testing.T) {
	in := inboundFixture(t)
	j, _ := in.queue.Enqueue("old", "old task", "owner", "old-context")
	task, _ := in.queue.Claim(time.Now())
	ref, _ := in.files.Save("owner", "image", "image.png", strings.NewReader("fixture"))
	in.queue.Complete(jobs.Completion{ID: task.ID, Lease: task.Lease, Result: "old answer", Outputs: []files.Ref{ref}}, time.Now())
	in.queue.CommitPart(j.ID, "text")
	if e := in.handle(context.Background(), textMessage("status", "任务状态")); e != nil {
		t.Fatal(e)
	}
	s, _ := in.queue.Snapshot(j.ID)
	if !s.MediaDeferred || len(selectMediaJobs(in.queue.History())) != 0 {
		t.Fatal("status revived old images")
	}
	if e := in.handle(context.Background(), textMessage("request", "回传任务 "+j.ID[:8])); e != nil {
		t.Fatal(e)
	}
	s, _ = in.queue.Snapshot(j.ID)
	if s.MediaDeferred || !s.MediaRequested {
		t.Fatal("explicit replay not authorized")
	}
}

func TestPausedFutureTaskStillBuildsCompleteAccessiblePackage(t *testing.T) {
	queue, _ := jobs.Open(t.TempDir())
	outputs, _ := files.Open(t.TempDir())
	j, _ := queue.Enqueue("paused-future", "new task", "owner", "original")
	task, _ := queue.Claim(time.Now())
	a, _ := outputs.Save("owner", "a", "a.txt", strings.NewReader("a"))
	b, _ := outputs.Save("owner", "b", "b.txt", strings.NewReader("b"))
	queue.Complete(jobs.Completion{ID: task.ID, Lease: task.Lease, Result: "answer", Outputs: []files.Ref{a, b}}, time.Now())
	queue.CommitPart(j.ID, "text")
	queue.PauseOwnerMedia("owner")
	sender := &fakeResultSender{}
	client := &liveResultSender{client: sender, contextFor: func(string) (string, error) { return "original", nil }}
	startDeliveryForTest(t, func(ctx context.Context) {
		deliverTexts(ctx, client, queue, nil, outputs, "")
	})
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		s, _ := queue.Snapshot(j.ID)
		if s.MediaPackage.ID != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	s, _ := queue.Snapshot(j.ID)
	if s.MediaPackage.ID == "" || !s.MediaDeferred || sender.texts != 0 || sender.files != 0 {
		t.Fatal("pause prevented full result availability or sent old notification")
	}
}

func TestLargePackageEntryIsDeliveredInOneShortResultNotification(t *testing.T) {
	queue, _ := jobs.Open(t.TempDir())
	outputs, _ := files.Open(t.TempDir())
	j, _ := queue.Enqueue("large-future", "new task", "owner", "original")
	task, _ := queue.Claim(time.Now())
	a, _ := outputs.Save("owner", "a", "a.txt", strings.NewReader("a"))
	b, _ := outputs.Save("owner", "b", "b.txt", strings.NewReader("b"))
	queue.Complete(jobs.Completion{ID: task.ID, Lease: task.Lease, Result: "answer", Outputs: []files.Ref{a, b}}, time.Now())
	large, _ := outputs.Save("owner", "large", "complete.zip", io.LimitReader(zeroReader{}, directMediaBytes+1))
	queue.SetMediaPackage(j.ID, large)
	sender := &fakeResultSender{}
	client := &liveResultSender{client: sender, contextFor: func(string) (string, error) { return "original", nil }}
	startDeliveryForTest(t, func(ctx context.Context) {
		deliverTexts(ctx, client, queue, nil, outputs, "https://example.com/wechat-files/")
	})
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		s, _ := queue.Snapshot(j.ID)
		if s.Status == "delivered" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	s, _ := queue.Snapshot(j.ID)
	if s.Status != "delivered" || !s.PartDelivered("package-link:"+large.ID) || s.PartDelivered(large.ID) || sender.texts != 1 || sender.files != 0 || !strings.Contains(s.DeliveryText, "/result/"+large.ID+"?") {
		t.Fatal("complete package link required an extra send or forged a file receipt")
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }
