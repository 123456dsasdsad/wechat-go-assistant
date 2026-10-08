package library

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestDraftResumeBackupAndLockedSections(t *testing.T) {
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	in, _ := s.Begin("a", "batch", "old", "研究")
	s.Append("a", in.ID, "one", "原始材料", nil)
	in, _ = s.Intake("a", in.ID)
	in.Updated = time.Now().Add(-time.Hour)
	s.saveIntake(in, "")
	if _, e = s.Active("a", "old"); e == nil {
		t.Fatal("expired draft was still active")
	}
	if _, e = s.Resume("a", in.ID, "new"); e != nil {
		t.Fatal(e)
	}
	resumed, e := s.Active("a", "new")
	if e != nil || resumed.Text != "原始材料\n" {
		t.Fatal(resumed, e)
	}
	m := add(t, s, "a", "paper", []string{"A", "B"})
	if e = s.Import("a", "A", "手写段落：不得丢失"); e != nil {
		t.Fatal(e)
	}
	if e = s.Notes("a", "A", "我的笔记"); e != nil {
		t.Fatal(e)
	}
	sn, _ := s.Snapshot("a", "A")
	r, _ := s.MergeLocked("a", "A", Review{Topic: "A", Sections: []Section{{Name: "方法体系", Text: "引用", MaterialIDs: []int64{m.ID}}}})
	r, e = s.Publish("a", sn, r)
	if e != nil || r.Notes != "我的笔记" || len(r.References) != 1 || r.References[0].Revision != m.Revision {
		t.Fatal(r, e)
	}
	sn, _ = s.Snapshot("a", "A")
	r.Sections[1].Text = "bad"
	if _, e = s.Publish("a", sn, r); e == nil {
		t.Fatal("locked text replaced")
	}
	dir := t.TempDir()
	if e = s.Backup(filepath.Join(dir, "library.sqlite")); e != nil {
		t.Fatal(e)
	}
	copy, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer copy.Close()
	if _, e = copy.Get("a", m.ID); e != nil {
		t.Fatal("backup missed WAL commits", e)
	}
	restored, e := s.RestoreReview("a", "A", 1)
	if e != nil || restored.Version != 2 || restored.Notes != "我的笔记" {
		t.Fatal("history restoration failed", restored, e)
	}
	if _, e = s.Review("a", "A", 1); e != nil {
		t.Fatal("restore removed history", e)
	}
}

func TestEvidenceBlobOwnershipSurvivesIntakeCleanup(t *testing.T) {
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	body := []byte("downloaded original evidence")
	hash := sha256.Sum256(body)
	a := Asset{SHA256: fmt.Sprintf("%x", hash), Name: "source.txt", Size: int64(len(body))}
	if e = s.PutBlob(a, bytes.NewReader(body)); e != nil {
		t.Fatal(e)
	}
	in, _ := s.Begin("a", "x", "", "研究")
	_, e = s.SaveResearch("a", in.ID, Research{Materials: []Material{{Title: "原文", Assets: []Asset{a}}}})
	if e != nil {
		t.Fatal(e)
	}
	f, e := s.Blob("a", a.SHA256)
	if e != nil {
		t.Fatal(e)
	}
	f.Close()
	if f, e := s.Blob("b", a.SHA256); e == nil {
		f.Close()
		t.Fatal("foreign evidence downloadable")
	}
}
