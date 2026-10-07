package files

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestTaskGrantOwnerExpiryAndJobIsolation(t *testing.T) {
	s, _ := Open(t.TempDir())
	now := time.Now()
	s.now = func() time.Time { return now }
	id := strings.Repeat("a", 24)
	link, _ := s.TaskLink("https://example.com/wechat-files/", "owner", id)
	u, _ := url.Parse(link)
	q := u.Query()
	if !s.AuthorizeTask("owner", id, q.Get("e"), q.Get("token")) {
		t.Fatal("valid grant rejected")
	}
	if s.AuthorizeTask("other", id, q.Get("e"), q.Get("token")) || s.AuthorizeTask("owner", strings.Repeat("b", 24), q.Get("e"), q.Get("token")) {
		t.Fatal("grant escaped scope")
	}
	now = now.Add(25 * time.Hour)
	if s.AuthorizeTask("owner", id, q.Get("e"), q.Get("token")) {
		t.Fatal("expired grant accepted")
	}
}
