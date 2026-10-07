package conversations

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTaskTopicsRenameAndReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	s, _ := Open(path)
	a := s.Current()
	at := time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC)
	if err := s.RecordTask(a.ID, "请分析实验数据", nil, at); err != nil {
		t.Fatal(err)
	}
	if s.Current().Title != "分析实验数据" {
		t.Fatal("unnamed session did not gain a task title", s.Current())
	}
	s.RecordTask(a.ID, "请绘制拟合曲线", nil, at.Add(time.Hour))
	s.RecordTask(a.ID, "请分析实验数据", nil, at)
	if s.Current().LastTask != "绘制拟合曲线" {
		t.Fatal("old replay regressed preview")
	}
	s.Handle("new-b", "新建会话 测试")
	b := s.Current()
	s.RecordTask(b.ID, "请检查论文格式", nil, at)
	s.Handle("new-c", "新建会话 测试")
	c := s.Current()
	s.RecordTask(c.ID, "请检查代码逻辑", nil, at)
	_, list, _ := s.Handle("list", "会话列表")
	if !strings.Contains(list, "测试 · 检查论文格式") || !strings.Contains(list, "测试 · 检查代码逻辑") || !strings.Contains(list, "最近：") {
		t.Fatal(list)
	}
	s.Handle("rename-b", "重命名会话 2 数学论文")
	s.Handle("rename-current", "会话命名 代码项目")
	s.Handle("rename-replay", "重命名会话 2 新论文")
	s.Handle("rename-b", "重命名会话 2 数学论文")
	if s.Current().ID != c.ID {
		t.Fatal("rename selected another session")
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(b.ID)
	if got.Title != "新论文" || got.Number != 2 || got.FirstTask != "检查论文格式" {
		t.Fatal("rename/reopen lost identity or topic", got)
	}
}
func TestTopicRedactionAndChronologicalBackfill(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "sessions.json"))
	a := s.Current()
	at := time.Now().UTC()
	s.RecordTask(a.ID, "请处理后一个任务", nil, at.Add(time.Hour))
	s.RecordTask(a.ID, "请处理第一个任务", nil, at)
	if s.Current().Title != "处理第一个任务" || s.Current().LastTask != "处理后一个任务" {
		t.Fatal(s.Current())
	}
	s.RecordTask(a.ID, "密码是 should-not-appear", nil, at.Add(2*time.Hour))
	if strings.Contains(s.Current().LastTask, "should-not-appear") {
		t.Fatal("credential echoed")
	}
	s.RecordTask(a.ID, "分析 https://example.com/?token=private-query 链接", nil, at.Add(3*time.Hour))
	if strings.Contains(s.Current().LastTask, "private-query") {
		t.Fatal("query token echoed")
	}
	s.Handle("empty", "新建会话")
	_, list, _ := s.Handle("menu", "会话列表")
	if !strings.Contains(list, "尚无任务") {
		t.Fatal("blank session did not explain its state")
	}
}
