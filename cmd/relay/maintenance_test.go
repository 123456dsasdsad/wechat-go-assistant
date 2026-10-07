package main

import (
	"context"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/maintenance"
	"strings"
	"testing"
)

func TestMaintenanceCommandsNeverEnqueueAI(t *testing.T) {
	in := inboundFixture(t)
	in.reports, _ = maintenance.Open(t.TempDir())
	in.reports.Put(maintenance.NewReport("cloud", "usage", "2026-10-06", "verified daily tokens"))
	for _, input := range []string{"用量日报", "运维日报", "账号状态", "更新状态"} {
		if e := in.handle(context.Background(), textMessage(input, input)); e != nil {
			t.Fatal(e)
		}
	}
	if len(in.queue.History()) != 0 {
		t.Fatal("maintenance triggered inference")
	}
	in.handle(context.Background(), textMessage("usage-again", "用量日报"))
	if !strings.Contains(in.client.(*fakeMessages).text, "verified daily tokens") {
		t.Fatal("report unavailable on phone")
	}
}
