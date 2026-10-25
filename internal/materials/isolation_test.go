package materials

import "testing"

func TestCollectionIsScopedToConversation(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, err := s.Begin("owner", "source-a", "A", "12345678")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Begin("owner", "source-b", "B", "87654321")
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Active("owner", "12345678")
	if err != nil || !ok || got.ID != a.ID {
		t.Fatal(got, ok, err)
	}
	got, ok, err = s.Active("owner", "87654321")
	if err != nil || !ok || got.ID != b.ID {
		t.Fatal(got, ok, err)
	}
	if _, ok, err = s.Active("owner", "00000000"); err != nil || ok {
		t.Fatal(ok, err)
	}
}
