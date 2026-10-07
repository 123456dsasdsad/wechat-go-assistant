package files

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestResultDownloadScopeAndExpiry(t *testing.T) {
	s, _ := OpenWithLimit(t.TempDir(), 1024)
	ref, _ := s.Save("owner", "result", "figure.tiff", strings.NewReader("actual bytes"))
	link, e := s.DownloadLink("https://example.com/wechat-files/", "owner", ref)
	if e != nil {
		t.Fatal(e)
	}
	h := s.DownloadHandler("https://example.com/wechat-files/")
	check := func(link string, want int) {
		t.Helper()
		r := httptest.NewRequest("GET", link, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatal(w.Code, want)
		}
		if want == 200 && w.Body.String() != "actual bytes" {
			t.Fatal("wrong download")
		}
	}
	check(link, 200)
	check("https://example.com/wechat-files/result/"+ref.ID, 401)
	check(strings.Replace(link, ref.ID, strings.Repeat("a", 24), 1), 401)
	if _, e = s.DownloadLink("https://example.com/wechat-files/", "other", ref); e == nil {
		t.Fatal("wrong owner accepted")
	}
	before := s.now()
	s.now = func() time.Time { return before.Add(25 * time.Hour) }
	check(link, 401)
}
