package assistant

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/models"
)

func TestMemoryScopesCommandsAndReopen(t *testing.T) {
	defer metadb.CloseAll()
	path := filepath.Join(t.TempDir(), "assistant.sqlite")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	choice := models.Choice{Model: "gpt-6.1-sol", Effort: "high"}
	handled, reply, e := s.Handle("owner", "message", "记住 绘图 中文标签", "aaaaaaaa", choice, time.Now())
	if !handled || e != nil || reply == "" || s.MemoryText("owner") == "" || s.MemoryText("other") != "" {
		t.Fatal(reply, e)
	}
	_, again, e := s.Handle("owner", "message", "记住 绘图 英文标签", "aaaaaaaa", choice, time.Now())
	if e != nil || again != reply {
		t.Fatal("replay changed preference")
	}
	s, e = Open(path)
	if e != nil || s.MemoryText("owner") == "" {
		t.Fatal("memory lost", e)
	}
	_, _, e = s.Handle("owner", "forget", "忘记 绘图", "aaaaaaaa", choice, time.Now())
	if e != nil || s.MemoryText("owner") != "" {
		t.Fatal("delete failed", e)
	}
}

func TestPlanReplayDispatchDailyAndCancel(t *testing.T) {
	defer metadb.CloseAll()
	s, e := Open(filepath.Join(t.TempDir(), "assistant.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 10, 8, 8, 0, 0, 0, Beijing)
	choice := models.Choice{Model: "gpt-6.1-sol", Effort: "high"}
	_, reply, e := s.Handle("owner", "plan", "每日任务 09:00 检查指定项目", "aaaaaaaa", choice, now)
	if e != nil {
		t.Fatal(e)
	}
	_, again, e := s.Handle("owner", "plan", "每日任务 10:00 改写内容", "aaaaaaaa", choice, now)
	if e != nil || again != reply {
		t.Fatal("replay changed plan")
	}
	count := 0
	sources := map[string]bool{}
	enqueue := func(p Plan, source string) (string, error) {
		count++
		sources[source] = true
		if p.Model != choice.Model || p.Conversation != "aaaaaaaa" {
			t.Fatal(p)
		}
		return "job", nil
	}
	if e = s.Dispatch(now.Add(30*time.Minute), enqueue); e != nil || count != 0 {
		t.Fatal(e, count)
	}
	if e = s.Dispatch(now.Add(2*time.Hour), enqueue); e != nil || count != 1 {
		t.Fatal(e, count)
	}
	if e = s.Dispatch(now.Add(3*time.Hour), enqueue); e != nil || count != 1 {
		t.Fatal("repeated occurrence", e, count)
	}
	if e = s.Dispatch(now.Add(26*time.Hour), enqueue); e != nil || count != 2 || len(sources) != 2 {
		t.Fatal(e, count)
	}
	var id string
	if e = s.db.QueryRow("SELECT id FROM plans WHERE owner='owner'").Scan(&id); e != nil {
		t.Fatal(e)
	}
	_, _, e = s.Handle("other", "foreign", "取消定时 "+id, "aaaaaaaa", choice, now)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Dispatch(now.Add(50*time.Hour), enqueue); e != nil || count != 3 {
		t.Fatal("foreign cancellation", e, count)
	}
	_, _, e = s.Handle("owner", "cancel", "取消定时 "+id, "aaaaaaaa", choice, now)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Dispatch(now.Add(74*time.Hour), enqueue); e != nil || count != 3 {
		t.Fatal("cancel failed", e, count)
	}
}
