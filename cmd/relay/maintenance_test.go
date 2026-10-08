package main

import (
	"context"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/maintenance"
	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
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

type maintenanceChunkSender struct {
	texts []string
	fail  bool
}

func (s *maintenanceChunkSender) SendText(_ context.Context, _ weixin.Reply, text string) (weixin.SendResult, error) {
	s.texts = append(s.texts, text)
	if s.fail && len(s.texts) == 2 {
		return weixin.SendResult{}, &weixin.APIError{Operation: "sendmessage", Ret: -2, Code: 0}
	}
	return weixin.SendResult{}, nil
}
func TestMaintenancePartialSendResumesAndRecordsAPIRejection(t *testing.T) {
	store, _ := maintenance.Open(t.TempDir())
	r := maintenance.NewReport("campus", "accounts", "2026-10-08", strings.Repeat("甲", 2000)+"tail")
	store.Put(r)
	sender := &maintenanceChunkSender{fail: true}
	if deliverMaintenanceReport(context.Background(), sender, store, r, "owner") == nil {
		t.Fatal("rejection ignored")
	}
	failed, _ := store.LatestReport("campus", "accounts")
	if !failed.Accepted.IsZero() || failed.LastError != "wechat_ret_-2_errcode_0" || failed.SentChunks != 1 {
		t.Fatalf("bad receipt %+v", failed)
	}
	reopened, _ := maintenance.Open(store.Dir)
	next := &maintenanceChunkSender{}
	if e := deliverMaintenanceReport(context.Background(), next, reopened, failed, "owner"); e != nil {
		t.Fatal(e)
	}
	accepted, _ := reopened.LatestReport("campus", "accounts")
	if accepted.Accepted.IsZero() || len(next.texts) != 1 || next.texts[0] != "tail" || accepted.SentChunks != 2 {
		t.Fatal("partial notification lost or replayed")
	}
}
