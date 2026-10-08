package codex

import (
	"context"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/usage"
	"os"
	"testing"
	"time"
)

func TestUsageSubtractsPreviousTurnAndIgnoresForeignEvents(t *testing.T) {
	t.Setenv("CAMPUS_TEST_RPC", "usage")
	c := Config{Binary: os.Args[0], Home: t.TempDir(), Directory: t.TempDir(), Key: "key", Model: "gpt-6-sol", Permissions: ":danger-full-access", Persistent: true, AppServer: true, ThreadID: fakeThread, UsageBaseline: usage.Tokens{Available: true, Input: 200, Cached: 30, Output: 10, Total: 210}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, e := Run(ctx, c, "test")
	if e != nil || !r.Usage.Available || r.Usage.Input != 200 || r.Usage.Cached != 20 || r.Usage.Output != 10 || r.Usage.Total != 210 {
		t.Fatal(r, e)
	}
}
