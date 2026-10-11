package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/accesskeys"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
)

func accessFixture(t *testing.T) (*inbound, http.Handler) {
	t.Helper()
	s, e := accesskeys.Open(t.TempDir(), strings.Repeat("s", 64))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	f, e := files.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	in := &inbound{access: s, files: f, owner: "owner", publicURL: "https://example.com/wechat-files/"}
	return in, in.accessGate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "protected") }))
}
func TestEveryPublicPageRequiresAccessKey(t *testing.T) {
	_, h := accessFixture(t)
	for _, p := range []string{"manage/", "task/abc", "result/abc", "upload/abc", "accounts/abc", "watch/abc", "manage/apps/abc/", "manage/knowledge.js"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "https://example.com/wechat-files/"+p, nil))
		if w.Code != 401 || !strings.Contains(w.Body.String(), "访问 key") {
			t.Fatalf("ungated %s %d", p, w.Code)
		}
		if !strings.Contains(w.Header().Get("Content-Security-Policy"), "form-action 'self'") {
			t.Fatal("login policy missing")
		}
		if w.Header().Get("Referrer-Policy") != "same-origin" || !strings.Contains(w.Body.String(), "fetch(f.action") || !strings.Contains(w.Header().Get("Content-Security-Policy"), "script-src 'nonce-") {
			t.Fatal("native login source policy missing")
		}
	}
}
func TestKeyLoginScopeReadonlyAndRevoke(t *testing.T) {
	in, h := accessFixture(t)
	g, key, e := in.access.Temporary("owner", "share", "/wechat-files/", 60, true)
	if e != nil {
		t.Fatal(e)
	}
	login := func(origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "https://example.com/wechat-files/access/login", strings.NewReader(url.Values{"key": {key}, "return": {"/wechat-files/task/abc?token=old"}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := login("https://evil.example"); w.Code != 403 {
		t.Fatal("cross-origin login accepted")
	}
	w := login("https://example.com")
	if w.Code != 303 || w.Header().Get("Location") != "/wechat-files/task/abc?token=old" {
		t.Fatal("login failed", w.Code)
	}
	var cookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "wechat_access" {
			cookie = c
		}
	}
	if cookie == nil || !cookie.Secure || !cookie.HttpOnly {
		t.Fatal("cookie not protected")
	}
	call := func(method, path, body string) int {
		r := httptest.NewRequest(method, "https://example.com"+path, strings.NewReader(body))
		r.Header.Set("Origin", "https://example.com")
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if call("GET", "/wechat-files/task/abc", "") != 200 {
		t.Fatal("read failed")
	}
	if call("POST", "/wechat-files/manage/api", `{"action":"library","library":{"action":"map"}}`) != 200 {
		t.Fatal("readonly library read rejected")
	}
	for _, body := range []string{`{"action":"access_issue"}`, `{"action":"library","library":{"action":"map_annotation"}}`, `{"action":"retry"}`} {
		if call("POST", "/wechat-files/manage/api", body) != 403 {
			t.Fatal("readonly mutation accepted", body)
		}
	}
	if call("POST", "/wechat-files/manage/chunk", "") != 403 {
		t.Fatal("readonly upload accepted")
	}
	writer, _, e := in.access.Temporary("owner", "writer", "/wechat-files/", 60, false)
	if e != nil {
		t.Fatal(e)
	}
	raw, _, e := in.access.Session(writer)
	if e != nil {
		t.Fatal(e)
	}
	oldCookie := cookie
	cookie = &http.Cookie{Name: "wechat_access", Value: raw}
	if call("POST", "/wechat-files/manage/api", `{"action":"access_show","id":"primary"}`) != 403 {
		t.Fatal("temporary writer could read permanent credentials")
	}
	cookie = oldCookie
	if e = in.access.Revoke("owner", g.ID); e != nil {
		t.Fatal(e)
	}
	if call("GET", "/wechat-files/task/abc", "") != 401 {
		t.Fatal("revoked session accepted")
	}
}
