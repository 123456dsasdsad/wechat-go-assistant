package main

import (
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
