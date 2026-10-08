package main

import (
	"context"
	"errors"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/conversations"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/models"
	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
	"strings"
	"testing"
	"time"
)

func TestTwoQuestionsKeepTheirOwnIdentityAfterNewQuestionAndRename(t *testing.T) {
	defer metadb.CloseAll()
	sessions, _ := conversations.Open(t.TempDir() + "/sessions.json")
	cid := sessions.Current().ID
	now := time.Now()
	a := jobs.Job{ID: strings.Repeat("a", 24), ConversationID: cid, Input: "这个图有没有重新训练？", Result: "没有，只重绘了旧日志。", Created: now}
	b := jobs.Job{ID: strings.Repeat("b", 24), ConversationID: cid, Input: "帮我重新训练并修图", Result: "已启动，尚未完成。", Created: now.Add(time.Minute)}
	sessions.RecordTask(cid, b.Input, nil, b.Created)
	sessions.Handle("rename", "会话命名 新的标题")
	first := resultText(a, sessions, nil, "")
	second := resultText(b, sessions, nil, "")
	if !strings.Contains(first, "原问题：这个图有没有重新训练？") || strings.Contains(first, "原问题：帮我重新训练") || !strings.Contains(second, "原问题：重新训练并修图") {
		t.Fatal("latest question replaced older question", first, second)
	}
}

func TestResultPageReplyKeepsOriginalSessionNumberAfterSwitch(t *testing.T) {
	defer metadb.CloseAll()
	sessions, _ := conversations.Open(t.TempDir() + "/sessions.json")
	original := sessions.Current()
	outputs, _ := files.Open(t.TempDir())
	j := jobs.Job{ID: strings.Repeat("a", 24), Owner: "owner", ConversationID: original.ID, Input: "原会话任务", Result: "原会话答复", Created: time.Now()}
	sessions.Handle("switch-result-session", "新建会话 其他会话")
	text := resultText(j, sessions, outputs, "https://example.com/wechat-files/")
	if !strings.HasPrefix(text, "会话 1 · ") || strings.Contains(text, "其他会话") || !strings.Contains(text, "完整答复与全部") {
		t.Fatal("result page response lost its original session", text)
	}
}
func TestSupersededMediaDoesNotInterleaveButCanBeRequested(t *testing.T) {
	defer metadb.CloseAll()
	queue, _ := jobs.Open(t.TempDir())
	outputs, _ := files.Open(t.TempDir())
	ref, _ := outputs.Save("owner", "figure", "figure.png", strings.NewReader("image"))
	var tasks []jobs.Job
	for _, source := range []string{"old-version", "new-version"} {
		j, _ := queue.EnqueueConversation(source, source, "owner", "ctx", models.Choice{Model: "gpt-6-sol", Effort: "high"}, nil, "aaaaaaaa")
		task, _ := queue.Claim(time.Now())
		queue.Complete(jobs.Completion{ID: task.ID, Lease: task.Lease, Result: source, Outputs: []files.Ref{ref}}, time.Now())
		queue.CommitPart(j.ID, "text")
		tasks = append(tasks, j)
	}
	selected := selectMediaJobs(queue.History())
	if len(selected) != 1 || selected[0].ID != tasks[1].ID {
		t.Fatal("old and new versions interleaved", selected)
	}
	if _, e := queue.RequestMedia("another-owner", tasks[0].ID[:8]); e == nil {
		t.Fatal("cross-owner request")
	}
	if _, e := queue.RequestMedia("owner", tasks[0].ID[:8]); e != nil {
		t.Fatal(e)
	}
	selected = selectMediaJobs(queue.History())
	if len(selected) != 1 || selected[0].ID != tasks[0].ID {
		t.Fatal("explicit older result cannot be resumed")
	}
}

type chunkFailureSender struct {
	fakeResultSender
	seen       []string
	failSecond bool
}

