package assistant

import (
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/models"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSessionMemoryDoesNotLeakAndGlobalMemoryIsExplicit(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "assistant.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer metadb.CloseAll()
	choice := models.Choice{Model: "gpt-6-sol", Effort: "high"}
	if _, _, err = s.Handle("owner", "one", "记住 项目 研序", "aaaaaaaa", choice, time.Now()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.MemoryText("owner", "aaaaaaaa"), "研序") || strings.Contains(s.MemoryText("owner", "bbbbbbbb"), "研序") {
		t.Fatal("session memory leaked")
	}
	if _, _, err = s.Handle("owner", "two", "全局记住 语言 中文", "aaaaaaaa", choice, time.Now()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.MemoryText("owner", "bbbbbbbb"), "中文") {
		t.Fatal("explicit personal preference missing")
	}
}
