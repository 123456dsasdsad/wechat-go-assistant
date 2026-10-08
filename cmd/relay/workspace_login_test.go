package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
)

func TestWorkbenchReusesExistingDeviceEvenWhenGrantCapacityIsFull(t *testing.T) {
	defer metadb.CloseAll()
	in := inboundFixture(t)
	device, e := in.files.Grant("owner", "device-existing", 30*24*time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	link, _ := in.files.Grant("owner", "new-login-link")
	foreign, _ := in.files.Grant("other", "foreign-device", 30*24*time.Hour)
	for i := 3; i < files.MaxGrants; i++ {
		if _, e = in.files.Grant("owner", fmt.Sprintf("other-link-%d", i)); e != nil {
			t.Fatal(e)
		}
	}
	handler := in.workspaceHandler("")
	for _, cookie := range []string{device, device, foreign, ""} {
		r := httptest.NewRequest("POST", "https://example.com/wechat-files/manage/api", strings.NewReader(`{"action":"login","token":"`+link+`"}`))
		r.Header.Set("Origin", "https://example.com")
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: "wechat_manage", Value: cookie})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if cookie == device {
			if w.Code != 200 || len(w.Result().Cookies()) != 0 {
				t.Fatal("valid device was replaced or blocked", w.Code, w.Body.String())
			}
		} else if w.Code != 503 {
			t.Fatal("foreign/missing cookie reused", w.Code)
		}
	}
	if len(in.queue.History()) != 0 {
		t.Fatal("login created AI work")
	}
}
