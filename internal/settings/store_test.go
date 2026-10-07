package settings

import (
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/models"
	"path/filepath"
	"strings"
	"testing"
)

func catalogFixture() models.Catalog {
	return models.Catalog{Models: []models.Model{{ID: "gpt-6-sol", Efforts: []string{"low", "high"}, DefaultEffort: "high"}, {ID: "gpt-6-luna", Efforts: []string{"low", "high"}, DefaultEffort: "high"}, {ID: "gpt-6.1-sol", Efforts: []string{"low", "high"}, DefaultEffort: "high"}}}
}
func TestCommandsPersistWithoutChangingExistingJob(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s, err := Open(path, catalogFixture())
	if err != nil {
		t.Fatal(err)
	}
	queue, _ := jobs.Open(t.TempDir())
	a, _ := queue.EnqueueSelected("task-a", "read file", "owner", "ctx", s.Current())
	handled, reply, err := s.Handle("command-a", "默认模型 2")
	if !handled || err != nil || !strings.Contains(reply, "gpt-6-luna") {
		t.Fatal(handled, reply, err)
	}
	s, err = Open(path, catalogFixture())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := queue.EnqueueSelected("task-b", "read file", "owner", "ctx", s.Current())
	if a.Model != "gpt-6-sol" || b.Model != "gpt-6-luna" {
		t.Fatal(a.Model, b.Model)
	}
	_, _, _ = s.Handle("command-b", "推理强度 low")
	_, replayed, _ := s.Handle("command-a", "默认模型 2")
	if replayed != reply || s.Current().Effort != "low" {
		t.Fatal("replay reverted newer setting")
	}
	again, _ := queue.EnqueueSelected("task-a", "different", "owner", "ctx", s.Current())
	if again.Model != a.Model || again.Effort != a.Effort || again.Input != a.Input {
		t.Fatal("job replay changed snapshot")
	}
}
func TestInvalidCommandsAndTemporaryChoice(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "settings.json"), catalogFixture())
	for i, cmd := range []string{"默认模型 claude-opus-5-5", "默认模型 --dangerous", "推理强度 ultra", "默认模型"} {
		handled, _, err := s.Handle(string(rune('a'+i)), cmd)
		if !handled || err != nil || s.Current() != (models.Choice{Model: "gpt-6-sol", Effort: "high"}) {
			t.Fatal("invalid command changed settings")
		}
	}
	choice, body, err := s.ChoiceForTask("使用 gpt-6-luna：读取 fixture.txt")
	if err != nil || choice.Model != "gpt-6-luna" || body != "读取 fixture.txt" || s.Current().Model != "gpt-6-sol" {
		t.Fatal(choice, body, err)
	}
	if _, _, err = s.ChoiceForTask("使用 unknown：任务"); err == nil {
		t.Fatal("unknown temporary model accepted")
	}
	if handled, _, _ := s.Handle("ordinary", "请解释模型的含义"); handled {
		t.Fatal("ordinary text became command")
	}
}

func TestMobileCommandFormats(t *testing.T) {
	for _, input := range []string{"默认模型gpt-6.1-sol", "默认模型gpt-6-luna", "默认模型：gpt-6-luna", "默认模型:gpt-6-luna", "切换模型为gpt-6-luna", "把模型改为gpt-6-luna", "请将模型切换为 gpt-6-luna。", "默认模型2"} {
		t.Run(input, func(t *testing.T) {
			s, _ := Open(filepath.Join(t.TempDir(), "settings.json"), catalogFixture())
			handled, reply, err := s.Handle("mobile", input)
			wantModel := "gpt-6-luna"
			if strings.Contains(input, "gpt-6.1-sol") {
				wantModel = "gpt-6.1-sol"
			}
			if !handled || err != nil || s.Current().Model != wantModel || !strings.Contains(reply, "已保存") {
				t.Fatal(handled, reply, err, s.Current())
			}
			if handled, _, err = s.Handle("effort", "推理强度：低"); !handled || err != nil || s.Current().Effort != "low" {
				t.Fatal("mobile effort was not saved")
			}
		})
	}
}

func TestMobileInvalidCommandsNeverBecomeTasks(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "settings.json"), catalogFixture())
	for i, input := range []string{"默认模型unknown", "默认模型：claude-opus-5.5", "推理强度ultra", "默认模型gpt-6-luna\n推理强度low", "切换模型为 gpt-6-luna 然后读取文件"} {
		handled, _, err := s.Handle(string(rune('a'+i)), input)
		if !handled || err != nil || s.Current() != (models.Choice{Model: "gpt-6-sol", Effort: "high"}) {
			t.Fatal(input, handled, err, s.Current())
		}
	}
	for _, input := range []string{"模型是什么", "默认模型会影响速度吗？", "请解释切换模型的原理", "模型 gpt-6-sol 是否适合阅读？"} {
		// The last example starts with the exact command keyword, so it gets syntax help.
		wantHandled := strings.HasPrefix(input, "模型 ")
		if handled, _, _ := s.Handle("ordinary-"+input, input); handled != wantHandled {
			t.Fatal("unexpected routing", input, handled)
		}
	}
	choice, body, err := s.ChoiceForTask("使用gpt-6-luna：读取 fixture.txt")
	if err != nil || choice.Model != "gpt-6-luna" || body != "读取 fixture.txt" || s.Current().Model != "gpt-6-sol" {
		t.Fatal(choice, body, err)
	}
}
