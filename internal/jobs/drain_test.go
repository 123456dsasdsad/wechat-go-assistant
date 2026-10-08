package jobs

import (
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestMaintenanceDrainsWithoutRemovingQueuedJobOrWeakeningAuth(t *testing.T) {
	defer metadb.CloseAll()
	root := t.TempDir()
	s, _ := Open(filepath.Join(root, "jobs"))
	s.Enqueue("request", "work", "owner", "context")
	h := Handler(s, "private-key")
	marker := filepath.Join(root, "maintenance-drain.flag")
	os.WriteFile(marker, []byte(strconv.FormatInt(time.Now().Add(5*time.Minute).Unix(), 10)), 0600)
	r := httptest.NewRequest("POST", "/jobs/claim", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("maintenance bypassed auth")
	}
	r.Header.Set("Authorization", "Bearer private-key")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 || s.History()[0].Status != "queued" {
		t.Fatal("drain modified queued task")
	}
	os.WriteFile(marker, []byte(strconv.FormatInt(time.Now().Add(-time.Minute).Unix(), 10)), 0600)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || s.History()[0].Status != "running" {
		t.Fatal("expired maintenance prevented task claim")
	}
}
