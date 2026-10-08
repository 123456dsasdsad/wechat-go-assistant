package jobs

import (
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/models"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAttachmentLeaseAndConversationSnapshot(t *testing.T) {
	defer metadb.CloseAll()
	f, _ := files.Open(t.TempDir())
	ref, _ := f.Save("owner", "source", "a.txt", strings.NewReader("protected file"))
	q, _ := Open(t.TempDir())
	j, _ := q.EnqueueConversation("first", "read", "owner", "private-context", models.Choice{Model: "gpt-6-sol", Effort: "high"}, []files.Ref{ref}, "aaaaaaaa")
	again, _ := q.EnqueueConversation("first", "changed", "owner", "other", models.Choice{Model: "gpt-6-luna", Effort: "low"}, nil, "bbbbbbbb")
	if again.ConversationID != j.ConversationID || len(again.Attachments) != 1 {
		t.Fatal("replay changed task selection")
	}
	task, _ := q.Claim(time.Now())
	h := HandlerWithFiles(q, "key", f)
	request := func(lease string) int {
		r := httptest.NewRequest("GET", "/jobs/"+j.ID+"/files/"+ref.ID, nil)
		r.Header.Set("Authorization", "Bearer key")
		r.Header.Set("X-Job-Lease", lease)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if request("wrong") != 403 || request(task.Lease) != 200 {
		t.Fatal("attachment lease boundary failed")
	}
	q.Complete(Completion{ID: j.ID, Lease: task.Lease, Result: "done"}, time.Now())
	if request(task.Lease) != 403 {
		t.Fatal("completed task could download blob")
	}
}
