package files

import (
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"strings"
	"testing"
)

func TestLatestFileAndUploadGrantRemainInOriginalConversation(t *testing.T) {
	defer metadb.CloseAll()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.SaveScoped("owner", "a", "a.txt", strings.NewReader("A"), "12345678")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.SaveScoped("owner", "b", "b.txt", strings.NewReader("B"), "87654321")
	if err != nil {
		t.Fatal(err)
	}
	rows := s.ListConversation("owner", "12345678")
	if len(rows) != 1 || rows[0] != a {
		t.Fatal(rows)
	}
	token, err := s.GrantConversation("owner", "link", "12345678")
	if err != nil {
		t.Fatal(err)
	}
	if s.GrantScope(token) != "12345678" {
		t.Fatal("lost upload scope")
	}
	if _, err = s.GrantConversation("owner", "link", "87654321"); err == nil {
		t.Fatal("rebound old upload link")
	}
	if err = s.BindConversation("owner", a.ID, "87654321"); err == nil {
		t.Fatal("rebound file")
	}
}
