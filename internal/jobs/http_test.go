package jobs

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPrivateAPIAndCredentialBoundary(t *testing.T) {
	s, _ := Open(t.TempDir())
	s.Enqueue("id", "input", "private-owner", "private-context")
	h := Handler(s, "test-key")
	r := httptest.NewRequest("POST", "/jobs/claim", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	r.Header.Set("Authorization", "Bearer test-key")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	if _, ok := body["owner"]; ok {
		t.Fatal("leaked owner")
	}
	if _, ok := body["reply_context"]; ok {
		t.Fatal("leaked context")
	}
	if len(body) != 5 {
		t.Fatal("unexpected worker fields")
	}
}

func TestHeartbeatAPIRequiresBearerAndCurrentLease(t *testing.T) {
	s, _ := Open(t.TempDir())
	s.Enqueue("id", "input", "owner", "reply")
	task, _ := s.Claim(time.Now())
	h := Handler(s, "test-key")
	for _, tc := range []struct {
		bearer, lease string
		code          int
	}{{"", task.Lease, 401}, {"Bearer test-key", "wrong", 409}, {"Bearer test-key", task.Lease, 200}} {
		body, _ := json.Marshal(map[string]string{"id": task.ID, "lease": tc.lease})
		r := httptest.NewRequest("POST", "/jobs/heartbeat", strings.NewReader(string(body)))
		r.Header.Set("Authorization", tc.bearer)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Fatal(w.Code, tc.code)
		}
	}
}
