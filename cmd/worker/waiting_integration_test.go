package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/conversations"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/models"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/userinput"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/workerpermits"
)

// A child process substitutes only the model protocol. The real worker, question
// HTTP handlers, permit HTTP handlers, leases, result path and thread store run.
func fakeWaitingCodex(fixture string) {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 65536), 1<<20)
	send := func(v any) { b, _ := json.Marshal(v); fmt.Println(string(b)) }
	var thread, turn, job, input string
	finish := func() {
		send(map[string]any{"method": "item/completed", "params": map[string]any{"threadId": thread, "turnId": turn, "item": map[string]string{"id": "answer", "type": "agentMessage", "phase": "final_answer", "text": "reply:" + input}}})
		send(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": thread, "turn": map[string]string{"id": turn, "status": "completed"}}})
	}
	for scanner.Scan() {
		var m struct {
			ID             json.RawMessage
			Method         string
			Params, Result json.RawMessage
		}
		if json.Unmarshal(scanner.Bytes(), &m) != nil {
			os.Exit(20)
		}
		reply := func(v any) { send(map[string]any{"id": m.ID, "result": v}) }
		switch m.Method {
		case "initialize":
			reply(map[string]any{})
		case "initialized":
		case "thread/start", "thread/resume":
			var p struct{ CWD, ThreadID string }
			json.Unmarshal(m.Params, &p)
			if filepath.Base(p.CWD) == "aaaaaaaa" {
				thread = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
			} else {
				thread = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
			}
			if m.Method == "thread/resume" && p.ThreadID != thread {
				os.Exit(21)
			}
			reply(map[string]any{"thread": map[string]string{"id": thread}})
		case "turn/start":
			var p struct{ Input []struct{ Text string } }
			if json.Unmarshal(m.Params, &p) != nil || len(p.Input) != 1 {
				os.Exit(22)
			}
			text := p.Input[0].Text
			var current struct {
				ID    string `json:"job_id"`
				Input string `json:"user_message"`
			}
			if json.Unmarshal([]byte(text[strings.LastIndex(text, "\n")+1:]), &current) != nil {
				os.Exit(23)
			}
			job, input = current.ID, current.Input
			turn = "turn-" + job
			reply(map[string]any{"turn": map[string]string{"id": turn}})
			os.WriteFile(filepath.Join(fixture, job+".started"), []byte(input), 0600)
			if input == "wait-a" {
				send(map[string]any{"id": "waiting-rpc", "method": "item/tool/requestUserInput", "params": map[string]any{"threadId": thread, "turnId": turn, "itemId": "waiting-item", "isBlocking": true, "questions": []userinput.Question{{ID: "choice", Header: "选择", Question: "选择A或B", Options: []userinput.Option{{Label: "A", Description: "A"}, {Label: "B", Description: "B"}}}}}})
			} else {
				if input == "busy-b" {
					for {
						if _, e := os.Stat(filepath.Join(fixture, "release-b")); e == nil {
							break
						}
						time.Sleep(10 * time.Millisecond)
					}
				}
				finish()
			}
		case "":
			if string(m.ID) != `"waiting-rpc"` {
				os.Exit(24)
			}
			var response userinput.Response
			if json.Unmarshal(m.Result, &response) != nil || len(response.Answers["choice"].Answers) != 1 || response.Answers["choice"].Answers[0] != "B" {
				os.Exit(25)
			}
			os.WriteFile(filepath.Join(fixture, job+".answered"), []byte("B"), 0600)
			finish()
		default:
			os.Exit(26)
		}
	}
}

