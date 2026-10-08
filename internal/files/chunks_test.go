package files

import (
	"strings"
	"testing"
)

func TestChunkResumeAfterReopen(t *testing.T) {
	root := t.TempDir()
	s, _ := Open(root)
	key := "123456789012345678901234"
	u, e := s.BeginUpload("owner", key, "test.txt", 6)
	if e != nil {
		t.Fatal(e)
	}
	u, e = s.AppendUpload("owner", u.ID, 0, strings.NewReader("abc"))
	if e != nil {
		t.Fatal(e)
	}
	s, e = Open(root)
	if e != nil {
		t.Fatal(e)
	}
	u, e = s.BeginUpload("owner", key, "test.txt", 6)
	if e != nil || u.Offset != 3 {
		t.Fatal(u, e)
	}
	if _, e = s.AppendUpload("other", u.ID, 3, strings.NewReader("def")); e == nil {
		t.Fatal("owner check")
	}
	if _, e = s.AppendUpload("owner", u.ID, 0, strings.NewReader("abc")); e != nil {
		t.Fatal(e)
	}
	if _, e = s.AppendUpload("owner", u.ID, 0, strings.NewReader("xxx")); e == nil {
		t.Fatal("conflicting retry")
	}
	if _, e = s.FinishUpload("owner", u.ID); e == nil {
		t.Fatal("incomplete accepted")
	}
	s.AppendUpload("owner", u.ID, 3, strings.NewReader("def"))
	a, e := s.FinishUpload("owner", u.ID)
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.FinishUpload("owner", u.ID)
	if e != nil || a != b || a.Size != 6 {
		t.Fatal(a, b, e)
	}
}
