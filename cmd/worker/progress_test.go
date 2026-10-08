package main

import (
	"context"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProgressReachesRelayBeforeResultAndFlushesLatest(t *testing.T) {
	defer metadb.CloseAll()
	s, _ := jobs.Open(t.TempDir())
	j, _ := s.Enqueue("source", "task", "owner", "context")
	task, _ := s.Claim(time.Now())
	server := httptest.NewServer(jobs.Handler(s, "key"))
	defer server.Close()
	p := newRelayProgress(context.Background(), server.URL, "key", *task)
	p.Publish("正在读取资料")
	deadline := time.Now().Add(4 * time.Second)
	for {
		got, _ := s.Snapshot(j.ID)
		if got.Progress != "" {
			if got.Status != "running" || got.Result != "" {
				t.Fatal("not live")
			}
			break
		}
		if time.Now().After(deadline) {
			p.Close()
			t.Fatal("progress not delivered while running")
		}
		time.Sleep(20 * time.Millisecond)
	}
	p.Publish("资料已经读取\n准备结果")
	p.Close()
	got, _ := s.Snapshot(j.ID)
	if got.Progress != "资料已经读取\n准备结果" || got.ProgressSequence != 2 {
		t.Fatal("latest update lost", got.Progress)
	}
}