func TestWaitingQuestionReleasesSlotAndResumesAfterBusyTask(t *testing.T) {
	defer metadb.CloseAll()
	root := t.TempDir()
	fixture := filepath.Join(root, "fixture")
	if e := os.Mkdir(fixture, 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("CAMPUS_WORKER_WAIT_FIXTURE", fixture)
	queue, e := jobs.Open(filepath.Join(root, "jobs"))
	if e != nil {
		t.Fatal(e)
	}
	choice := models.Choice{Model: "gpt-6-sol", Effort: "high"}
	enqueue := func(source, cid string) jobs.Job {
		t.Helper()
		j, e := queue.EnqueueConversation(source, source, "owner", "ctx", choice, nil, cid)
		if e != nil {
			t.Fatal(e)
		}
		return j
	}
	a := enqueue("wait-a", "aaaaaaaa")
	next := enqueue("after-a", "aaaaaaaa")
	b := enqueue("busy-b", "bbbbbbbb")
	key := strings.Repeat("x", 32)
	relay := httptest.NewServer(jobs.Handler(queue, key))
	defer relay.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := workerpermits.New(1)
	stop, e := pool.Start(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer stop()
	cfg := config{RelayURL: relay.URL, WorkRoot: filepath.Join(root, "tasks"), CodexBinary: os.Args[0], CodexHome: filepath.Join(root, "home"), Permissions: ":danger-full-access", TurnTimeoutSeconds: 25, LiveSteering: true, permits: pool}
	threads, e := conversations.OpenThreads(filepath.Join(root, "threads.json"))
	if e != nil {
		t.Fatal(e)
	}
	catalog := models.Catalog{Models: []models.Model{{ID: choice.Model, Efforts: []string{choice.Effort}, DefaultEffort: choice.Effort}}}
	done := make(chan error, 1)
	go func() {
		done <- runElasticWorkerPool(ctx, 3, func(ctx context.Context) error {
			return workerLoop(ctx, cfg, catalog, threads, key, strings.Repeat("y", 32))
		})
	}()
	joined := false
	defer func() {
		cancel()
		if !joined {
			select {
			case <-done:
			case <-time.After(4 * time.Second):
				t.Error("waiting workers did not join")
			}
		}
	}()
	wait := func(check func() bool) {
		t.Helper()
		for !check() {
			for _, id := range []string{a.ID, b.ID, next.ID} {
				if j, ok := queue.Snapshot(id); ok && j.Error != "" {
					t.Fatalf("fixture task %s failed: %s", j.Input, j.Error)
				}
			}
			select {
			case <-ctx.Done():
				t.Fatal("waiting fixture timed out")
			case e := <-done:
				joined = true
				t.Fatal("worker stopped", e)
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
	wait(func() bool {
		j, _ := queue.Snapshot(a.ID)
		_, e := os.Stat(filepath.Join(fixture, b.ID+".started"))
		return j.WaitingForUser() && e == nil
	})
	j, _ := queue.Snapshot(next.ID)
	if j.Status != "queued" {
		t.Fatal("same session overlapped waiting task", j.Status)
	}
	first, _ := queue.Snapshot(a.ID)
	q := first.Questions[0]
	if _, e = queue.AnswerQuestion("owner", a.ID, q.ID, "choice", "answer-b", "B", time.Now()); e != nil {
		t.Fatal(e)
	}
	// Allow the question poll to see B while the sole executing slot is still busy.
	time.Sleep(1300 * time.Millisecond)
	if _, e = os.Stat(filepath.Join(fixture, a.ID+".answered")); !os.IsNotExist(e) {
		t.Fatal("resumed model without reacquiring its execution slot", e)
	}
	still, _ := queue.Snapshot(a.ID)
	if still.Questions[0].State != "answered" {
		t.Fatal("question resolved before execution slot", still.Questions[0].State)
	}
	if e = os.WriteFile(filepath.Join(fixture, "release-b"), []byte("release"), 0600); e != nil {
		t.Fatal(e)
	}
	wait(func() bool { j, _ := queue.Snapshot(next.ID); return j.Status == "done" })
	for _, job := range []jobs.Job{a, b, next} {
		j, _ := queue.Snapshot(job.ID)
		if j.Error != "" || j.Result != "reply:"+job.Input {
			t.Fatal("wrong result or failed task", j.ID, j.Error, j.Result)
		}
	}
	completed, _ := queue.Snapshot(a.ID)
	if completed.Questions[0].State != "resolved" {
		t.Fatal("answer was not consumed", completed.Questions[0].State)
	}
	cancel()
	select {
	case e := <-done:
		joined = true
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("worker pool did not stop")
	}
}
