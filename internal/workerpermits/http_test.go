package workerpermits

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPLeaseAndTokenCannotPauseOtherTasks(t *testing.T) {
	p := New(1)
	p.Key = strings.Repeat("a", 64)
	a, _ := p.Acquire(context.Background())
	a.Bind("job", "lease")
	defer a.Release()
	for _, test := range []struct {
		key, lease string
		status     int
	}{{"bad", "lease", 401}, {p.Key, "stale", 409}, {p.Key, "lease", 200}} {
		req := httptest.NewRequest("POST", "http://127.0.0.1/permit", strings.NewReader(`{"job":"job","lease":"`+test.lease+`","question":"q","action":"pause"}`))
		req.Header.Set("Authorization", "Bearer "+test.key)
		w := httptest.NewRecorder()
		p.ServeHTTP(w, req)
		if w.Code != test.status {
			t.Fatal(w.Code, test.status, w.Body.String())
		}
	}
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/permit", nil)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)
	if w.Code == 200 {
		t.Fatal("GET accepted")
	}
}
