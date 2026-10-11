package conversations

import "testing"

func TestBackgroundConversationPreservesSelectionAndRetries(t *testing.T) {
	s, e := Open(t.TempDir() + "/sessions.json")
	if e != nil {
		t.Fatal(e)
	}
	before := s.Current()
	a, e := s.CreateBackground("direction-1", "研究验证")
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.CreateBackground("direction-1", "重试")
	if e != nil || a.ID != b.ID || s.Current().ID != before.ID {
		t.Fatal(a, b, e, s.Current())
	}
}
