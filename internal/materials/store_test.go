package materials

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"testing"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
)

func TestCollectionSurvivesRestartAndDeduplicates(t *testing.T) {
	root := filepath.Join(t.TempDir(), "materials")
	s, e := Open(root)
	if e != nil {
		t.Fatal(e)
	}
	p, e := s.Begin("owner", "start", "论文修改", "aaaaaaaa")
	if e != nil {
		t.Fatal(e)
	}
	p, e = s.Append("owner", p.ID, Entry{ID: "m1", Kind: "text", Text: "导师要求增加 CPU 基线", Speaker: "导师"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Append("owner", p.ID, p.Entries[0]); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Append("owner", p.ID, Entry{ID: "m1", Text: "不同内容"}); e == nil {
		t.Fatal("conflicting duplicate accepted")
	}
	s.Close()
	s, e = Open(root)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	got, ok, e := s.Active("owner")
	if e != nil || !ok || len(got.Entries) != 1 || got.Title != "论文修改" {
		t.Fatal(got, ok, e)
	}
	if _, e = s.Get("other", p.ID); e == nil {
		t.Fatal("another owner read material")
	}
	if _, e = s.Begin("owner", "second", "其他材料", "bbbbbbbb"); e == nil {
		t.Fatal("overlapping collectors accepted")
	}
	if _, e = s.Update("owner", p.ID, "saved", "", ""); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Begin("owner", "second", "其他材料", "bbbbbbbb"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Update("owner", p.ID, "collecting", "", ""); e == nil {
		t.Fatal("resuming bypassed active collector")
	}
}
func TestMaterialBlobIntegrityAndOwnerScope(t *testing.T) {
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	data := []byte("source document")
	h := sha256.Sum256(data)
	ref := files.Ref{ID: "aaaaaaaaaaaaaaaaaaaaaaaa", Name: "source.txt", Size: int64(len(data)), SHA256: hex.EncodeToString(h[:])}
	if e = s.PutBlob(ref, bytes.NewReader([]byte("damaged"))); e == nil {
		t.Fatal("damaged source accepted")
	}
	if e = s.PutBlob(ref, bytes.NewReader(data)); e != nil {
		t.Fatal(e)
	}
	p, _ := s.Begin("owner", "a", "资料", "aaaaaaaa")
	p, e = s.Append("owner", p.ID, Entry{ID: "file", Files: []files.Ref{ref}})
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.Blob("other", p.ID, ref.SHA256); e == nil {
		t.Fatal("cross-owner file read")
	}
	f, got, e := s.Blob("owner", p.ID, ref.SHA256)
	if e != nil {
		t.Fatal(e)
	}
	f.Close()
	if got.Name != ref.Name {
		t.Fatal(got)
	}
	pr, e := s.SaveProject(Project{Owner: "owner", Title: "论文", Conversations: []string{"aaaaaaaa"}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Bind("other", p.ID, pr.ID); e == nil {
		t.Fatal("cross-owner project assignment")
	}
	if _, e = s.Bind("owner", p.ID, pr.ID); e != nil {
		t.Fatal(e)
	}
}
