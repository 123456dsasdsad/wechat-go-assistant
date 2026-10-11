package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/library"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
)

func TestCampusAppManagementAuthProxyAndPathCompatibility(t *testing.T) {
	defer metadb.CloseAll()
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, `<script>fetch('/api/projects')</script><link href="/style.css"><a href="https://external.example/path">external</a>`)
	}))
	defer app.Close()
	s, _ := library.Open(t.TempDir())
	defer s.Close()
	site, e := s.AddCampusApp(context.Background(), "owner", "Writing", app.URL)
	if e != nil {
		t.Fatal(e)
	}
	up := httptest.NewServer(library.Handler(s, strings.Repeat("k", 32)))
	defer up.Close()
	in := inboundFixture(t)
	in.library, _ = library.NewClient(up.URL, strings.Repeat("k", 32))
	h := in.workspaceHandler("")
	path := "/wechat-files/manage/apps/" + site.ID + "/"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", path, nil))
	if rr.Code != 401 {
		t.Fatal("unauthenticated proxy", rr.Code)
	}
	cookie, e := in.files.Grant("owner", "app-test")
	if e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("GET", path, nil)
	r.AddCookie(&http.Cookie{Name: "wechat_manage", Value: cookie})
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "fetch('"+path+"api/projects')") || !strings.Contains(rr.Body.String(), `href="`+path+`style.css"`) || !strings.Contains(rr.Body.String(), "https://external.example/path") {
		t.Fatal("path rewrite", rr.Code, rr.Body.String())
	}
	r = httptest.NewRequest("POST", path+"api/projects", strings.NewReader(`{}`))
	r.AddCookie(&http.Cookie{Name: "wechat_manage", Value: cookie})
	r.Header.Set("Origin", "https://foreign.example")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	if rr.Code != 403 {
		t.Fatal("cross-origin write", rr.Code)
	}
	if len(in.queue.History()) != 0 {
		t.Fatal("app management launched AI")
	}
}
func TestKnowledgeNativeCommandsDoNotLaunchAI(t *testing.T) {
	defer metadb.CloseAll()
	s, _ := library.Open(t.TempDir())
	defer s.Close()
	server := httptest.NewServer(library.Handler(s, strings.Repeat("k", 32)))
	defer server.Close()
	in := inboundFixture(t)
	in.library, _ = library.NewClient(server.URL, strings.Repeat("k", 32))
	for i, input := range []string{"知识树", "研究地图", "研究方向", "校园应用"} {
		if e := in.handle(context.Background(), textMessage(string(rune('a'+i)), input)); e != nil {
			t.Fatal(e)
		}
	}
	if len(in.queue.History()) != 0 {
		t.Fatal("lookup launched AI")
	}
	if _, e := in.workspaceAction("owner", workspaceRequest{Action: "library", Library: &library.Request{Action: "map_publish", Collection: "A"}}, ""); e == nil {
		t.Fatal("browser could publish model output")
	}
}

func TestCampusAppTemplatePathRewrite(t *testing.T) {
	body := "fetch(`/api/projects/${id}/stage`)"
	got := string(adaptCampusAppBody([]byte(body), "/wechat-files/manage/apps/abc/"))
	if got != "fetch(`/wechat-files/manage/apps/abc/api/projects/${id}/stage`)" {
		t.Fatal(got)
	}
}
