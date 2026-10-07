package codex

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAppServerStreamsOnlyCurrentAssistantOutput(t *testing.T) {
	t.Setenv("CAMPUS_TEST_RPC", "progress")
	var updates []string
	c := Config{Binary: os.Args[0], Home: t.TempDir(), Directory: t.TempDir(), Key: "key", Model: "gpt-6-sol", Permissions: ":danger-full-access", Persistent: true, AppServer: true, Progress: func(text string) { updates = append(updates, text) }}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, e := Run(ctx, c, "test")
	if e != nil || r.Text != "最终结论" || len(updates) != 3 || updates[0] != "正在检查" || updates[1] != "资料已检查" || updates[2] != "资料已检查\n\n最终结论" {
		t.Fatal(r, e, updates)
	}
	for _, u := range updates {
		if strings.Contains(u, "推理") || strings.Contains(u, "其他任务") {
			t.Fatal("wrong output exposed")
		}
	}
}
