package maintenance

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNativeUpdateRequestsAreIdempotentScopedAndDurable(t *testing.T) {
	s, _ := Open(t.TempDir())
	if e := s.RequestUpdates("phone-message", "cloud", "campus"); e != nil {
		t.Fatal(e)
	}
	s.RequestUpdates("phone-message", "cloud", "campus")
	cloud := s.PendingUpdates("cloud")
	campus := s.PendingUpdates("campus")
	if len(cloud) != 1 || len(campus) != 1 || cloud[0].ID == campus[0].ID {
		t.Fatal("request duplicated or host scope lost")
	}
	if s.CompleteUpdate("campus", cloud[0].ID) == nil || s.CompleteUpdate("cloud", "../../bad") == nil {
		t.Fatal("wrong host or traversal accepted")
	}
	reopen, _ := Open(s.Dir)
	if len(reopen.PendingUpdates("cloud")) != 1 {
		t.Fatal("request lost on restart")
	}
	h := Handler(reopen, "key")
	r := httptest.NewRequest("GET", "/maintenance/update-requests?host=cloud", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("native update controls exposed")
	}
	r = httptest.NewRequest("POST", "/maintenance/update-requests/complete", strings.NewReader(`{"host":"cloud","id":"`+cloud[0].ID+`"}`))
	r.Header.Set("Authorization", "Bearer key")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || len(reopen.PendingUpdates("cloud")) != 0 || len(reopen.PendingUpdates("campus")) != 1 {
		t.Fatal("completion lost or acknowledged another host")
	}
}
