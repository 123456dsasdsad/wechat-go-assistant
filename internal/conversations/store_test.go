package conversations

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestNumberedSelectionReplayAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversations.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	a := s.Current()
	if a.Number != 1 {
		t.Fatal(a)
	}
	_, reply, err := s.Handle("new-b", "新建会话 论文")
	if err != nil || !strings.Contains(reply, "2. 论文") {
		t.Fatal(reply, err)
	}
	b := s.Current()
	if b.ID == a.ID || b.Number != 2 {
		t.Fatal(b)
	}
	if handled, _, _ := s.Handle("number-before-list", "1"); handled {
		t.Fatal("ordinary number became selection without menu")
	}
	_, list, _ := s.Handle("list", "会话列表")
	if !strings.Contains(list, "1. 微信会话") || !strings.Contains(list, "2. 论文") {
		t.Fatal(list)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if handled, _, err := s.Handle("pick-a", "1"); !handled || err != nil || s.Current().ID != a.ID {
		t.Fatal("persisted numbered menu failed")
	}
	_, replayed, _ := s.Handle("new-b", "新建会话 论文")
	if replayed != reply || s.Current().ID != a.ID {
		t.Fatal("replay changed current conversation")
	}
	_, _, _ = s.Handle("pick-b", "继续会话2")
	if s.Current().ID != b.ID {
		t.Fatal("compact numbered command failed")
	}
	if handled, _, _ := s.Handle("pick-a", "1"); !handled || s.Current().ID != b.ID {
		t.Fatal("numbered command replay was not idempotent")
	}
	_, _, _ = s.Handle("list-again", "会话列表")
	s.Handle("ordinary-task", "请读取文件")
	if handled, _, _ := s.Handle("ordinary-number", "1"); handled {
		t.Fatal("number selected after an intervening task")
	}
	_, _, _ = s.Handle("pick-unknown", "继续会话99")
	if s.Current().ID != b.ID {
		t.Fatal("invalid selection changed current")
	}
}
func TestNativeThreadCacheAndIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "threads.json")
	s, _ := OpenThreads(path)
	a := Turn{ConversationID: "aaaaaaaa", JobID: strings.Repeat("1", 24), ThreadID: "11111111-1111-4111-8111-111111111111", Text: "A answer", ToolCount: 1}
	b := Turn{ConversationID: "bbbbbbbb", JobID: strings.Repeat("2", 24), ThreadID: "22222222-2222-4222-8222-222222222222", Text: "B answer"}
	if err := s.Save(a); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(b); err != nil {
		t.Fatal(err)
	}
	s, err := OpenThreads(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Thread(a.ConversationID) != a.ThreadID || s.Thread(b.ConversationID) != b.ThreadID {
		t.Fatal("thread association lost")
	}
	if cached, ok := s.Cached(a.ConversationID, a.JobID); !ok || !reflect.DeepEqual(cached, a) {
		t.Fatal(cached)
	}
	if _, ok := s.Cached(b.ConversationID, a.JobID); ok {
		t.Fatal("foreign conversation got cached turn")
	}
	a.ThreadID = b.ThreadID
	a.JobID = strings.Repeat("3", 24)
	if err = s.Save(a); err == nil {
		t.Fatal("native thread replaced silently")
	}
}
