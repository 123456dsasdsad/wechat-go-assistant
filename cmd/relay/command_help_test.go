package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
)

func TestHelpDoesNotQueueOrChangeRunningConversation(t *testing.T) {
	defer metadb.CloseAll()
	in := inboundFixture(t)
	if e := in.handle(t.Context(), textMessage("task", "运行实验")); e != nil {
		t.Fatal(e)
	}
	if j, e := in.queue.Claim(time.Now()); e != nil || j == nil {
		t.Fatal(j, e)
	}
	before := in.queue.History()
	session, choice := in.sessions.Current(), in.preferences.Current(in.sessions.Current().ID)
	for i, input := range []string{"指令", "帮助", "HELP", "功能列表", "指令 5", "指令 资料库", "查指令 上传", "查询指令 账号 上传", "指令详情 完成收录", "查指令 不存在的功能"} {
		if e := in.handle(t.Context(), textMessage(fmt.Sprintf("help-%d", i), input)); e != nil {
			t.Fatal(input, e)
		}
		if !reflect.DeepEqual(in.queue.History(), before) || !reflect.DeepEqual(in.sessions.Current(), session) || !reflect.DeepEqual(in.preferences.Current(session.ID), choice) {
			t.Fatalf("help changed a task, conversation or setting: %s", input)
		}
		if in.client.(*fakeMessages).text == "" {
			t.Fatal("missing help reply", input)
		}
	}
}

func TestHelpParsingAndLookup(t *testing.T) {
	for _, input := range []string{"指令", "指令列表", "help", "帮助：模型", "指令\t5", "查指令 账号 上传", "指令详情 完成收录"} {
		if _, ok := commandHelpText(input); !ok {
			t.Fatal("not handled", input)
		}
	}
	for _, input := range []string{"5", "helpful", "指令执行时要遵守项目要求", "帮我查一下上传功能", "研究主题 帮助"} {
		if _, ok := commandHelpText(input); ok {
			t.Fatal("ordinary message intercepted", input)
		}
	}
	byNumber, _ := commandHelpText("指令 5")
	byName, _ := commandHelpText("指令 资料库")
	if byNumber != byName || !strings.Contains(byName, "完成收录") || !strings.Contains(byName, "查资料 <关键词>") {
		t.Fatal("category lookup inconsistent", byName)
	}
	d := lookupCommands("", "账号 上传")
	if len(d.Matches) != 3 || d.Matches[0].Syntax != "上传账号" || d.Matches[1].Syntax != "管理账号" || d.Matches[2].Syntax != "上传账号状态" {
		t.Fatal("keyword search did not narrow results", d.Matches)
	}
	d = lookupCommands("", "压缩包")
	if len(d.Matches) == 0 || d.Matches[0].Syntax != "上传文件" {
		t.Fatal("synonym search missing", d.Matches)
	}
	d = lookupCommands("模型", "上传")
	if len(d.Matches) != 0 {
		t.Fatal("search escaped category", d.Matches)
	}
	text, _ := commandHelpText("查指令 不存在的功能")
	if !strings.Contains(text, "未找到") || !strings.Contains(text, "发送“指令”") {
		t.Fatal("missing recovery advice", text)
	}
	for _, g := range commandGroups {
		text, _ := commandHelpText("指令 " + g.Name)
		if len(text) > 6000 {
			t.Fatal("category reply too large", g.Name, len(text))
		}
		for _, entry := range g.Entries {
			if entry.Syntax == "" || entry.Purpose == "" || entry.Example == "" {
				t.Fatal("incomplete catalog entry", g.Name, entry)
			}
		}
	}
}

func TestWorkbenchCommandLookupIsAuthenticatedAndIndependentOfReplyToken(t *testing.T) {
	defer metadb.CloseAll()
	in := inboundFixture(t)
	handler := in.workspaceHandler(filepath.Join(t.TempDir(), "missing-weixin-state.json"))
	token, e := in.files.Grant("owner", "lookup-login")
	if e != nil {
		t.Fatal(e)
	}
	for _, test := range []struct {
		origin, cookie string
		status         int
	}{{"https://example.com", "", 401}, {"https://evil.example", token, 403}, {"https://example.com", token, 200}} {
		body := `{"action":"commands","source":"0123456789abcdef01234567","name":"账号","input":"上传"}`
		r := httptest.NewRequest("POST", "https://example.com/wechat-files/manage/api", strings.NewReader(body))
		r.Header.Set("Origin", test.origin)
		if test.cookie != "" {
			r.AddCookie(&http.Cookie{Name: "wechat_manage", Value: test.cookie})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != test.status {
			t.Fatal(test.status, w.Code, w.Body.String())
		}
		if w.Code == 200 {
			var d helpDirectory
			if e := json.Unmarshal(w.Body.Bytes(), &d); e != nil || len(d.Groups) != 13 || len(d.Matches) != 3 {
				t.Fatal(d, e)
			}
		}
	}
	if len(in.queue.History()) != 0 {
		t.Fatal("workbench lookup queued an AI task")
	}
}
