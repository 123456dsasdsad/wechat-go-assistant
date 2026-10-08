package jobs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
)

func TestLegacyMigrationIsAtomicAndNeverReimportsStaleJSON(t *testing.T) {
	defer metadb.CloseAll()
	dir := t.TempDir()
	j := Job{ID: strings.Repeat("a", 24), Input: "original", Model: "gpt-6.1-sol", Effort: "high", Owner: "owner", ReplyContext: "ctx", Status: "queued", Created: time.Now()}
	path := filepath.Join(dir, j.ID+".json")
	raw, _ := json.Marshal(j)
	os.WriteFile(path, raw, 0600)
	bad := filepath.Join(dir, strings.Repeat("b", 24)+".json")
	os.WriteFile(bad, []byte("invalid"), 0600)
	if _, e := Open(dir); e == nil {
		t.Fatal("partial migration accepted")
	}
	os.Remove(bad)
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	task, e := s.Claim(time.Now())
	if e != nil || task == nil {
		t.Fatal(e)
	}
	if e = s.Complete(Completion{ID: j.ID, Lease: task.Lease, Result: "finished"}, time.Now()); e != nil {
		t.Fatal(e)
	}
	s, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	saved, ok := s.Snapshot(j.ID)
	if !ok || saved.Status != "done" || saved.Result != "finished" {
		t.Fatal("stale JSON overwrote SQL", saved, e)
	}
	original, _ := os.ReadFile(path)
	if string(original) != string(raw) {
		t.Fatal("legacy backup changed")
	}
}

func TestRecentAndArchivePagesPreserveOrdering(t *testing.T) {
	defer metadb.CloseAll()
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 140; i++ {
		j, e := s.Enqueue(string(rune(1000+i)), "input", "owner", "ctx")
		if e != nil {
			t.Fatal(e)
		}
		j.Status = "delivered"
		j.Created = time.Unix(int64(i), 0)
		if e = s.save(j); e != nil {
			t.Fatal(e)
		}
	}
	if len(s.Active()) != 0 || len(s.Recent("owner", 5)) != 5 || len(s.Recent("other", 5)) != 0 {
		t.Fatal("SQL filters failed")
	}
	count := 0
	last := int64(-1)
	if e = s.Each(func(j Job) error {
		if j.Created.Unix() <= last {
			t.Fatal("page order")
		}
		last = j.Created.Unix()
		count++
		return nil
	}); e != nil || count != 140 {
		t.Fatal(e, count)
	}
}
