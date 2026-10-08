package settings

import (
	"path/filepath"
	"testing"
)

func TestConversationSettingsRemainIndependent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "prefs.json")
	s, e := Open(p, catalogFixture())
	if e != nil {
		t.Fatal(e)
	}
	s.Handle("a", "默认模型 gpt-6-luna", "12345678")
	s.Handle("b", "推理强度 low", "12345678")
	if s.Current("abcdef12").Model != "gpt-6-sol" || s.Current().Model != "gpt-6-sol" {
		t.Fatal("global choice changed")
	}
	s, e = Open(p, catalogFixture())
	if e != nil {
		t.Fatal(e)
	}
	a := s.Current("12345678")
	if a.Model != "gpt-6-luna" || a.Effort != "low" {
		t.Fatal(a)
	}
	v, body, e := s.ChoiceForTask("使用 gpt-6.1-sol：hello", "12345678")
	if e != nil || body != "hello" || v.Model != "gpt-6.1-sol" || v.Effort != "low" {
		t.Fatal(v, body, e)
	}
}
