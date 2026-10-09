package main

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/watches"
)

func TestWatchNativeStatusDoesNotCreateDuplicateAIWork(t *testing.T) {
	defer metadb.CloseAll()
	in := inboundFixture(t)
	s := in.sessions.Current()
	in.outputs = in.files
	in.watches, _ = watches.Open(filepath.Join(t.TempDir(), "watches.json"))
	_, _, e := in.watches.Put("owner", watches.Update{Conversation: s.ID, Thread: "01a00000-0000-7000-8000-000000000001", Title: "论文实验", State: "running", Text: "60 个实验已配置", Sequence: 1, Observed: time.Now()}, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	for _, input := range []string{"任务状态", "任务列表", "查看进度", "进度", "补充：继续训练", "请重新训练"} {
		if e := in.handle(t.Context(), textMessage(input, input)); e != nil {
			t.Fatal(e)
		}
		if len(in.queue.History()) != 0 {
			t.Fatal("monitor queued duplicate AI task", input)
		}
		if input == "任务列表" && !strings.Contains(in.client.(*fakeMessages).text, "60 个实验") {
			t.Fatal("monitor listed unrelated jobs")
		}
	}
	v, _ := in.watches.Current("owner", s.ID)
	link := in.watchLink(v)
	req := httptest.NewRequest("GET", link+"&format=json", nil)
	rec := httptest.NewRecorder()
	in.watchHandler().ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "60 个实验") {
		t.Fatal(rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest("GET", "/wechat-files/watch/"+v.ID+"?format=json", nil)
	rec = httptest.NewRecorder()
	in.watchHandler().ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatal("unsigned progress accessible")
	}
	_, e = in.workspaceAction("owner", workspaceRequest{Action: "task", Input: "restart", Conversation: s.ID}, "")
	if e == nil || len(in.queue.History()) != 0 {
		t.Fatal("portal queued duplicate training")
	}
	if e := in.handle(t.Context(), textMessage("new", "新建会话 正常任务")); e != nil {
		t.Fatal(e)
	}
	if e := in.handle(t.Context(), textMessage("work", "计算17乘23")); e != nil || len(in.queue.History()) != 1 {
		t.Fatal("regular sessions stopped working", e)
	}
}
