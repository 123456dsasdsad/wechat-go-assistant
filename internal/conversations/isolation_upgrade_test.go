package conversations

import (
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"path/filepath"
	"testing"
)

func TestNativeThreadCannotBelongToTwoConversations(t *testing.T) {
	defer metadb.CloseAll()
	s, err := OpenThreads(filepath.Join(t.TempDir(), "threads.json"))
	if err != nil {
		t.Fatal(err)
	}
	first := Turn{ConversationID: "12345678", JobID: "123456789012345678901234", ThreadID: "12345678-1234-1234-1234-123456789012", Text: "first"}
	if err = s.Save(first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.ConversationID = "87654321"
	second.JobID = "223456789012345678901234"
	if err = s.Save(second); err == nil {
		t.Fatal("shared native thread accepted")
	}
	if s.Thread(second.ConversationID) != "" {
		t.Fatal("rejected mapping was persisted")
	}
}
