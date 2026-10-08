package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/conversations"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/models"
)

func TestMain(m *testing.M) {
	if fixture := os.Getenv("CAMPUS_WORKER_POOL_FIXTURE"); fixture != "" {
		fakeParallelCodex(fixture)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// A real child process speaking app-server JSONL lets this test exercise the
// complete claim, Codex, progress, thread persistence and result path.
func fakeParallelCodex(fixture string) {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 65536), 1<<20)
	var thread string
	resumed := false
	send := func(v any) { b, _ := json.Marshal(v); fmt.Println(string(b)) }
	for scanner.Scan() {
		var message struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &message) != nil {
			os.Exit(10)
		}
		reply := func(v any) { send(map[string]any{"id": message.ID, "result": v}) }
		switch message.Method {
		case "initialize":
			reply(map[string]any{})
		case "initialized":
		case "thread/start", "thread/resume":
			var p struct{ CWD, ThreadID string }
			json.Unmarshal(message.Params, &p)
			cid := filepath.Base(p.CWD)
			if cid == "aaaaaaaa" {
				thread = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
			} else if cid == "bbbbbbbb" {
				thread = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
			} else {
				os.Exit(11)
			}
			resumed = message.Method == "thread/resume"
			if resumed && p.ThreadID != thread {
				os.Exit(12)
			}
			reply(map[string]any{"thread": map[string]string{"id": thread}})
		case "turn/start":
			var p struct{ Input []struct{ Text string } }
			json.Unmarshal(message.Params, &p)
			if len(p.Input) != 1 {
				os.Exit(13)
			}
			text := p.Input[0].Text
			var current struct {
				ID    string `json:"job_id"`
				Input string `json:"user_message"`
			}
			if json.Unmarshal([]byte(text[strings.LastIndex(text, "\n")+1:]), &current) != nil {
				os.Exit(14)
			}
			started, _ := json.Marshal(map[string]any{"resumed": resumed, "thread": thread})
			os.WriteFile(filepath.Join(fixture, current.ID+".started"), started, 0600)
			turn := "turn-" + current.ID
			reply(map[string]any{"turn": map[string]string{"id": turn}})
			send(map[string]any{"method": "item/completed", "params": map[string]any{"threadId": thread, "turnId": turn, "item": map[string]string{"id": "progress", "type": "agentMessage", "phase": "commentary", "text": "progress:" + current.ID}}})
			if current.Input == "a1" {
				for {
					if _, err := os.Stat(filepath.Join(fixture, "release-a1")); err == nil {
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
			if current.Input == "a2" && !resumed {
				os.Exit(15)
			}
			send(map[string]any{"method": "item/completed", "params": map[string]any{"threadId": thread, "turnId": turn, "item": map[string]string{"id": "answer", "type": "agentMessage", "phase": "final_answer", "text": "reply:" + current.ID}}})
			send(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": thread, "turn": map[string]string{"id": turn, "status": "completed"}}})
		default:
			os.Exit(16)
		}
	}
}

func TestWorkerParallelSessionsAndExplicitNativeResume(t *testing.T) {
	defer metadb.CloseAll()
	root := t.TempDir()
	fixture := filepath.Join(root, "fixture")
	if err := os.Mkdir(fixture, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAMPUS_WORKER_POOL_FIXTURE", fixture)
	queue, err := jobs.Open(filepath.Join(root, "jobs"))
	if err != nil {
		t.Fatal(err)
	}
	choice := models.Choice{Model: "gpt-6-sol", Effort: "high"}
	enqueue := func(source, cid string) jobs.Job {
		j, e := queue.EnqueueConversation(source, source, "owner", "ctx-"+source, choice, nil, cid)
		if e != nil {
			t.Fatal(e)
		}
		return j
	}
	a1 := enqueue("a1", "aaaaaaaa")
	a2 := enqueue("a2", "aaaaaaaa")
	b1 := enqueue("b1", "bbbbbbbb")
	key := strings.Repeat("x", 32)
	relay := httptest.NewServer(jobs.Handler(queue, key))
	defer relay.Close()
	cfg := config{RelayURL: relay.URL, WorkRoot: filepath.Join(root, "tasks"), CodexBinary: os.Args[0], CodexHome: filepath.Join(root, "home"), Permissions: ":danger-full-access", TurnTimeoutSeconds: 30, LiveSteering: true}
	threads, err := conversations.OpenThreads(filepath.Join(root, "threads.json"))
	if err != nil {
		t.Fatal(err)
	}
	catalog := models.Catalog{Models: []models.Model{{ID: choice.Model, Efforts: []string{choice.Effort}, DefaultEffort: choice.Effort}}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runWorkerPool(ctx, 2, func(ctx context.Context) error {
			return workerLoop(ctx, cfg, catalog, threads, key, strings.Repeat("y", 32))
		})
	}()
	wait := func(check func() bool) {
		t.Helper()
		for !check() {
			select {
			case <-ctx.Done():
				t.Fatal("parallel fixture timed out")
			case err := <-done:
				t.Fatal("worker stopped early", err)
			case <-time.After(15 * time.Millisecond):
			}
		}
	}
	wait(func() bool { j, _ := queue.Snapshot(b1.ID); return j.Status == "done" })
	running, _ := queue.Snapshot(a1.ID)
	queued, _ := queue.Snapshot(a2.ID)
	if running.Status != "running" || queued.Status != "queued" {
		t.Fatal("different sessions failed to overlap or same session overlapped", running.Status, queued.Status)
	}
	if err := os.WriteFile(filepath.Join(fixture, "release-a1"), []byte("release"), 0600); err != nil {
		t.Fatal(err)
	}
	wait(func() bool { j, _ := queue.Snapshot(a2.ID); return j.Status == "done" })
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("parallel workers did not join")
	}
	for _, job := range []jobs.Job{a1, a2, b1} {
		j, _ := queue.Snapshot(job.ID)
		if j.Error != "" || j.Result != "reply:"+job.ID || j.ReplyContext != job.ReplyContext || !strings.Contains(j.Progress, "progress:"+job.ID) {
			t.Fatal("task output or progress crossed sessions", job.ID, j.Error, j.Result, j.Progress)
		}
	}
	saved, err := conversations.OpenThreads(filepath.Join(root, "threads.json"))
	if err != nil {
		t.Fatal(err)
	}
	if saved.Thread("aaaaaaaa") != "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" || saved.Thread("bbbbbbbb") != "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb" {
		t.Fatal("concurrent native mappings lost")
	}
	b, err := os.ReadFile(filepath.Join(fixture, a2.ID+".started"))
	if err != nil || !strings.Contains(string(b), `"resumed":true`) {
		t.Fatal("a2 did not explicitly resume a1's thread", string(b), err)
	}
}
