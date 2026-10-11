package main

import (
	"strings"
	"testing"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/library"
)

func TestMapCatalogResolvesObservedQuotesAndRejectsUnknownKeys(t *testing.T) {
	m := library.Material{ID: 1, Title: "Observed paper", Summary: "Thin structures require connected predictions.", Sources: []library.Source{{Verified: true, ReadingScope: "partial_text"}}}
	snap := library.MapSnapshot{Snapshot: library.Snapshot{Topic: library.Topic{Name: "A"}, Materials: []library.Material{m}}}
	b := library.KnowledgeBranch{Topic: "A", Domain: "Vision", Group: "Segmentation", Nodes: []library.MapNode{
		{Key: "root", Kind: "topic", Title: "A", Basis: "index"},
		{Key: "problem", Parent: "root", Kind: "problem", Title: "Connectivity", Basis: "system_synthesis", MaterialIDs: []int64{1}, Evidence: []library.Evidence{{ExcerptKey: "m1-s0-e0", Excerpt: "A retyped quote must be replaced"}}},
	}}
	if e := resolveMapEvidence(snap, &b); e != nil {
		t.Fatal(e)
	}
	ref := b.Nodes[1].Evidence[0]
	if ref.Excerpt != m.Summary || ref.MaterialID != 1 || ref.Source != 0 || ref.ExcerptKey != "" {
		t.Fatal("quote was not resolved from observed catalog", ref)
	}
	b.Nodes[1].Evidence[0].ExcerptKey = "invented"
	if e := resolveMapEvidence(snap, &b); e == nil || e.Error() != "map_unknown_excerpt_key" {
		t.Fatal("unknown catalog reference accepted", e)
	}
}

func TestMapCatalogDoesNotAttributeMultiSourceSummaryOrUnreadSources(t *testing.T) {
	snap := library.MapSnapshot{Snapshot: library.Snapshot{Materials: []library.Material{{
		ID: 2, Summary: "Merged summary cannot be attributed to either source.",
		Sources: []library.Source{{Verified: true, ReadingScope: "metadata_only"}, {Verified: true, ReadingScope: "partial_text"}},
		Claims:  []library.Claim{{Source: 0, Excerpt: "This source is not read."}, {Source: 1, Excerpt: strings.Repeat("Observed continuous source text. ", 100)}},
	}}}}
	catalog := mapEvidenceCatalog(snap)
	if len(catalog) != 4 {
		t.Fatal("catalog is not bounded", len(catalog))
	}
	for _, entry := range catalog {
		if entry.Source != 1 || strings.Contains(entry.Excerpt, "Merged summary") || !strings.Contains(strings.Join(strings.Fields(snap.Snapshot.Materials[0].Claims[1].Excerpt), " "), entry.Excerpt) {
			t.Fatal("unobserved or misattributed quote", entry)
		}
	}
}
