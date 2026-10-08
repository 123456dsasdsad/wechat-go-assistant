package library

import "testing"

func TestResultsDistinguishModelAndDatasetNamesFromMeasurements(t *testing.T) {
	m := Material{Sources: []Source{{Verified: true, ReadingScope: "partial_text"}}, Claims: []Claim{{Field: "result", Text: "QM9 中 GATv2 相对改进 11.5%，参见 Table 1a。", Excerpt: "GATv2 achieves a lower average error than GAT, by 11.5% relatively.", Source: 0, Locator: "p8", Attribution: "author_report"}}}
	if e := ValidateMaterial(m); e != nil {
		t.Fatal("model/dataset name mistaken for a result", e)
	}
	m.Claims[0].Text = "准确率90%"
	m.Claims[0].Excerpt = "accuracy 0.90"
	if ValidateMaterial(m) == nil {
		t.Fatal("substring matched a different scale without conversion")
	}
	m.Claims[0].Text = "提高11.50%"
	m.Claims[0].Excerpt = "improvement 11.5%"
	if e := ValidateMaterial(m); e != nil {
		t.Fatal("decimal formatting changed numeric value", e)
	}
}
