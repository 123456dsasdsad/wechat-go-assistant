package main

import (
	"context"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/materials"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/quotes"
	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDeliverySummaryCountsAcceptedPackageAsCoveringOriginals(t *testing.T) {
	j := jobs.Job{Outputs: []files.Ref{{ID: "one"}, {ID: "two"}}, MediaPackage: files.Ref{ID: "bundle"}, DeliveryParts: []string{"text", "package-link:bundle"}}
	d := deliverySummary(j)
	if d["missing"] != 0 || d["package_accepted"] != true {
		t.Fatal("accepted originals shown as missing", d)
	}
	j.DeliveryParts = []string{"text", "one"}
	if d = deliverySummary(j); d["missing"] != 1 {
		t.Fatal("missing original hidden", d)
	}
}

func workflowFixture(t *testing.T) *inbound {
	in := quoteFixture(t)
	var e error
	in.materials, e = materials.Open(filepath.Join(t.TempDir(), "materials"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { in.materials.Close() })
	return in
}
func TestMaterialCollectionNeverQueuesUntilExplicitSubmit(t *testing.T) {
	defer metadb.CloseAll()
	in := workflowFixture(t)
	ctx := context.Background()
	for _, m := range []weixin.Message{textMessage("start", "开始收集 导师意见"), textMessage("m1", "小王周五前修改摘要"), textMessage("m1", "小王周五前修改摘要"), textMessage("m2", strings.Repeat("长聊天", 4000)), textMessage("project", "创建项目 毕业论文")} {
		if e := in.handle(ctx, m); e != nil {
			t.Fatal(e)
		}
	}
	if len(in.queue.History()) != 0 {
		t.Fatal("collection triggered AI")
	}
	p, ok, e := in.materials.Active(in.owner)
	if e != nil || !ok || len(p.Entries) != 2 {
		t.Fatal(p, ok, e)
	}
	if ps, e := in.materials.Projects(in.owner); e != nil || len(ps) != 1 {
		t.Fatal("project command captured as source", ps, e)
	}
	for i := 0; i < 2; i++ {
		if e := in.handle(ctx, textMessage("finish", "发完了：提取行动清单")); e != nil {
			t.Fatal(e)
		}
	}
	if len(in.queue.History()) != 1 {
		t.Fatal("submit duplicated")
	}
	j := in.queue.History()[0]
	if j.ConversationID != p.Conversation || len(j.Attachments) != 1 || !strings.Contains(j.Input, "原文依据") {
		t.Fatal(j)
	}
	f, e := in.files.OpenBlob(j.Attachments[0])
	if e != nil {
		t.Fatal(e)
	}
	f.Close()
}
func TestQuotedOperationsStayBoundToOriginalConversation(t *testing.T) {
	defer metadb.CloseAll()
	in := workflowFixture(t)
	ctx := context.Background()
	a := in.sessions.Current().ID
	if e := in.handle(ctx, textMessage("a", "A任务")); e != nil {
		t.Fatal(e)
	}
	job := in.queue.History()[0]
	in.quotes.Put(in.botID, in.owner, "out-a", quotes.Content{Text: "A任务", JobID: job.ID, ConversationID: a})
	if e := in.handle(ctx, textMessage("new", "新建会话 B")); e != nil {
		t.Fatal(e)
	}
	b := in.sessions.Current().ID
	m := textMessage("analysis", "继续研究这个问题")
	m.Items[0].Ref = &weixin.RefMessage{ServerID: "out-a"}
	if e := in.handle(ctx, m); e != nil {
		t.Fatal(e)
	}
	if rows := in.queue.History(); len(rows) != 2 || rows[1].ConversationID != a || in.sessions.Current().ID != b {
		t.Fatal(rows)
	}
	m = textMessage("stop", "停止这个")
	m.Items[0].Ref = &weixin.RefMessage{ServerID: "out-a"}
	if e := in.handle(ctx, m); e != nil {
		t.Fatal(e)
	}
	old, _ := in.queue.Snapshot(job.ID)
	if old.Status != "done" || old.Error != "user_canceled" {
		t.Fatal(old)
	}
	next := in.queue.History()[1]
	if next.CancelRequested || next.Status != "queued" {
		t.Fatal("stopped a different task", next)
	}
}
func TestWorkflowActionsDenyForeignPackAndKeepSubmissionIdempotent(t *testing.T) {
	defer metadb.CloseAll()
	in := workflowFixture(t)
	p, e := in.materials.Begin("other", "source", "private", in.sessions.Current().ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = in.workflowAction(in.owner, "reply-context", workspaceRequest{Action: "material_get", ID: p.ID}); e == nil {
		t.Fatal("foreign pack visible")
	}
	p, e = in.materials.Begin(in.owner, "own", "own", in.sessions.Current().ID)
	if e != nil {
		t.Fatal(e)
	}
	in.materials.Append(in.owner, p.ID, materials.Entry{ID: "one", Text: "周五交稿"})
	for i := 0; i < 2; i++ {
		_, _, e = in.workflowAction(in.owner, "reply-context", workspaceRequest{Action: "material_submit", ID: p.ID, Input: "总结"})
		if e != nil {
			t.Fatal(e)
		}
	}
	if len(in.queue.History()) != 1 {
		t.Fatal("duplicate submission")
	}
	task, _ := in.queue.Claim(time.Now())
	if task == nil {
		t.Fatal("no task")
	}
}
