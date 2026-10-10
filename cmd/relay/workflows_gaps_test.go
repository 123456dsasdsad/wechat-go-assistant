package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/materials"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/quotes"
	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
)

func TestMaterialDraftContinuesAcrossSessionSwitchAndStoreRestart(t *testing.T) {
	defer metadb.CloseAll()
	in := quoteFixture(t)
	dir := filepath.Join(t.TempDir(), "materials")
	var err error
	in.materials, err = materials.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { in.materials.Close() })
	ctx := context.Background()
	handle := func(id, text string) {
		t.Helper()
		if err := in.handle(ctx, textMessage(id, text)); err != nil {
			t.Fatal(err)
		}
	}
	a := in.sessions.Current().ID
	handle("begin", "开始收集 隔离材料")
	handle("one", "来源一：周五提交实验记录")
	p, active, err := in.materials.Active(in.owner)
	if err != nil || !active {
		t.Fatal(p, active, err)
	}
	handle("switch", "新建会话 B")
	b := in.sessions.Current().ID
	handle("two", "来源二：保留原始图表")
	handle("cancel", "取消收集")
	p, err = in.materials.Get(in.owner, p.ID)
	if err != nil || p.State != "saved" || p.Conversation != a || len(p.Entries) != 2 {
		t.Fatal(p, err)
	}
	if err = in.materials.Close(); err != nil {
		t.Fatal(err)
	}
	in.materials, err = materials.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	handle("resume", "继续收集 "+p.ID[:8])
	handle("three", "来源三：下周讨论")
	handle("finish", "发完了")
	p, err = in.materials.Get(in.owner, p.ID)
	if err != nil || p.State != "saved" || p.Conversation != a || len(p.Entries) != 3 || len(in.queue.History()) != 0 || in.sessions.Current().ID != b {
		t.Fatal("draft or conversation was lost, or collection called AI", p, err)
	}
	handle("submit", "处理材料 "+p.ID[:8]+"：总结原文")
	rows := in.queue.History()
	if len(rows) != 1 || rows[0].ConversationID != a || in.sessions.Current().ID != b {
		t.Fatal("submission followed the selected session rather than the material session", rows)
	}
}

func TestQuotedResendAndRetryOnlyAffectReferencedTask(t *testing.T) {
	defer metadb.CloseAll()
	in := workflowFixture(t)
	ctx := context.Background()
	a := in.sessions.Current().ID
	first, err := in.queue.EnqueueConversation("first", "first input", in.owner, "context", in.preferences.Current(), nil, a)
	if err != nil {
		t.Fatal(err)
	}
	task, err := in.queue.Claim(time.Now())
	if err != nil || task == nil {
		t.Fatal(task, err)
	}
	ref, err := in.files.Save(in.owner, "output", "fixture.txt", strings.NewReader("result fixture"))
	if err != nil {
		t.Fatal(err)
	}
	if err = in.queue.Complete(jobs.Completion{ID: task.ID, Lease: task.Lease, Result: "first result", Outputs: []files.Ref{ref}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = in.queue.CommitPart(first.ID, "text"); err != nil {
		t.Fatal(err)
	}
	if err = in.quotes.Put(in.botID, in.owner, "first-result", quotes.Content{Text: "first result", JobID: first.ID, ConversationID: a}); err != nil {
		t.Fatal(err)
	}
	if err = in.handle(ctx, textMessage("select", "新建会话 B")); err != nil {
		t.Fatal(err)
	}
	b := in.sessions.Current().ID
	other, err := in.queue.EnqueueConversation("other", "other input", in.owner, "other context", in.preferences.Current(), nil, b)
	if err != nil {
		t.Fatal(err)
	}
	quote := func(id, text string) {
		t.Helper()
		m := textMessage(id, text)
		m.Items[0].Ref = &weixin.RefMessage{ServerID: "first-result"}
		if err := in.handle(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	quote("resend", "补发这个")
	j, _ := in.queue.Snapshot(first.ID)
	u, _ := in.queue.Snapshot(other.ID)
	if !j.MediaRequested || !j.PartDelivered("text") || u.MediaRequested || len(in.queue.History()) != 2 || in.sessions.Current().ID != b {
		t.Fatal("resend reran AI, lost accepted parts, or touched another task", j, u)
	}
	// A stopped task is retryable. The retry must preserve A's context while B remains selected.
	failed, err := in.queue.EnqueueConversation("failed", "failed input", in.owner, "context", in.preferences.Current(), nil, a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = in.queue.Cancel(in.owner, failed.ID); err != nil {
		t.Fatal(err)
	}
	if err = in.quotes.Put(in.botID, in.owner, "failed-result", quotes.Content{Text: "failure", JobID: failed.ID, ConversationID: a}); err != nil {
		t.Fatal(err)
	}
	m := textMessage("retry", "重试这个")
	m.Items[0].Ref = &weixin.RefMessage{ServerID: "failed-result"}
	for i := 0; i < 2; i++ {
		if err = in.handle(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	rows := in.queue.History()
	if len(rows) != 4 {
		t.Fatal("duplicate quoted retry", len(rows))
	}
	retries := 0
	for _, j := range rows {
		if j.ID != failed.ID && j.Input == failed.Input {
			retries++
			if j.ConversationID != a {
				t.Fatal("retry switched conversation", j.ConversationID)
			}
		}
	}
	if retries != 1 || in.sessions.Current().ID != b {
		t.Fatal(retries, in.sessions.Current().ID)
	}
	u, _ = in.queue.Snapshot(other.ID)
	if u.Status != "queued" || u.CancelRequested {
		t.Fatal("another task changed", u)
	}
}
