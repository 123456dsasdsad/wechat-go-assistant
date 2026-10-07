package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/userinput"
	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
)

func pendingQuestionFixture(t *testing.T) (*inbound, jobs.Job, jobs.UserQuestion) {
	in := quoteFixture(t)
	in.handle(context.Background(), textMessage("task", "生成一张图"))
	task, _ := in.queue.Claim(time.Now())
	j, _ := in.queue.Snapshot(task.ID)
	b, e := in.queue.PublishQuestion(jobs.QuestionRequest{ID: j.ID, Lease: task.Lease, Request: userinput.Request{ID: "call", Questions: []userinput.Question{{ID: "format", Question: "选择图片格式", Options: []userinput.Option{{Label: "PNG"}, {Label: "SVG"}}}}}}, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if e = in.queue.RecordQuestionMessage(j.ID, b.ID, "format", "sent-question"); e != nil {
		t.Fatal(e)
	}
	return in, j, b
}
func TestQuotedOptionAnswersOriginalTaskAfterConversationSwitch(t *testing.T) {
	in, j, b := pendingQuestionFixture(t)
	in.handle(context.Background(), textMessage("switch", "新建会话 另一个任务"))
	m := textMessage("answer", "2")
	m.Items[0].Ref = &weixin.RefMessage{ServerID: "sent-question"}
	if e := in.handle(context.Background(), m); e != nil {
		t.Fatal(e)
	}
	saved, _ := in.queue.Snapshot(j.ID)
	if len(in.queue.History()) != 1 || saved.Questions[0].Answers["format"].Answers[0] != "SVG" || saved.Questions[0].ID != b.ID || !strings.Contains(in.client.(*fakeMessages).text, "继续执行") {
		t.Fatal("quote became another task", saved)
	}
	if e := in.handle(context.Background(), m); e != nil {
		t.Fatal("replay failed", e)
	}
}
func TestOrdinaryMessageStillQueuesWhileQuestionWaits(t *testing.T) {
	in, j, _ := pendingQuestionFixture(t)
	in.handle(context.Background(), textMessage("ordinary", "这是新任务，检查文件"))
	saved, _ := in.queue.Snapshot(j.ID)
	if !saved.WaitingForUser() || len(in.queue.History()) != 2 {
		t.Fatal("ordinary text consumed as answer")
	}
}
func TestQuotedFreeTextAndOldQuestionsNeverBecomeNewTasks(t *testing.T) {
	in, j, b := pendingQuestionFixture(t)
	m := textMessage("free", "我需要 PDF 格式")
	m.Items[0].Ref = &weixin.RefMessage{ServerID: "sent-question"}
	in.handle(context.Background(), m)
	saved, _ := in.queue.Snapshot(j.ID)
	if saved.Questions[0].Answers["format"].Answers[0] != "我需要 PDF 格式" {
		t.Fatal(saved)
	}
	old := textMessage("late", "1")
	old.Items[0].Ref = &weixin.RefMessage{ServerID: "sent-question"}
	in.handle(context.Background(), old)
	if len(in.queue.History()) != 1 || !strings.Contains(in.client.(*fakeMessages).text, "已回答") {
		t.Fatal("old answer enqueued")
	}
	if code := jobs.QuestionCode(b, 0); !strings.Contains(userQuestionText(j, b, 0), code) {
		t.Fatal("fallback code missing")
	}
}

type rejectedQuestionClient struct{ fakeMessages }

func (*rejectedQuestionClient) SendText(context.Context, weixin.Reply, string) (weixin.SendResult, error) {
	return weixin.SendResult{}, errors.New("rejected")
}
func TestDeliveryFailureNeverResolvesUserQuestion(t *testing.T) {
	in, j, b := pendingQuestionFixture(t)
	if e := deliverUserQuestion(context.Background(), &rejectedQuestionClient{}, in.queue, j, b, 0); e == nil {
		t.Fatal("failed send accepted")
	}
	saved, _ := in.queue.Snapshot(j.ID)
	if !saved.WaitingForUser() {
		t.Fatal("question automatically resolved")
	}
}

func TestAcceptedQuestionIDBindsBeforeSendReturnsEvenWithoutQuoteCache(t *testing.T) {
	in, j, b := pendingQuestionFixture(t)
	callback := quoteRecorder(nil, "bot", in.queue)
	callback(weixin.Reply{ToUserID: "owner", ClientID: "go-question-" + b.ID + "-1"}, weixin.SendResult{MessageID: "accepted-question-id"}, userQuestionText(j, b, 0), false)
	bound, batch, index, ok := in.queue.FindQuotedQuestion("owner", "accepted-question-id")
	if !ok || bound.ID != j.ID || batch.ID != b.ID || index != 0 {
		t.Fatal("accepted question not durably bound")
	}
	callback(weixin.Reply{ToUserID: "stranger", ClientID: "go-question-" + b.ID + "-1"}, weixin.SendResult{MessageID: "foreign-id"}, "", false)
	if _, _, _, ok := in.queue.FindQuotedQuestion("owner", "foreign-id"); ok {
		t.Fatal("foreign delivery bound to owner")
	}
}
