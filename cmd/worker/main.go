package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/codex"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/conversations"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/models"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/userinput"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type config struct {
	ConfigPath         string `json:"-"`
	RelayURL           string `json:"relay_url"`
	RelayKeyFile       string `json:"relay_key_file"`
	APIKeyFile         string `json:"api_key_file"`
	CodexBinary        string `json:"codex_binary"`
	CodexHome          string `json:"codex_home"`
	WorkRoot           string `json:"work_root"`
	ModelsFile         string `json:"models_file"`
	ThreadsFile        string `json:"threads_file"`
	Permissions        string `json:"permissions"`
	TurnTimeoutSeconds int    `json:"turn_timeout_seconds"`
	PythonBinary       string `json:"python_binary,omitempty"`
	LiveSteering       bool   `json:"live_steering,omitempty"`
	MaxConcurrentTasks int    `json:"max_concurrent_tasks,omitempty"`
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--training-run" {
		fs := flag.NewFlagSet("training", flag.ExitOnError)
		path := fs.String("config", "", "config")
		id := fs.String("id", "", "id")
		resume := fs.Bool("resume", false, "resume")
		command := fs.String("command", "", "command")
		fs.Parse(os.Args[2:])
		b, e := os.ReadFile(*path)
		var cfg config
		if e != nil || json.Unmarshal(b, &cfg) != nil {
			os.Exit(1)
		}
		cfg.ConfigPath = *path
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		if e = trainingRun(ctx, cfg, *id, *resume, *command); e != nil {
			fmt.Fprintln(os.Stderr, e)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--question-mcp" {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		connection := userinput.Connection{URL: os.Getenv("WECHAT_QUESTION_URL"), Key: os.Getenv("WECHAT_QUESTION_KEY"), JobID: os.Getenv("WECHAT_QUESTION_JOB"), Lease: os.Getenv("WECHAT_QUESTION_LEASE")}
		if !connection.Valid() {
			fmt.Fprintln(os.Stderr, "invalid_question_connection")
			os.Exit(1)
		}
		_ = userinput.ServeMCP(ctx, os.Stdin, os.Stdout, connection.Wait)
		return
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func pause(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
func run(ctx context.Context) error {
	path := flag.String("config", "", "private config path")
	flag.Parse()
	b, e := os.ReadFile(*path)
	if e != nil {
		return errors.New("worker_config_unreadable")
	}
	var cfg config
	if json.Unmarshal(b, &cfg) != nil {
		return errors.New("invalid_worker_config")
	}
	cfg.ConfigPath = *path
	if cfg.Permissions != "" && cfg.Permissions != ":danger-full-access" {
		return errors.New("invalid_worker_permissions")
	}
	if cfg.TurnTimeoutSeconds == 0 {
		cfg.TurnTimeoutSeconds = 180
	}
	if cfg.TurnTimeoutSeconds < 30 || cfg.TurnTimeoutSeconds > 86400 {
		return errors.New("invalid_turn_timeout")
	}
	cfg.MaxConcurrentTasks, e = workerParallelism(cfg.MaxConcurrentTasks)
	if e != nil {
		return e
	}
	catalog, e := models.Load(cfg.ModelsFile)
	if e != nil {
		return e
	}
	u, e := url.Parse(cfg.RelayURL)
	if e != nil || u.Scheme != "http" || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() {
		return errors.New("relay_must_be_private_loopback")
	}
	b, e = os.ReadFile(cfg.RelayKeyFile)
	if e != nil {
		return errors.New("relay_key_unreadable")
	}
	relayKey := strings.TrimSpace(string(b))
	b, e = os.ReadFile(cfg.APIKeyFile)
	if e != nil {
		return errors.New("api_key_unreadable")
	}
	apiKey := strings.TrimSpace(string(b))
	if len(relayKey) < 32 || len(apiKey) < 32 {
		return errors.New("invalid_service_keys")
	}
	if e = os.MkdirAll(cfg.WorkRoot, 0700); e != nil {
		return e
	}
	if cfg.ThreadsFile == "" {
		cfg.ThreadsFile = filepath.Join(filepath.Dir(cfg.ModelsFile), "conversations.json")
	}
	threads, e := conversations.OpenThreads(cfg.ThreadsFile)
	if e != nil {
		return e
	}
	fmt.Printf("{\"type\":\"worker_ready\",\"models\":%d,\"max_concurrent_tasks\":%d}\n", len(catalog.Models), cfg.MaxConcurrentTasks)
	go trainingManager(ctx, cfg, relayKey)
	go replayOutbox(ctx, cfg, relayKey)
	return runWorkerPool(ctx, cfg.MaxConcurrentTasks, func(loopCtx context.Context) error {
		return workerLoop(loopCtx, cfg, catalog, threads, relayKey, apiKey)
	})
}

// All slots share one synchronized native-thread store. Task state, clients,
// heartbeats, questions and progress remain local to the execution slot.
func workerLoop(ctx context.Context, cfg config, catalog models.Catalog, threads *conversations.Threads, relayKey, apiKey string) error {
	httpClient := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	call := func(endpoint string, body any, out any) (int, error) {
		data, e := json.Marshal(body)
		if e != nil {
			return 0, e
		}
		req, e := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(cfg.RelayURL, "/")+endpoint, bytes.NewReader(data))
		if e != nil {
			return 0, e
		}
		req.Header.Set("Authorization", "Bearer "+relayKey)
		req.Header.Set("Content-Type", "application/json")
		res, e := httpClient.Do(req)
		if e != nil {
			return 0, errors.New("relay_network_error")
		}
		defer res.Body.Close()
		if res.StatusCode == 204 {
			return 204, nil
		}
		if res.StatusCode != 200 {
			io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
			return res.StatusCode, errors.New("relay_request_rejected")
		}
		if out != nil {
			if e = json.NewDecoder(io.LimitReader(res.Body, 128*1024)).Decode(out); e != nil {
				return res.StatusCode, errors.New("invalid_relay_response")
			}
		}
		return res.StatusCode, nil
	}
	var finishLease context.CancelFunc
	defer func() {
		if finishLease != nil {
			finishLease()
		}
	}()
	for {
		if ctx.Err() != nil {
			return nil
		}
		if finishLease != nil {
			finishLease()
			finishLease = nil
		}
		var task jobs.Task
		status, e := call("/jobs/claim", nil, &task)
		if ctx.Err() != nil {
			return nil
		}
		if e != nil || status == 204 {
			if !pause(ctx, 2*time.Second) {
				return nil
			}
			continue
		}
		if len(task.ID) != 24 || len(task.Input) > 8192 {
			return errors.New("unsupported_worker_task")
		}
		if _, e = hex.DecodeString(task.ID); e != nil {
			return errors.New("invalid_task_id")
		}
		if task.Effort == "" {
			task.Effort = "high"
		}
		if _, selectionErr := catalog.Resolve(task.Model, task.Effort); selectionErr != nil {
			_, _ = call("/jobs/result", jobs.Completion{ID: task.ID, Lease: task.Lease, Error: "model_not_available"}, nil)
			continue
		}
		if task.ConversationID != "" && !conversations.ValidID(task.ConversationID) {
			return errors.New("invalid_worker_conversation")
		}
		if exists, err := cachedOutbox(cfg, task.ID, task.Lease); err != nil {
			return err
		} else if exists {
			continue
		}
		if task.ConversationID != "" {
			if cached, ok := threads.Cached(task.ConversationID, task.ID); ok {
				completion := jobs.Completion{ID: task.ID, Lease: task.Lease, Usage: cached.Usage, Result: cached.Text, Error: cached.Error, ToolCount: cached.ToolCount, Outputs: cached.Outputs, SteerReceipts: cached.SteerReceipts}
				for attempt := 0; attempt < 4; attempt++ {
					status, e = call("/jobs/result", completion, nil)
					if e == nil || status == 409 {
						break
					}
					if !pause(ctx, 2*time.Second) {
						return nil
					}
				}
				fmt.Printf("{\"type\":\"task_reply_replayed\",\"id\":%q,\"result_uploaded\":%t}\n", task.ID, e == nil)
				continue
			}
		}
		if len(task.Attachments) > 4 {
			return errors.New("unsupported_worker_attachments")
		}
		for _, ref := range task.Attachments {
			if !files.ValidRef(ref) {
				return errors.New("invalid_worker_attachment")
			}
		}
		taskCtx, finish := context.WithCancel(ctx)
		finishLease = finish
		go watchCancellation(taskCtx, finish, func() (bool, error) {
			var c struct {
				Cancel bool `json:"cancel"`
			}
			_, e := call("/jobs/control", map[string]string{"id": task.ID, "lease": task.Lease}, &c)
			return c.Cancel, e
		})
		go maintainLease(taskCtx, finish, func() (int, error) {
			return call("/jobs/heartbeat", map[string]string{"id": task.ID, "lease": task.Lease}, nil)
		})
		dir := filepath.Join(cfg.WorkRoot, task.ID)
		conversationDir := dir
		if task.ConversationID != "" {
			conversationDir = filepath.Join(cfg.WorkRoot, "sessions", task.ConversationID)
			if task.Project != "" {
				if !conversations.ValidProject(task.Project) {
					return errors.New("invalid_project")
				}
				conversationDir = filepath.Join(cfg.WorkRoot, "projects", filepath.FromSlash(task.Project))
			}
			dir = filepath.Join(conversationDir, "turns", task.ID)
		}
		if e = os.MkdirAll(dir, 0700); e != nil {
			return e
		}
		if e = rememberTraining(cfg, task, dir); e != nil {
			return e
		}
		fixture := filepath.Join(dir, "fixture.txt")
		if _, e = os.Stat(fixture); os.IsNotExist(e) {
			var nonce [12]byte
			if _, e = rand.Read(nonce[:]); e != nil {
				return e
			}
			host, _ := os.Hostname()
			content := "校园服务器：" + host + "\n执行凭证：CAMPUS_TOOL_" + hex.EncodeToString(nonce[:]) + "\n测试算式：17 × 23\n"
			if e = os.WriteFile(fixture, []byte(content), 0600); e != nil {
				return e
			}
		}
		fmt.Printf("{\"type\":\"task_started\",\"id\":%q,\"model\":%q,\"effort\":%q}\n", task.ID, task.Model, task.Effort)
		var inputs []map[string]string
		var inputErr error
		for _, ref := range task.Attachments {
			path, err := downloadAttachment(taskCtx, cfg.RelayURL, relayKey, task.ID, task.Lease, dir, ref)
			if err != nil {
				inputErr = err
				break
			}
			if task.ConversationID != "" {
				path = filepath.ToSlash(filepath.Join("turns", task.ID, path))
			}
			inputs = append(inputs, map[string]string{"name": ref.Name, "path": path})
		}
		if inputErr != nil {
			if ctx.Err() != nil {
				return nil
			}
			completion := jobs.Completion{ID: task.ID, Lease: task.Lease, Error: attachmentError(inputErr)}
			if task.ConversationID != "" {
				if e = threads.Save(conversations.Turn{ConversationID: task.ConversationID, JobID: task.ID, ThreadID: threads.Thread(task.ConversationID), Error: completion.Error}); e != nil {
					return e
				}
			}
			for attempt := 0; attempt < 4; attempt++ {
				status, e = call("/jobs/result", completion, nil)
				if e == nil || status == 409 {
					break
				}
				if !pause(ctx, 2*time.Second) {
					return nil
				}
			}
			fmt.Printf("{\"type\":\"task_attachment_failed\",\"id\":%q,\"category\":%q,\"result_uploaded\":%t}\n", task.ID, completion.Error, e == nil)
			continue
		}
		if len(inputs) > 0 {
			fmt.Printf("{\"type\":\"task_inputs_ready\",\"id\":%q,\"files\":%d}\n", task.ID, len(inputs))
		}
		runCtx, cancel := context.WithTimeout(taskCtx, time.Duration(cfg.TurnTimeoutSeconds)*time.Second)
		prompt := buildTaskPrompt(task, cfg.Permissions, cfg.PythonBinary, inputs)
		nativeThread := threads.Thread(task.ConversationID)
		var steering codex.Steering
		if cfg.LiveSteering {
			steering = &relaySteering{url: cfg.RelayURL, key: relayKey, task: task}
		}
		workerBinary, executableErr := os.Executable()
		if executableErr != nil {
			cancel()
			return errors.New("worker_executable_unavailable")
		}
		questions := &userinput.Connection{URL: cfg.RelayURL, Key: relayKey, JobID: task.ID, Lease: task.Lease, Executable: workerBinary}
		progress := newRelayProgress(taskCtx, cfg.RelayURL, relayKey, task)
		result, runErr := codex.Run(runCtx, codex.Config{Binary: cfg.CodexBinary, Home: cfg.CodexHome, Directory: conversationDir, Key: apiKey, Model: task.Model, Effort: task.Effort, Persistent: task.ConversationID != "", ThreadID: nativeThread, Permissions: cfg.Permissions, AppServer: cfg.LiveSteering, Steering: steering, Questions: questions, QuestionMCP: questions, UsageBaseline: threads.Usage(task.ConversationID), Progress: progress.Publish}, prompt)
		progress.Close()
		cancel()
		if ctx.Err() != nil {
			return nil
		}
		completion := jobs.Completion{Usage: result.Usage, ID: task.ID, Lease: task.Lease, Result: result.Text, ToolCount: result.ToolCount, SteerReceipts: result.SteerReceipts}
		if runErr != nil {
			completion.Result = ""
			completion.Error = runErr.Error()
			if len(completion.Error) > 80 || strings.ContainsAny(completion.Error, " \n") {
				completion.Error = "codex_turn_failed"
			}
			if errors.Is(runErr, context.DeadlineExceeded) {
				completion.Error = "codex_timeout"
			}
			if nativeThread != "" && result.ThreadID == "" && !errors.Is(runErr, context.DeadlineExceeded) {
				completion.Error = "codex_session_unavailable"
			}
		}
		if taskCtx.Err() != nil && runErr != nil {
			completion.Error = "context_canceled"
		}
		outbox, outboxErr := queueResult(cfg, task, completion, dir)
		if outboxErr != nil {
			completion.Error = outboxErr.Error()
			completion.Result = ""
			outbox, outboxErr = queueResult(cfg, task, completion, dir)
		}
		if outboxErr != nil {
			return outboxErr
		}
		completion = outbox.Completion

		if task.ConversationID != "" {
			threadID := result.ThreadID
			if threadID == "" {
				threadID = nativeThread
			}
			if e = threads.Save(conversations.Turn{ConversationID: task.ConversationID, JobID: task.ID, ThreadID: threadID, Usage: completion.Usage, Cumulative: result.Cumulative, Text: completion.Result, Error: completion.Error, ToolCount: completion.ToolCount, Outputs: completion.Outputs, SteerReceipts: completion.SteerReceipts}); e != nil {
				return e
			}
		}
		e = acceptResult(ctx, cfg, relayKey, outbox)

		fmt.Printf("{\"type\":\"task_finished\",\"id\":%q,\"tools\":%d,\"failed\":%t,\"result_uploaded\":%t}\n", task.ID, result.ToolCount, runErr != nil, e == nil)
		if !pause(ctx, time.Second) {
			return nil
		}
	}
}
