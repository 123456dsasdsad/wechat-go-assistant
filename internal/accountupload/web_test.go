package accountupload

import (
	"bytes"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func exportJSON(a Account) []byte {
	b, _ := json.Marshal(map[string]any{"type": "codex", "email": a.Email, "account_id": a.AccountID, "access_token": a.AccessToken, "id_token": a.IDToken, "refresh_token": a.RefreshToken})
	return b
}
func TestReceiptIdentifiesNonAINotification(t *testing.T) {
	for _, status := range []string{"pending", "applied", "active", "failed"} {
		text := (Receipt{ID: "0123456789abcdef", Status: status, Count: 2}).Text()
		if !strings.HasPrefix(text, "账号导入回执（程序处理，未调用 AI）\n") || !strings.Contains(text, "01234567") {
			t.Fatalf("ambiguous account receipt: %s", text)
		}
	}
}
func TestPersistedBatchRetryReloadDoesNotReplayOldCredentials(t *testing.T) {
	dir := t.TempDir()
	store, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	a := fixtureAccount("person@example.com", "workspace-1", "private-refresh")
	first, e := store.Stage("owner", "same-source", exportJSON(a))
	if e != nil {
		t.Fatal(e)
	}
	again, e := store.Stage("owner", "same-source", exportJSON(a))
	if e != nil || again.ID != first.ID {
		t.Fatal("upload retry duplicated batch")
	}
	root := fixturePool(t)
	if e = store.Process(first.ID, root, func() error { return errors.New("reload-failed") }); e == nil {
		t.Fatal("reload failure claimed success")
	}
	if r, _ := store.Status("owner", first.ID); r.Status != "applied" || r.Added != 1 {
		t.Fatal("lost pending activation", r)
	}
	reopened, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	if e = reopened.Process(first.ID, root, func() error { return nil }); e != nil {
		t.Fatal(e)
	}
	receipt, _ := reopened.Status("owner", first.ID)
	if receipt.Status != "active" || receipt.Added != 1 || receipt.Updated != 0 {
		t.Fatal("reload replayed credentials", receipt)
	}
	if _, e = reopened.Status("another-owner", first.ID); e == nil {
		t.Fatal("cross-owner receipt readable")
	}
	saved, e := reopened.readBatch(first.ID)
	if e != nil || len(saved.Accounts) != 0 {
		t.Fatal("completed staging retained credential copies")
	}
}
func multipartBody(data []byte) (*bytes.Buffer, string) {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	p, _ := w.CreateFormFile("file", "accounts.json")
	p.Write(data)
	w.Close()
	return &b, w.FormDataContentType()
}
func TestDedicatedUploadRequiresGrantAndOriginAndReturnsNoCredentials(t *testing.T) {
	store, _ := Open(t.TempDir())
	token, e := store.Grant("owner", "message-1")
	if e != nil {
		t.Fatal(e)
	}
	handler, e := PublicHandler(store, "https://example.com/wechat-files/")
	if e != nil {
		t.Fatal(e)
	}
	session := func(token, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/wechat-files/accounts/session", strings.NewReader(`{"token":"`+token+`"}`))
		r.Header.Set("Origin", origin)
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, r)
		return out
	}
	if out := session(strings.Repeat("0", 64), "https://example.com"); out.Code != 401 {
		t.Fatal("anonymous session accepted")
	}
	if out := session(token, "https://other.example"); out.Code != 403 {
		t.Fatal("cross-origin session accepted")
	}
	out := session(token, "https://example.com")
	if out.Code != 200 {
		t.Fatal(out.Code)
	}
	cookie := out.Result().Cookies()[0]
	if !cookie.Secure || !cookie.HttpOnly || cookie.Path != "/wechat-files/accounts/" {
		t.Fatal("weak account session cookie")
	}
	a := fixtureAccount("person@example.com", "workspace-1", "private-refresh")
	for _, authenticated := range []bool{false, true} {
		b, contentType := multipartBody(exportJSON(a))
		r := httptest.NewRequest("POST", "/wechat-files/accounts/upload", b)
		r.Header.Set("Content-Type", contentType)
		r.Header.Set("Origin", "https://example.com")
		if authenticated {
			r.AddCookie(cookie)
		}
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, r)
		if !authenticated && result.Code != 401 {
			t.Fatal("anonymous import accepted")
		}
		if authenticated {
			if result.Code != 200 {
				t.Fatal(result.Code, result.Body.String())
			}
			for _, secret := range []string{a.AccessToken, a.IDToken, a.RefreshToken, a.Email} {
				if strings.Contains(result.Body.String(), secret) {
					t.Fatal("secret exposed in import receipt")
				}
			}
		}
	}
	r := httptest.NewRequest("GET", "/wechat-files/accounts/credentials.json", nil)
	r.AddCookie(cookie)
	out = httptest.NewRecorder()
	handler.ServeHTTP(out, r)
	if out.Code != http.StatusNotFound {
		t.Fatal("credential download route exists")
	}
}
