package main

import (
	"encoding/json"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/conversations"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/models"
	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReplayKeepsInputFilesConversationAndOrdering(t *testing.T) {
	root := t.TempDir()
	cfg := config{StatePath: filepath.Join(root, "weixin.json"), JobsDir: filepath.Join(root, "jobs"), FilesDir: filepath.Join(root, "files"), ModelsFile: filepath.Join(root, "models.json"), ConversationsFile: filepath.Join(root, "conversations.json")}
	account := weixin.NewState(weixin.Account{BotToken: "fixture-token", BotID: "fixture-bot", OwnerID: "owner", BaseURL: weixin.DefaultAPI})
	account.Contexts["owner"] = "latest-reply-context"
	if err := weixin.SaveState(cfg.StatePath, account); err != nil {
		t.Fatal(err)
	}
	catalog := models.Catalog{Models: []models.Model{{ID: "gpt-6-sol", Efforts: []string{"high"}, DefaultEffort: "high"}}}
	raw, _ := json.Marshal(catalog)
	os.WriteFile(cfg.ModelsFile, raw, 0600)
	sessions, _ := conversations.Open(cfg.ConversationsFile)
	originalSession := sessions.Current()
	sessions.Handle("new-selected", "新建会话 other")
	selected := sessions.Current()
	fs, _ := files.Open(cfg.FilesDir)
	ref, _ := fs.Save("owner", "source", "project.zip", strings.NewReader("fixture"))
	store, _ := jobs.Open(cfg.JobsDir)
	first, _ := store.EnqueueConversation("upload", "original file instruction", "owner", "old-context", models.Choice{Model: "gpt-6-sol", Effort: "high"}, []files.Ref{ref}, originalSession.ID)
	second, _ := store.EnqueueConversation("followup", "original later instruction", "owner", "old-context", models.Choice{Model: "gpt-6-sol", Effort: "high"}, nil, originalSession.ID)
	for i := 0; i < 2; i++ {
		task, _ := store.Claim(time.Now())
		if task == nil {
			t.Fatal("fixture task missing")
		}
		if err := store.Complete(jobs.Completion{ID: task.ID, Lease: task.Lease, Result: "old result"}, time.Now()); err != nil {
			t.Fatal(err)
		}
		store.Delivered(task.ID)
	}
	pending, _ := store.EnqueueConversation("pending-notification", "already completed", "owner", "old-context", models.Choice{Model: "gpt-6-sol", Effort: "high"}, nil, originalSession.ID)
	task, _ := store.Claim(time.Now())
	if err := store.Complete(jobs.Completion{ID: task.ID, Lease: task.Lease, Result: "pending notification"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "config.json")
	raw, _ = json.Marshal(cfg)
	os.WriteFile(path, raw, 0600)
	if err := run(path, []string{first.ID, second.ID}); err != nil {
		t.Fatal(err)
	}
	store, _ = jobs.Open(cfg.JobsDir)
	all := store.History()
	if len(all) != 5 {
		t.Fatal("originals were replaced", len(all))
	}
	if all[0].Status != "delivered" || all[1].Status != "delivered" {
		t.Fatal("old status changed")
	}
	if all[2].ID != pending.ID || all[2].Status != "done" || all[2].Result != "pending notification" {
		t.Fatal("repair altered an already completed notification")
	}
	if all[3].Input != first.Input || all[4].Input != second.Input || len(all[3].Attachments) != 1 || all[3].Attachments[0] != ref || len(all[4].Attachments) != 0 {
		t.Fatal("replay snapshot changed")
	}
	for _, replay := range all[3:] {
		if replay.ConversationID != originalSession.ID || replay.ReplyContext != "latest-reply-context" || replay.Model != first.Model || replay.Effort != first.Effort {
			t.Fatal("replay lost original context/model or current reply route")
		}
	}
	sessions, _ = conversations.Open(cfg.ConversationsFile)
	if sessions.Current().ID != selected.ID {
		t.Fatal("replay switched user's selected session")
	}
	if err := run(path, []string{first.ID, second.ID}); err == nil {
		t.Fatal("replay ignored active queued jobs")
	}
}
