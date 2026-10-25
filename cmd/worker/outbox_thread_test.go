package main

import (
	"github.com/123456dsasdsad/wechat-go-assistant/internal/conversations"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"path/filepath"
	"testing"
)

func TestOutboxRecoversThreadMappingBeforeReplayingResult(t *testing.T) {
	defer metadb.CloseAll()
	root := t.TempDir()
	threads, err := conversations.OpenThreads(filepath.Join(root, "threads.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config{WorkRoot: root, threads: threads}
	task := jobs.Task{ID: "123456789012345678901234", ConversationID: "12345678", Lease: "old"}
	turn := conversations.Turn{ConversationID: task.ConversationID, JobID: task.ID, ThreadID: "12345678-1234-1234-1234-123456789012"}
	_, err = queueResult(cfg, task, jobs.Completion{ID: task.ID, Lease: task.Lease, Result: "saved answer"}, root, turn)
	if err != nil {
		t.Fatal(err)
	}
	if threads.Thread(task.ConversationID) != "" {
		t.Fatal("test must simulate crash before thread commit")
	}
	found, err := cachedOutbox(cfg, task.ID, "new-lease")
	if err != nil || !found {
		t.Fatal(found, err)
	}
	if threads.Thread(task.ConversationID) != turn.ThreadID {
		t.Fatal("native thread lost after crash")
	}
	saved, ok := threads.Cached(task.ConversationID, task.ID)
	if !ok || saved.Text != "saved answer" {
		t.Fatal(saved, ok)
	}
	if _, err = cachedOutbox(cfg, task.ID, "new-lease"); err != nil {
		t.Fatal("not idempotent", err)
	}
}
