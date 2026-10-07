package jobs

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/userinput"
)

func testQuestionRequest() userinput.Request {
	return userinput.Request{ID: "call-1", Questions: []userinput.Question{{ID: "format", Question: "选择格式", Options: []userinput.Option{{Label: "PNG"}, {Label: "SVG"}}}, {ID: "title", Question: "填写标题"}}}
}
func TestQuestionPersistenceOwnershipAndNoPrematureCompletion(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	j, _ := s.Enqueue("source", "work", "owner", "context")
	task, _ := s.Claim(time.Now())
	r := QuestionRequest{ID: j.ID, Lease: task.Lease, Request: testQuestionRequest()}
	b, e := s.PublishQuestion(r, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.PublishQuestion(QuestionRequest{ID: j.ID, Lease: "stale", Request: r.Request}, time.Now()); e == nil {
		t.Fatal("stale publish accepted")
	}
	if e = s.Complete(Completion{ID: j.ID, Lease: task.Lease, Result: "assumed answer"}, time.Now()); e == nil {
		t.Fatal("completed unanswered question")
	}
	if e = s.RecordQuestionMessage(j.ID, b.ID, "format", "wechat-question-1"); e != nil {
		t.Fatal(e)
	}
	if _, _, _, ok := s.FindQuotedQuestion("stranger", "wechat-question-1"); ok {
		t.Fatal("cross-owner quote")
	}
	if _, e = s.AnswerQuestion("stranger", j.ID, b.ID, "format", "answer", "2", time.Now()); e == nil {
		t.Fatal("cross-owner answer")
	}
	if done, e := s.AnswerQuestion("owner", j.ID, b.ID, "format", "answer", "2", time.Now()); e != nil || done {
		t.Fatal(done, e)
	}
	s, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	_, saved, i, ok := s.FindQuotedQuestion("owner", "wechat-question-1")
	if !ok || i != 0 || saved.Answers["format"].Answers[0] != "SVG" {
		t.Fatal(saved)
	}
	if _, e = s.AnswerQuestion("owner", j.ID, b.ID, "format", "answer", "2", time.Now()); e != nil {
		t.Fatal("replay not idempotent", e)
	}
	if _, e = s.AnswerQuestion("owner", j.ID, b.ID, "format", "answer", "1", time.Now()); e == nil {
		t.Fatal("conflicting replay")
	}
	if done, e := s.AnswerQuestion("owner", j.ID, b.ID, "title", "answer-2", "用户的标题", time.Now()); e != nil || !done {
		t.Fatal(done, e)
	}
	if e = s.ResolveQuestion(QuestionRequest{ID: j.ID, Lease: task.Lease, QuestionID: b.ID}, time.Now()); e != nil {
		t.Fatal(e)
	}
	if e = s.Complete(Completion{ID: j.ID, Lease: task.Lease, Result: "answered work"}, time.Now()); e != nil {
		t.Fatal(e)
	}
}
func TestExpiredAttemptCannotConsumeOldQuotedAnswer(t *testing.T) {
	s, _ := Open(t.TempDir())
	j, _ := s.Enqueue("s", "work", "owner", "ctx")
	now := time.Now()
	task, _ := s.Claim(now)
	b, _ := s.PublishQuestion(QuestionRequest{ID: j.ID, Lease: task.Lease, Request: testQuestionRequest()}, now)
	s.RecordQuestionMessage(j.ID, b.ID, "format", "old-message")
	if _, e := s.AnswerQuestion("owner", j.ID, b.ID, "format", "a", "1", now.Add(5*time.Minute)); e == nil {
		t.Fatal("expired answer")
	}
	newTask, _ := s.Claim(now.Add(5 * time.Minute))
	if newTask == nil || newTask.Lease == task.Lease {
		t.Fatal(newTask)
	}
	if _, e := s.AnswerQuestion("owner", j.ID, b.ID, "format", "b", "1", now.Add(5*time.Minute)); e == nil {
		t.Fatal("old attempt answer")
	}
	old, _, _, ok := s.FindQuotedQuestion("owner", "old-message")
	if !ok || old.Questions[0].State != "canceled" {
		t.Fatal("old quote not retained for rejection")
	}
}
func TestHTTPQuestionWaitBlocksUntilEveryAnswer(t *testing.T) {
	s, _ := Open(t.TempDir())
	j, _ := s.Enqueue("s", "work", "owner", "ctx")
	task, _ := s.Claim(time.Now())
	key := strings.Repeat("k", 32)
	server := httptest.NewServer(Handler(s, key))
	defer server.Close()
	c := userinput.Connection{URL: server.URL, Key: key, JobID: j.ID, Lease: task.Lease}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan userinput.Response, 1)
	errs := make(chan error, 1)
	go func() { v, e := c.Wait(ctx, testQuestionRequest()); done <- v; errs <- e }()
	var b UserQuestion
	for until := time.Now().Add(time.Second); time.Now().Before(until); {
		saved, _ := s.Snapshot(j.ID)
		if len(saved.Questions) > 0 {
			b = saved.Questions[0]
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if b.ID == "" {
		t.Fatal("question not published")
	}
	select {
	case <-done:
		t.Fatal("invented automatic answer")
	default:
	}
	s.AnswerQuestion("owner", j.ID, b.ID, "format", "a", "2", time.Now())
	select {
	case <-done:
		t.Fatal("did not wait for second answer")
	default:
	}
	s.AnswerQuestion("owner", j.ID, b.ID, "title", "b", "custom title", time.Now())
	select {
	case response := <-done:
		if e := <-errs; e != nil || response.Answers["format"].Answers[0] != "SVG" {
			t.Fatal(response, e)
		}
	case <-ctx.Done():
		t.Fatal("did not resume")
	}
	saved, _ := s.Snapshot(j.ID)
	if saved.Questions[0].State != "resolved" {
		t.Fatal(saved.Questions)
	}
}
