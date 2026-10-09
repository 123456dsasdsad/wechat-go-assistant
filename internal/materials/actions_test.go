package materials

import (
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"path/filepath"
	"testing"
)

func TestActionsRequireOriginalEvidenceAndRetainCompletion(t *testing.T) {
	defer metadb.CloseAll()
	root := filepath.Join(t.TempDir(), "db")
	s, e := Open(root)
	if e != nil {
		t.Fatal(e)
	}
	p, _ := s.Begin("o", "m", "paper", "cid")
	s.Append("o", p.ID, Entry{ID: "s1", Text: "小王周五前改摘要"})
	good := []byte(`[{"text":"修改摘要","assignee":"小王","source_entry":1,"excerpt":"周五前改摘要"}]`)
	if e = s.ImportActions("o", p.ID, []byte(`[{"text":"删数据","source_entry":1,"excerpt":"删除"}]`)); e == nil {
		t.Fatal("fabricated evidence accepted")
	}
	if e = s.ImportActions("o", p.ID, good); e != nil {
		t.Fatal(e)
	}
	rows, _ := s.Actions("o", p.ID)
	s.ActionStatus("o", p.ID, rows[0].ID, "done")
	s.Close()
	s, _ = Open(root)
	defer s.Close()
	s.ImportActions("o", p.ID, good)
	rows, e = s.Actions("o", p.ID)
	if e != nil || len(rows) != 1 || rows[0].Status != "done" {
		t.Fatal(rows, e)
	}
	if _, e = s.Actions("other", p.ID); e == nil {
		t.Fatal("foreign actions visible")
	}
}
