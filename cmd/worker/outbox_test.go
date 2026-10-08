package main

import (
	"context"
	"encoding/json"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func __readOutbox(cfg config, id string) ([]byte, error) { return metadb.ReadJSON(outboxPath(cfg, id)) }
func TestOutboxRecoversFailedUploadWithoutInference(t *testing.T) {
	root := t.TempDir()
	s, _ := jobs.Open(filepath.Join(root, "jobs"))
	defer s.Close()
	f, _ := files.Open(filepath.Join(root, "outputs"))
	var fail atomic.Bool
	fail.Store(true)
	handler := jobs.HandlerWithOutputs(s, "key", nil, f)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() && strings.Contains(r.URL.Path, "/outputs/") {
			http.Error(w, "temporary failure", 503)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	j, _ := s.Enqueue("one", "task", "owner", "reply")
	task, _ := s.Claim(time.Now())
	dir := filepath.Join(root, "turn")
	os.MkdirAll(filepath.Join(dir, "outputs"), 0700)
	os.WriteFile(filepath.Join(dir, "outputs/result.txt"), []byte("actual output"), 0600)
	os.WriteFile(filepath.Join(dir, "outputs/manifest.json"), []byte(`{"files":["outputs/result.txt"]}`), 0600)
	cfg := config{WorkRoot: root, RelayURL: server.URL}
	out, e := queueResult(cfg, *task, jobs.Completion{ID: j.ID, Lease: task.Lease, Result: "Actual answer"}, dir)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if e = recoverResult(ctx, cfg, "key", out); e == nil {
		t.Fatal("network failure ignored")
	}
	v, _ := s.Snapshot(j.ID)
	if v.Result != "Actual answer" || !v.OutputPending {
		t.Fatal(v)
	}
	b, e := __readOutbox(cfg, j.ID)
	if e != nil {
		t.Fatal(e)
	}
	json.Unmarshal(b, &out)
	fail.Store(false)
	if e = recoverResult(ctx, cfg, "key", out); e != nil {
		t.Fatal(e)
	}
	v, _ = s.Snapshot(j.ID)
	if v.OutputPending || len(v.Outputs) != 1 || v.Attempts != 1 {
		t.Fatal(v)
	}
	if e = recoverResult(ctx, cfg, "key", out); e != nil {
		t.Fatal("replay", e)
	}
}
