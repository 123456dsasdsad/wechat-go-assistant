package jobs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOutputUploadLeaseIntegrityAndCompletionReplay(t *testing.T) {
	s, _ := Open(t.TempDir())
	s.Enqueue("source", "task", "owner", "reply")
	task, _ := s.Claim(time.Now())
	outputs, _ := files.OpenWithLimit(t.TempDir(), 1024)
	h := HandlerWithOutputs(s, "test-key", nil, outputs)
	body := "actual result bytes"
	sum := sha256.Sum256([]byte(body))
	digest := hex.EncodeToString(sum[:])
	send := func(lease, digest, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/jobs/"+task.ID+"/outputs/0?name=figure.png", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer test-key")
		r.Header.Set("X-Job-Lease", lease)
		r.Header.Set("X-File-SHA256", digest)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := send("wrong", digest, body); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := send(task.Lease, strings.Repeat("0", 64), body); w.Code != 400 || len(outputs.List("owner")) != 0 {
		t.Fatal("corrupt output committed", w.Code)
	}
	w := send(task.Lease, digest, body)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var ref files.Ref
	json.Unmarshal(w.Body.Bytes(), &ref)
	if w = send(task.Lease, digest, body); w.Code != 200 || !strings.Contains(w.Body.String(), ref.ID) {
		t.Fatal("upload retry duplicated result")
	}
	c := Completion{ID: task.ID, Lease: task.Lease, Result: "completed", Outputs: []files.Ref{ref}}
	if e := s.Complete(c, time.Now()); e != nil {
		t.Fatal(e)
	}
	if e := s.Complete(c, time.Now()); e != nil {
		t.Fatal("matching retry rejected", e)
	}
	c.Outputs = nil
	if e := s.Complete(c, time.Now()); e == nil {
		t.Fatal("conflicting result accepted")
	}
	if w = send(task.Lease, digest, body); w.Code != 403 {
		t.Fatal("completed task reopened")
	}
	if e := s.CommitPart(task.ID, "text"); e != nil {
		t.Fatal(e)
	}
	s.CommitPart(task.ID, ref.ID)
	reopened, e := Open(s.dir)
	if e != nil {
		t.Fatal(e)
	}
	j := reopened.Ready()[0]
	if !j.PartDelivered("text") || !j.PartDelivered(ref.ID) {
		t.Fatal("receipts lost on restart")
	}
}
