package conversations

import (
	"path/filepath"
	"testing"
)

func TestArchiveSearchAndProfilePersistence(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sessions.json")
	s, e := Open(p)
	if e != nil {
		t.Fatal(e)
	}
	a := s.Current()
	s.Handle("new", "新建会话 数据分析")
	b := s.Current()
	if e = s.Mark(a.ID, true, true); e != nil {
		t.Fatal(e)
	}
	if len(s.List("", true)) != 1 || len(s.List("数据", false)) != 1 {
		t.Fatal("search/archive")
	}
	if e = s.UpdateProfile(b.ID, Profile{Project: "data/experiment", Notes: "中文", BudgetUSD: 3}); e != nil {
		t.Fatal(e)
	}
	s, e = Open(p)
	if e != nil {
		t.Fatal(e)
	}
	v, _ := s.Get(b.ID)
	if v.Profile.Project != "data/experiment" || v.Number != b.Number || s.Current().ID != b.ID {
		t.Fatal(v)
	}
	if e = s.Mark(a.ID, false, false); e != nil {
		t.Fatal(e)
	}
	v, _ = s.Get(a.ID)
	if v.Number != a.Number {
		t.Fatal("number changed")
	}
	for _, invalid := range []string{"/etc", "../secret", "a/../b", "C:/foo", "a\\b"} {
		if ValidProject(invalid) {
			t.Fatal(invalid)
		}
	}
}
