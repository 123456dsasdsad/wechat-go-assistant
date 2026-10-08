package assistant

import (
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"path/filepath"
	"testing"
)

func TestTemplatePersistsLiteralUserContent(t *testing.T) {
	defer metadb.CloseAll()
	path := filepath.Join(t.TempDir(), "templates.json")
	s, e := OpenTemplates(path)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Save(Template{Name: "review", Body: "Read {{内容}} then inspect {{内容}}"}); e != nil {
		t.Fatal(e)
	}
	s, e = OpenTemplates(path)
	if e != nil {
		t.Fatal(e)
	}
	got, e := s.Expand("review", "$HOME; {{内容}}")
	if e != nil || got != "Read $HOME; {{内容}} then inspect $HOME; {{内容}}" {
		t.Fatal(got, e)
	}
	if e = s.Delete("review"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Expand("review", "x"); e == nil {
		t.Fatal("deleted template still usable")
	}
}