func (s *chunkFailureSender) SendText(_ context.Context, _ weixin.Reply, text string) (weixin.SendResult, error) {
	if s.failSecond && len(s.seen) == 1 {
		s.failSecond = false
		return weixin.SendResult{}, errors.New("temporary")
	}
	s.seen = append(s.seen, text)
	return weixin.SendResult{}, nil
}
func TestLongReplyRetryFreezesTextAndDoesNotRepeatFirstChunk(t *testing.T) {
	defer metadb.CloseAll()
	dir := t.TempDir()
	queue, _ := jobs.Open(dir)
	queue.Enqueue("long", "original question", "owner", "ctx")
	task, _ := queue.Claim(time.Now())
	queue.Complete(jobs.Completion{ID: task.ID, Lease: task.Lease, Result: "answer"}, time.Now())
	original := "原问题：original question\n" + strings.Repeat("中", 4500)
	sender := &chunkFailureSender{failSecond: true}
	if e := deliverJob(context.Background(), sender, queue, nil, "", queue.Ready()[0], original); e == nil {
		t.Fatal("failure ignored")
	}
	queue, _ = jobs.Open(dir)
	if e := deliverJob(context.Background(), sender, queue, nil, "", queue.Ready()[0], "changed question and answer"); e != nil {
		t.Fatal(e)
	}
	if len(sender.seen) != 3 || strings.Join(sender.seen, "") != original {
		t.Fatal("reply duplicated or changed across restart")
	}
	j, _ := queue.Snapshot(task.ID)
	if !j.PartDelivered("text") || !j.PartDelivered("text:0") {
		t.Fatal("missing durable chunk receipts")
	}
	if e := queue.CommitPart(task.ID, "text:99"); e == nil {
		t.Fatal("arbitrary receipt accepted")
	}
}

type labelledSender struct {
	fakeResultSender
	events []string
}

func (s *labelledSender) SendText(_ context.Context, _ weixin.Reply, text string) (weixin.SendResult, error) {
	s.events = append(s.events, text)
	return weixin.SendResult{}, nil
}
func (s *labelledSender) SendImage(context.Context, weixin.Reply, weixin.Uploaded) (weixin.SendResult, error) {
	s.events = append(s.events, "image")
	return weixin.SendResult{}, nil
}
func TestAnInterveningReplyRestoresTheImageBatchIdentity(t *testing.T) {
	defer metadb.CloseAll()
	sender := &labelledSender{}
	live := &liveResultSender{client: sender, contextFor: func(string) (string, error) { return "fresh", nil }}
	ctx := context.Background()
	old := strings.Repeat("a", 24)
	newJob := strings.Repeat("b", 24)
	r := weixin.Reply{ToUserID: "owner", ClientID: "go-output-" + old + "-one", ContextToken: "original-media-context"}
	live.SendCaptionedImage(ctx, r, weixin.Uploaded{}, "old question")
	live.SendText(ctx, weixin.Reply{ToUserID: "owner", ClientID: "go-result-" + newJob}, "new question reply")
	live.SendCaptionedImage(ctx, r, weixin.Uploaded{}, "old question")
	live.SendCaptionedImage(ctx, r, weixin.Uploaded{}, "old question")
	if strings.Join(sender.events, "|") != "old question|image|new question reply|old question|image|image" {
		t.Fatal("images appear to answer the intervening question", sender.events)
	}
}

func TestNewestTextPrecedesOldResultAndMediaWaitsForText(t *testing.T) {
	defer metadb.CloseAll()
	queue, _ := jobs.Open(t.TempDir())
	sessions, _ := conversations.Open(t.TempDir() + "/sessions.json")
	var ids []string
	for _, name := range []string{"old", "new"} {
		j, _ := queue.Enqueue(name, name, "owner", "ctx")
		task, _ := queue.Claim(time.Now())
		queue.Complete(jobs.Completion{ID: task.ID, Lease: task.Lease, Result: name}, time.Now())
		ids = append(ids, j.ID)
	}
	history := queue.History()
	history[0].Outputs = []files.Ref{{ID: strings.Repeat("c", 24)}}
	history[0].DeliveryParts = []string{"text"}
	if len(selectMediaJobs(history)) != 0 {
		t.Fatal("media can consume reply quota while a newer text waits")
	}
	history[1].VerificationOnly = true
	if len(selectMediaJobs(history)) != 1 {
		t.Fatal("private verification fixture blocked normal image delivery")
	}
	sender := &blockedMediaSender{started: make(chan struct{}), texts: make(chan string, 4)}
	live := &liveResultSender{client: sender, contextFor: func(string) (string, error) { return "ctx", nil }}
	startDeliveryForTest(t, func(ctx context.Context) {
		deliverTexts(ctx, live, queue, sessions, nil, "")
	})
	select {
	case id := <-sender.texts:
		if id != "go-result-"+ids[1] {
			t.Fatal("old answer consumed the first reply slot")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("text not delivered")
	}
}
