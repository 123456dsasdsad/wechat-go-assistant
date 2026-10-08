package main

import (
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestTaskPageProtectsJobScopeAndDoesNotExposePrivateFields(t *testing.T) {
	defer metadb.CloseAll()
	queue, _ := jobs.Open(t.TempDir())
	outputs, _ := files.Open(t.TempDir())
	job, _ := queue.Enqueue("request", "password=private-input", "private-owner", "private-reply-context")
	task, _ := queue.Claim(time.Now())
	ref, _ := outputs.Save("private-owner", "output", "figure.png", strings.NewReader("test-image-bytes"))
	queue.Complete(jobs.Completion{ID: task.ID, Lease: task.Lease, Result: "actual answer", Outputs: []files.Ref{ref}}, time.Now())
	handler := taskStatusHandler(queue, outputs, "https://example.com/wechat-files/")
	signed, _ := outputs.TaskLink("https://example.com/wechat-files/", job.Owner, job.ID)
	u, _ := url.Parse(signed)
	q := u.Query()
	q.Set("format", "json")
	u.RawQuery = q.Encode()
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", u.String(), nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "actual answer") {
		t.Fatal("signed result not accessible", w.Code)
	}
	if !strings.Contains(w.Body.String(), "question") || !strings.Contains(w.Body.String(), "内容已省略") {
		t.Fatal("question identity absent or not redacted")
	}
	for _, private := range []string{"private-input", "private-owner", "private-reply-context", task.Lease} {
		if strings.Contains(w.Body.String(), private) {
			t.Fatal("private job field exposed")
		}
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/wechat-files/task/"+job.ID+"?format=json", nil))
	if w.Code != 401 {
		t.Fatal("anonymous job access allowed")
	}
	q.Set("file", strings.Repeat("f", 24))
	u.RawQuery = q.Encode()
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", u.String(), nil))
	if w.Code != 404 {
		t.Fatal("task allowed unrelated output")
	}
}

func TestTaskPageShowsProgressBeforeCompletion(t *testing.T) {
	defer metadb.CloseAll()
	queue, _ := jobs.Open(t.TempDir())
	outputs, _ := files.Open(t.TempDir())
	j, _ := queue.Enqueue("live", "检查资料", "owner", "context")
	task, _ := queue.Claim(time.Now())
	if e := queue.UpdateProgress(jobs.ProgressUpdate{ID: j.ID, Lease: task.Lease, Sequence: 1, Text: "已读入资料，开始分析"}, time.Now()); e != nil {
		t.Fatal(e)
	}
	signed, _ := outputs.TaskLink("https://example.com/wechat-files/", j.Owner, j.ID)
	u, _ := url.Parse(signed)
	q := u.Query()
	q.Set("format", "json")
	u.RawQuery = q.Encode()
	w := httptest.NewRecorder()
	taskStatusHandler(queue, outputs, "https://example.com/wechat-files/").ServeHTTP(w, httptest.NewRequest("GET", u.String(), nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "已读入资料，开始分析") || !strings.Contains(w.Body.String(), `"state":"running"`) || !strings.Contains(w.Body.String(), `"result":""`) {
		t.Fatal("running output missing", w.Body.String())
	}
}
