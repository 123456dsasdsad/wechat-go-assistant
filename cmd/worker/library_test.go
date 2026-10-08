package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"crypto/sha256"
	"fmt"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/library"
)

func TestReviewFailureDoesNotSkipOtherCategories(t *testing.T) {
	s, e := library.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	intake, e := s.Begin("owner", "x", "", "研究")
	if e != nil {
		t.Fatal(e)
	}
	ms, e := s.SaveResearch("owner", intake.ID, library.Research{Materials: []library.Material{{Title: "真实资料", Topics: []string{"A", "B", "C"}}}})
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	for _, topic := range []string{"A", "B", "C"} {
		sn, _ := s.Snapshot("owner", topic)
		h := sha256.Sum256([]byte(topic))
		p := filepath.Join(dir, fmt.Sprintf("review-%x-%d-%d.json", h[:8], sn.Topic.Revision, sn.Topic.Version))
		ids := []int64{ms[0].ID}
		if topic == "A" {
			ids = []int64{999}
		}
		review := library.Review{Topic: topic, Sections: []library.Section{{Name: "方法体系", Text: "已读证据", MaterialIDs: ids}}}
		text, _ := json.Marshal(review)
		cache, _ := json.Marshal(libraryStage{Text: string(text)})
		os.WriteFile(p, cache, 0600)
	}
	r := libraryRunner{ctx: context.Background(), cfg: config{library: s}, task: jobs.Task{Owner: "owner", Kind: "review_update", LibraryID: "*"}, dir: dir}
	updated, e := r.reviews()
	if e == nil || len(updated) != 2 {
		t.Fatalf("failed category blocked independent reviews: %v %v", e, updated)
	}
	for _, topic := range []string{"B", "C"} {
		v, e := s.Review("owner", topic, 0)
		if e != nil || v.Version != 1 {
			t.Fatal(topic, e)
		}
	}
	ts, _ := s.Topics("owner")
	if !ts[0].Dirty || ts[0].LastError == "" {
		t.Fatal("failed category lost durable retry marker")
	}
}

func TestEvidenceChecksAndHTMLExtraction(t *testing.T) {
	if containsEvidence("results unknown", "99 percent accuracy") {
		t.Fatal("invented evidence matched")
	}
	if !containsEvidence("Graph   Attention Networks\nuse attention", "Graph Attention Networks") {
		t.Fatal("PDF whitespace rejected")
	}
	if got := plainHTML("<style>hidden</style><p>A &amp; B</p><script>secret</script>"); got != " A & B " {
		t.Fatal(got)
	}
}
