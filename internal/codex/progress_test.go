package codex

import (
	"strings"
	"testing"
)

func TestProgressReplacesDeltaWithAuthoritativeMessage(t *testing.T) {
	var got string
	p := newProgress(func(s string) { got = s })
	p.update("first", "commentary", "正在", false)
	p.update("first", "commentary", "检查", false)
	if got != "正在检查" {
		t.Fatal(got)
	}
	p.update("first", "commentary", "已检查资料", true)
	p.update("second", "final_answer", "结论", false)
	if got != "已检查资料\n\n结论" {
		t.Fatal("duplicated final item", got)
	}
	p.update("hidden", "reasoning", "不得显示的推理", true)
	if strings.Contains(got, "推理") {
		t.Fatal("reasoning exposed")
	}
	p.update("huge", "commentary", strings.Repeat("字", 100000), true)
	if len(got) > 256<<10 {
		t.Fatal("progress unbounded")
	}
}
