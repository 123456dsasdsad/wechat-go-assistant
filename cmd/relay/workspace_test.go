package main

import (
	"encoding/json"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/assistant"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/conversations"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceAuthenticationAndSharedPreferences(t *testing.T) {
	defer metadb.CloseAll()
	in := inboundFixture(t)
	in.templates, _ = assistant.OpenTemplates(filepath.Join(t.TempDir(), "templates.json"))
	handler := in.workspaceHandler("")
	req := httptest.NewRequest("GET", "https://example.com/wechat-files/manage/api", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	token, _ := in.files.Grant("owner", "login")
	login := `{"action":"login","token":"` + token + `"}`
	req = httptest.NewRequest("POST", "https://example.com/wechat-files/manage/api", strings.NewReader(login))
	req.Header.Set("Origin", "https://evil.example")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal("CSRF", w.Code)
	}
	req.Header.Set("Origin", "https://example.com")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	cookie := w.Result().Cookies()[0]
	req = httptest.NewRequest("GET", "https://example.com/wechat-files/manage/api", nil)
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var snapshot map[string]any
	if json.Unmarshal(w.Body.Bytes(), &snapshot) != nil || snapshot["tasks"] == nil {
		t.Fatal(w.Body.String())
	}
	cid := in.sessions.Current().ID
	in.sessions.UpdateProfile(cid, conversations.Profile{Project: "demo", Notes: "use actual files"})
	in.preferences.Handle("scope", "推理强度 low", cid)
	j, e := in.enqueue("web", "analyze", "owner", "context", cid, nil)
	if e != nil || j.Project != "demo" || j.Effort != "low" || !strings.Contains(j.Memory, "use actual files") {
		t.Fatal(j, e)
	}
	if e = in.handle(t.Context(), textMessage("settings", "任务列表")); e != nil {
		t.Fatal(e)
	}
	if len(in.queue.History()) != 1 {
		t.Fatal("control called AI")
	}
}
