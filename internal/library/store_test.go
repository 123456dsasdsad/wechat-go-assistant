package library

import (
	"errors"
	"testing"
)

func add(t *testing.T, s *Store, owner, key string, ts []string) Material {
	t.Helper()
	in, e := s.Begin(owner, key, "chat", "研究")
	if e != nil {
		t.Fatal(e)
	}
	ms, e := s.SaveResearch(owner, in.ID, Research{Materials: []Material{{Title: "微信模型方法", Text: "微信模型接入方法", Topics: ts}}})
	if e != nil {
		t.Fatal(e)
	}
	return ms[0]
}
func publish(t *testing.T, s *Store, owner, topic string) {
	t.Helper()
	sn, e := s.Snapshot(owner, topic)
	if e != nil {
		t.Fatal(e)
	}
	ids := []int64{}
	for _, m := range sn.Materials {
		ids = append(ids, m.ID)
	}
	_, e = s.Publish(owner, sn, Review{Topic: topic, Sections: []Section{{Name: "范围与覆盖", Text: "覆盖当前资料", MaterialIDs: ids}}})
	if e != nil {
		t.Fatal(e)
	}
}
func TestEveryAssociatedReviewBecomesDirty(t *testing.T) {
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	m := add(t, s, "a", "1", []string{"图学习", "半监督学习", "模型优化"})
	for _, v := range m.Topics {
		publish(t, s, "a", v)
	}
	_, e = s.Change("a", m.ID, []string{"图学习", "半监督学习", "新类别"}, nil, "move")
	if e != nil {
		t.Fatal(e)
	}
	ts, e := s.Topics("a")
	if e != nil {
		t.Fatal(e)
	}
	if len(ts) != 4 {
		t.Fatalf("missed old/new category: %#v", ts)
	}
	for _, v := range ts {
		if !v.Dirty {
			t.Errorf("review %s not invalidated", v.Name)
		}
	}
	if _, e = s.Get("other", m.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal("owner leak", e)
	}
}
func TestStalePublishAndDedup(t *testing.T) {
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	m := add(t, s, "a", "1", []string{"A", "B"})
	sn, _ := s.Snapshot("a", "A")
	publish(t, s, "a", "A")
	_, e = s.Publish("a", sn, Review{Topic: "A", Sections: []Section{{Name: "范围与覆盖", Text: "old"}}})
	if !errors.Is(e, ErrStale) {
		t.Fatal(e)
	}
	m2 := add(t, s, "a", "2", []string{"A", "B"})
	if m.ID != m2.ID || m.Revision != m2.Revision {
		t.Fatal("duplicate changed corpus", m, m2)
	}
	ts, _ := s.Topics("a")
	for _, v := range ts {
		if v.Name == "A" && v.Dirty {
			t.Fatal("repeat requests rewrite")
		}
	}
}
func TestPersistedSearchAndMissingEvidence(t *testing.T) {
	root := t.TempDir()
	s, _ := Open(root)
	m := add(t, s, "a", "1", []string{"方法"})
	s.Close()
	s, e := Open(root)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	hits, e := s.Search("a", "模型")
	if e != nil || len(hits) != 1 || hits[0].ID != m.ID {
		t.Fatal(e, hits)
	}
	hits, e = s.Search("b", "模型")
	if e != nil || len(hits) != 0 {
		t.Fatal("owner search leaked", e)
	}
	if e = ValidateMaterial(Material{Sources: []Source{{Verified: true, ReadingScope: "abstract_only"}}, Claims: []Claim{{Field: "result", Text: "99%", Source: 0, Locator: "p2", Excerpt: "99%"}}}); e == nil {
		t.Fatal("abstract invented result accepted")
	}
}
