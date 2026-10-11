package library

import (
	"errors"
	"testing"
)

func mapFixture(t *testing.T, s *Store) Material {
	t.Helper()
	in, e := s.Begin("a", "map-paper", "chat", "methods")
	if e != nil {
		t.Fatal(e)
	}
	ms, e := s.SaveResearch("a", in.ID, Research{Materials: []Material{{Title: "Topology study", Text: "We preserve thin structure connectivity using a skeleton loss.", Summary: "We preserve thin structure connectivity using a skeleton loss.", Topics: []string{"A", "B"}, Sources: []Source{{URL: "https://example.org/paper", Verified: true, ReadingScope: "partial_text"}}}}})
	if e != nil {
		t.Fatal(e)
	}
	for _, topic := range []string{"A", "B"} {
		publish(t, s, "a", topic)
	}
	return ms[0]
}
func mapDraft(m Material, topic string) KnowledgeBranch {
	e := Evidence{MaterialID: m.ID, Source: 0, Locator: "observed abstract", Excerpt: "preserve thin structure connectivity"}
	return KnowledgeBranch{Topic: topic, Domain: "Vision", Group: "Segmentation", Nodes: []MapNode{
		{Key: "root", Kind: "topic", Title: topic, Basis: "index"},
		{Key: "method", Parent: "root", Kind: "method", Title: "Skeleton loss", Text: "Observed method description; full experiments still require reading.", Basis: "author_report", MaterialIDs: []int64{m.ID}, Evidence: []Evidence{e}},
		{Key: "gap", Parent: "method", Kind: "limitation", Title: "CPU feasibility remains to be checked", Text: "Library inference, not a reported failure", Basis: "system_synthesis", MaterialIDs: []int64{m.ID}, Evidence: []Evidence{e}},
		{Key: "idea", Parent: "gap", Kind: "direction", Title: "Test sparse skeleton loss", Basis: "hypothesis", MaterialIDs: []int64{m.ID}, Evidence: []Evidence{e}, Direction: &Direction{Question: "Can CPU cost decrease without harming connectivity?", NearestWork: m.Title, Change: "Sparse loss evaluation", Baseline: "Original skeleton loss", Dataset: "Small public dataset, license and split to verify", Metrics: []string{"connectivity", "latency"}, Steps: []string{"Verify original method", "Compare controlled baseline"}, Risks: []string{"Sparse sampling may miss breaks"}, Falsification: "Reject if connectivity drops at matched cost", Verification: "Candidate only; search nearest published work before novelty claims"}},
	}, Links: []MapLink{{Key: "transfer", From: "idea", TargetTopic: map[string]string{"A": "B", "B": "A"}[topic], Kind: "transfer", Reason: "Candidate cross-category experiment", MaterialIDs: []int64{m.ID}}}, Changes: []string{"Initial synthesis"}}
}
func TestMapPublicationHistoryAnnotationsAndMultiCategoryInvalidation(t *testing.T) {
	root := t.TempDir()
	s, e := Open(root)
	if e != nil {
		t.Fatal(e)
	}
	m := mapFixture(t, s)
	for _, topic := range []string{"A", "B"} {
		snap, _ := s.MapSnapshot("a", topic)
		b, e := s.PublishMap("a", snap, mapDraft(m, topic))
		if e != nil || b.Version != 1 || len(b.Coverage) != 1 {
			t.Fatal(b, e)
		}
	}
	if e = s.AnnotateMap("a", "A", "idea", MapAnnotation{Title: "My direction", Notes: "Keep this", Idea: "My separate idea", Pinned: true}); e != nil {
		t.Fatal(e)
	}
	snap, _ := s.MapSnapshot("a", "A")
	b, e := s.PublishMap("a", snap, mapDraft(m, "A"))
	if e != nil || b.Version != 2 {
		t.Fatal(e, b)
	}
	if _, e = s.MapBranch("a", "A", 1); e != nil {
		t.Fatal(e)
	}
	if _, e = s.MapBranch("other", "A", 0); !errors.Is(e, ErrNotFound) {
		t.Fatal("foreign owner read", e)
	}
	if e = s.AnnotateMap("other", "A", "idea", MapAnnotation{Notes: "leak"}); !errors.Is(e, ErrNotFound) {
		t.Fatal("foreign owner write", e)
	}
	s.Close()
	s, e = Open(root)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	view, e := s.Map("a")
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range view.Branches {
		if v.Topic.Count != 1 {
			t.Fatal("map lost paper count", v.Topic)
		}
		if v.Topic.Name == "A" {
			found := false
			for _, n := range v.Branch.Nodes {
				if n.Key == "idea" {
					found = true
					if n.Annotation == nil || n.Annotation.Title != "My direction" || !n.Annotation.Pinned {
						t.Fatal("annotation lost", n)
					}
				}
			}
			if !found || v.Dirty {
				t.Fatal(v)
			}
		}
	}
	if _, e = s.Change("a", m.ID, nil, nil, "trash"); e != nil {
		t.Fatal(e)
	}
	view, e = s.Map("a")
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range view.Branches {
		if !v.Dirty || len(v.Branch.Coverage) != 0 {
			t.Fatal("deleted evidence exposed", v)
		}
	}
	if _, e = s.DirectionPrompt("a", "A", "idea"); !errors.Is(e, ErrStale) {
		t.Fatal("stale task allowed", e)
	}
}
func TestMapRejectsInvalidEvidenceCyclesAndStaleWrites(t *testing.T) {
	s, _ := Open(t.TempDir())
	defer s.Close()
	m := mapFixture(t, s)
	snap, _ := s.MapSnapshot("a", "A")
	for _, tc := range []struct {
		name   string
		change func(*KnowledgeBranch)
	}{
		{"cycle", func(b *KnowledgeBranch) { b.Nodes[1].Parent = "gap" }},
		{"foreign", func(b *KnowledgeBranch) { b.Nodes[1].MaterialIDs = []int64{9999} }},
		{"fabricated", func(b *KnowledgeBranch) { b.Nodes[1].Evidence[0].Excerpt = "This is not in the original source" }},
		{"unresolved_catalog", func(b *KnowledgeBranch) { b.Nodes[1].Evidence[0].ExcerptKey = "unresolved" }},
		{"incomplete", func(b *KnowledgeBranch) { b.Nodes[3].Direction.Baseline = "" }},
		{"not_hypothesis", func(b *KnowledgeBranch) { b.Nodes[3].Basis = "author_report" }},
		{"orphan", func(b *KnowledgeBranch) { b.Nodes[1].Parent = "missing" }},
		{"foreign_link", func(b *KnowledgeBranch) { b.Links[0].TargetTopic = "unknown" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := mapDraft(m, "A")
			tc.change(&b)
			if _, e := s.PublishMap("a", snap, b); e == nil {
				t.Fatal("invalid publication accepted")
			}
			if _, e := s.MapBranch("a", "A", 0); !errors.Is(e, ErrNotFound) {
				t.Fatal("failed publish wrote state", e)
			}
		})
	}
	if _, e := s.PublishMap("a", snap, mapDraft(m, "A")); e != nil {
		t.Fatal(e)
	}
	if _, e := s.PublishMap("a", snap, mapDraft(m, "A")); !errors.Is(e, ErrStale) {
		t.Fatal("map version race", e)
	}
	snap, _ = s.MapSnapshot("a", "A")
	if e := s.Notes("a", "A", "Changed corpus notes"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.PublishMap("a", snap, mapDraft(m, "A")); !errors.Is(e, ErrStale) {
		t.Fatal("corpus race", e)
	}
	draft := mapDraft(m, "A")
	snap.Snapshot.Materials[0].Sources[0].ReadingScope = "metadata_only"
	if e := ValidateMap(snap, draft); e == nil {
		t.Fatal("unread source accepted")
	}
}
