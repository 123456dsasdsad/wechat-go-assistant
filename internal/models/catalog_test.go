package models

import "testing"

func TestCatalogRejectsUnsafeOrAmbiguousEntries(t *testing.T) {
	for _, c := range []Catalog{{Models: []Model{{ID: "--flag", Efforts: []string{"high"}, DefaultEffort: "high"}}}, {Models: []Model{{ID: "safe", Efforts: []string{"high"}, DefaultEffort: "low"}}}, {Models: []Model{{ID: "safe", Efforts: []string{"high", "high"}, DefaultEffort: "high"}}}} {
		if c.Validate() == nil {
			t.Fatal("invalid catalog accepted")
		}
	}
	c := Catalog{Models: []Model{{ID: "gpt-6-sol", Efforts: []string{"high"}, DefaultEffort: "high"}}}
	if _, err := c.Resolve("gpt-6-sol", "ultra"); err == nil {
		t.Fatal("unsupported effort accepted")
	}
}
