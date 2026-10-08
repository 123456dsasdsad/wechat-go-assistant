package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/library"
	"testing"
)

func TestPreserveSectionsWithoutChangedEvidence(t *testing.T) {
	previous := library.Section{Name: "稳定方法", Text: "人工核对后的旧论述", MaterialIDs: []int64{1}}
	sn := library.Snapshot{Previous: library.Review{References: []library.Material{{ID: 1, Revision: 1}}, Sections: []library.Section{previous}}, Materials: []library.Material{{ID: 1, Revision: 1}, {ID: 2, Revision: 1}}}
	r := preserveUnchanged(sn, library.Review{Sections: []library.Section{{Name: "稳定方法", Text: "任意改写", MaterialIDs: []int64{1}}}})
	if r.Sections[0].Text != previous.Text {
		t.Fatal("unaffected section rewritten")
	}
	r = preserveUnchanged(sn, library.Review{Sections: []library.Section{{Name: "稳定方法", Text: "加入新证据的论述", MaterialIDs: []int64{1, 2}}}})
	if r.Sections[0].Text == previous.Text {
		t.Fatal("new evidence was excluded")
	}
}

func TestInlineCitationReconciliationDoesNotInventIDs(t *testing.T) {
	snap := library.Snapshot{Materials: []library.Material{{ID: 1}, {ID: 2}}}
	r := reconcileReviewCitations(snap, library.Review{Sections: []library.Section{{Name: "方法", Text: "实际引用[资料 2]和[资料 999]，重复[资料 2]", MaterialIDs: []int64{1}}}})
	ids := r.Sections[0].MaterialIDs
	if len(ids) != 2 || ids[0] != 1 || ids[1] != 2 {
		t.Fatal("invented or duplicated citation", ids)
	}
	if r.Sections[0].Text != "实际引用[资料 2]和[资料 999]，重复[资料 2]" {
		t.Fatal("removed invalid inline citation instead of allowing validation to reject it")
	}
}

func TestReviewRepairKeepsStrictCitationsNotesAndLockedText(t *testing.T) {
	s, e := library.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	intake, _ := s.Begin("owner", "input", "", "研究")
	ms, e := s.SaveResearch("owner", intake.ID, library.Research{Materials: []library.Material{{Title: "方法证据", Topics: []string{"类别"}}}})
	if e != nil {
		t.Fatal(e)
	}
	s.Notes("owner", "类别", "我的笔记必须保留")
	s.Lock("owner", "类别", "我的约束", "锁定原文")
	snap, _ := s.Snapshot("owner", "类别")
	bad := library.Review{Topic: "类别", Sections: []library.Section{{Name: "待读文献", Text: "没有新增待读文献"}}}
	if _, e = s.Publish("owner", snap, bad); e == nil {
		t.Fatal("missing citation accepted without correction")
	}
	dir := t.TempDir()
	fixed := library.Review{Topic: "类别", Sections: []library.Section{{Name: "待读文献", Text: ""}, {Name: "方法体系", Text: "方法论述[资料 1]", MaterialIDs: []int64{ms[0].ID}}, {Name: "我的约束", Text: "模型修改了锁定内容"}}}
	text, _ := json.Marshal(fixed)
	cache, _ := json.Marshal(libraryStage{Text: string(text)})
	if e = os.WriteFile(filepath.Join(dir, "review-repair.json"), cache, 0600); e != nil {
		t.Fatal(e)
	}
	r := libraryRunner{ctx: context.Background(), cfg: config{library: s}, task: jobs.Task{Owner: "owner"}, dir: dir}
	published, e := r.publishReview("review", []byte(`{"materials":[{"id":1}]}`), snap, bad)
	if e != nil || published.Version != 1 || published.Notes != "我的笔记必须保留" {
		t.Fatal(published, e)
	}
	locked := false
	for _, sec := range published.Sections {
		if sec.Name == "我的约束" {
			locked = sec.Text == "锁定原文"
		}
	}
	if !locked {
		t.Fatal("locked text lost during repair")
	}
}
