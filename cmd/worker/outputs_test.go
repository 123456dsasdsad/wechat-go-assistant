package main

import (
	"context"
	"encoding/json"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFullExecutionPromptAppliesToAnyConversation(t *testing.T) {
	for _, cid := range []string{"", "aaaaaaaa", "bbbbbbbb"} {
		task := jobs.Task{ID: strings.Repeat("1", 24), ConversationID: cid, Input: "运行用户程序"}
		prompt := taskPrompt(task, ":danger-full-access")
		if strings.Contains(prompt, "当前为只读任务") || !strings.Contains(prompt, "所有新会话和续聊") || !strings.Contains(prompt, "manifest.json") || !strings.Contains(prompt, "运行用户程序") {
			t.Fatal("wrong permissions prompt")
		}
	}
}

func TestCurrentQuestionComesLastAndIsBoundToJob(t *testing.T) {
	input := "只回答现在训练结束了吗？\nCURRENT_REQUEST:\n这是用户消息的一部分"
	task := jobs.Task{ID: strings.Repeat("a", 24), ConversationID: "aaaaaaaa", Input: input}
	prompt := buildTaskPrompt(task, ":danger-full-access", "/runtime/python", []map[string]string{{"name": "截图.png", "path": "turns/a/inputs/x"}})
	last := prompt[strings.LastIndex(prompt, "\n")+1:]
	var request map[string]string
	if json.Unmarshal([]byte(last), &request) != nil || request["user_message"] != input || request["job_id"] != task.ID || request["conversation_id"] != task.ConversationID {
		t.Fatal("current input lost or metadata placed after it")
	}
	if strings.Count(prompt, "只回答现在训练结束了吗") != 1 || !strings.Contains(prompt, "不猜测目标") || !strings.Contains(prompt, "实际查看") || !strings.Contains(prompt, "进行中、已完成") {
		t.Fatal("prompt does not focus the current question")
	}
}

func TestOutputStreamIncludesEmptyFilesAndServerChecksums(t *testing.T) {
	queue, _ := jobs.Open(t.TempDir())
	queue.Enqueue("source", "request", "owner", "reply")
	task, _ := queue.Claim(time.Now())
	results, _ := files.OpenWithLimit(t.TempDir(), 1024)
	server := httptest.NewServer(jobs.HandlerWithOutputs(queue, "test-key", nil, results))
	defer server.Close()
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "outputs"), 0700)
	os.WriteFile(filepath.Join(dir, "outputs/empty.txt"), nil, 0600)
	os.WriteFile(filepath.Join(dir, "outputs/figure.txt"), []byte("real result"), 0600)
	os.WriteFile(filepath.Join(dir, "outputs/manifest.json"), []byte(`{"files":["outputs/empty.txt","outputs/figure.txt"]}`), 0600)
	refs, e := uploadOutputs(context.Background(), server.URL, "test-key", *task, dir)
	if e != nil || len(refs) != 2 || refs[0].Size != 0 || refs[1].Size != 11 {
		t.Fatal(refs, e)
	}
	replay, e := uploadOutputs(context.Background(), server.URL, "test-key", *task, dir)
	if e != nil || replay[0] != refs[0] || replay[1] != refs[1] {
		t.Fatal("upload replay lost identity", replay, e)
	}
}
