package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/accountupload"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/assistant"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/conversations"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/library"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/maintenance"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/models"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/quotes"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/settings"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/watches"
	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type config struct {
	StatePath           string `json:"state_path"`
	JobsDir             string `json:"jobs_dir"`
	KeyFile             string `json:"key_file"`
	Listen              string `json:"listen"`
	ModelsFile          string `json:"models_file"`
	SettingsFile        string `json:"settings_file"`
	FilesDir            string `json:"files_dir"`
	UploadListen        string `json:"upload_listen"`
	PublicURL           string `json:"public_url"`
	ConversationsFile   string `json:"conversations_file"`
	OutputsDir          string `json:"outputs_dir"`
	MaintenanceDir      string `json:"maintenance_dir,omitempty"`
	CockpitRoot         string `json:"cockpit_root,omitempty"`
	AccountsDir         string `json:"accounts_dir,omitempty"`
	AccountReloadRunner string `json:"account_reload_runner,omitempty"`
	LibraryURL          string `json:"library_url,omitempty"`
	LibraryDraftsDir    string `json:"library_drafts_dir,omitempty"`
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(ctx context.Context) error {
	defer metadb.CloseAll()
	path := flag.String("config", "", "private config path")
	prepareDelivery := flag.String("prepare-delivery", "", "offline: pause old media and prepare a completed task package; Relay must be stopped")
	flag.Parse()
	if *path == "" {
		return errors.New("config_required")
	}
	raw, err := os.ReadFile(*path)
	if err != nil {
		return errors.New("config_unreadable")
	}
	var cfg config
	if json.Unmarshal(raw, &cfg) != nil {
		return errors.New("invalid_config")
	}
	catalog, err := models.Load(cfg.ModelsFile)
	if err != nil {
		return err
	}
	preferences, err := settings.Open(cfg.SettingsFile, catalog)
	if err != nil {
		return err
	}
	host, _, err := net.SplitHostPort(cfg.Listen)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return errors.New("listener_must_be_loopback")
	}
	raw, err = os.ReadFile(cfg.KeyFile)
	if err != nil {
		return errors.New("private_key_unreadable")
	}
	key := strings.TrimSpace(string(raw))
	if len(key) < 32 {
		return errors.New("invalid_private_key")
	}
	if err = os.MkdirAll(filepath.Dir(cfg.StatePath), 0700); err != nil {
		return err
	}
	lock := cfg.StatePath + ".lock"
	f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("wechat_state_is_owned_by_another_process")
	}
	fmt.Fprintln(f, os.Getpid())
	f.Close()
	defer os.Remove(lock)
	state, err := weixin.LoadState(cfg.StatePath)
	if err != nil {
		return errors.New("wechat_authorization_unreadable")
	}
	client, err := weixin.New(weixin.Options{BaseURL: state.Account.BaseURL, Token: state.Account.BotToken})
	if err != nil {
		return err
	}
	store, err := jobs.Open(cfg.JobsDir)
	if err != nil {
		return errors.New("job_store_unreadable")
	}
	if cfg.FilesDir == "" {
		cfg.FilesDir = filepath.Join(filepath.Dir(cfg.JobsDir), "files")
	}
	fileStore, err := files.Open(cfg.FilesDir)
	if err != nil {
		return err
	}
	if cfg.OutputsDir == "" {
		cfg.OutputsDir = filepath.Join(filepath.Dir(cfg.JobsDir), "outputs")
	}
	outputStore, err := files.OpenWithLimit(cfg.OutputsDir, 1024)
	if err != nil {
		return err
	}
	quoteStore, quoteErr := quotes.Open(filepath.Join(filepath.Dir(cfg.JobsDir), "quotes", "index.json"))
	if quoteErr != nil {
		fmt.Println(`{"type":"quote_cache_unavailable"}`)
	}
	if *prepareDelivery != "" {
		j, ok := store.Snapshot(*prepareDelivery)
		if !ok || j.Owner != state.Account.OwnerID || (j.Status != "done" && j.Status != "delivered") {
			return errors.New("package_task_unavailable")
		}
		if err = store.PauseOwnerMedia(j.Owner); err != nil {
			return err
		}
		j, err = ensureResultPackage(store, outputStore, j)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"packagePrepared": true, "job": j.ID, "originalFiles": len(j.Outputs), "packageBytes": j.MediaPackage.Size, "packageSHA256": j.MediaPackage.SHA256, "oldMediaPaused": true})
	}
	if err = outputStore.Prune(); err != nil {
		return errors.New("output_retention_cleanup_failed")
	}
	if err = fileStore.Prune(); err != nil {
		return errors.New("file_retention_cleanup_failed")
	}
	go func() {
		timer := time.NewTicker(time.Hour)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				if fileStore.Prune() != nil || outputStore.Prune() != nil {
					fmt.Println(`{"type":"file_retention_retry"}`)
				}
			}
		}
	}()
	if cfg.ConversationsFile == "" {
		cfg.ConversationsFile = filepath.Join(filepath.Dir(cfg.JobsDir), "conversations.json")
	}
	sessions, err := conversations.Open(cfg.ConversationsFile)
	if err != nil {
		return err
	}
	if err = store.Each(func(job jobs.Job) error {
		if job.Owner == state.Account.OwnerID {
			return recordJob(sessions, job)
		}
		return nil
	}); err != nil {
		return err
	}
	defer store.Close()
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return errors.New("private_listener_unavailable")
	}
	if cfg.MaintenanceDir == "" {
		cfg.MaintenanceDir = filepath.Join(filepath.Dir(cfg.JobsDir), "maintenance-reports")
	}
	reports, err := maintenance.Open(cfg.MaintenanceDir)
	if err != nil {
		return errors.New("maintenance_store_unavailable")
	}
	var accounts *accountupload.Store
	if cfg.CockpitRoot != "" {
		if cfg.AccountReloadRunner == "" || !filepath.IsAbs(cfg.CockpitRoot) || !filepath.IsAbs(cfg.AccountReloadRunner) {
			return errors.New("invalid_account_upload_config")
		}
		if cfg.AccountsDir == "" {
			cfg.AccountsDir = filepath.Join(filepath.Dir(cfg.JobsDir), "account-uploads")
		}
		accounts, err = accountupload.Open(cfg.AccountsDir)
		if err != nil {
			return errors.New("account_upload_store_unavailable")
		}
	}
	outbound := &liveResultSender{client: client, contextFor: latestReplyContext(cfg.StatePath)}
	outbound.remember = quoteRecorder(quoteStore, state.Account.BotID, store)
	outbound.pauseMedia = store.PauseOwnerMedia
	outbound.mediaAllowed = func(r weixin.Reply) bool {
		j, ok := store.Snapshot(outboundJobID(r.ClientID))
		return ok && j.Owner == r.ToUserID && j.Status == "done" && !j.MediaDeferred && j.MediaContext() == r.ContextToken
	}
	assistantStore, err := assistant.Open(filepath.Join(filepath.Dir(cfg.JobsDir), "assistant.sqlite"))
	if err != nil {
		return err
	}
	templates, err := assistant.OpenTemplates(filepath.Join(filepath.Dir(cfg.JobsDir), "templates.json"))
	if err != nil {
		return err
	}
	in := &inbound{templates: templates, assistant: assistantStore, client: outbound, preferences: preferences, queue: store, files: fileStore, outputs: outputStore, publicURL: cfg.PublicURL, owner: state.Account.OwnerID, sessions: sessions, reports: reports, accounts: accounts, quotes: quoteStore, botID: state.Account.BotID}
	in.watches, err = watches.Open(filepath.Join(filepath.Dir(cfg.JobsDir), "external-watches.json"))
	if err != nil {
		return err
	}
	if cfg.LibraryURL != "" {
		in.library, err = library.NewClient(cfg.LibraryURL, key)
		if err != nil {
			return err
		}
		if cfg.LibraryDraftsDir == "" {
			cfg.LibraryDraftsDir = filepath.Join(filepath.Dir(cfg.JobsDir), "library-drafts")
		}
		in.libraryDrafts, err = library.Open(cfg.LibraryDraftsDir)
		if err != nil {
			return err
		}
		defer in.libraryDrafts.Close()
		go in.libraryRetry(ctx, cfg.StatePath)
	}
	privateJobs := jobs.HandlerWithOutputs(store, key, fileStore, outputStore, func() models.Choice { return preferences.Current() })
	privateMaintenance := maintenance.Handler(reports, key)
	leaseHandler := maintenanceLease(store, key, filepath.Dir(cfg.JobsDir))
	privateHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/watch/update" {
			in.watchUpdate(key).ServeHTTP(w, r)
			return
		}
		if r.Method == "POST" && r.URL.Path == "/workspace/grant" {
			if r.Header.Get("Authorization") != "Bearer "+key {
				http.Error(w, "unauthorized", 401)
				return
			}
			token, e := fileStore.Grant(state.Account.OwnerID, "private-workspace:"+randomSource())
			if e != nil {
				http.Error(w, "grant unavailable", 503)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"url": cfg.PublicURL + "manage/#" + token})
			return
		}
		if r.URL.Path == "/maintenance/lease" {
			leaseHandler.ServeHTTP(w, r)
			return
		}
		if r.Method == "GET" && r.URL.Path == "/maintenance/idle" {
			if r.Header.Get("Authorization") != "Bearer "+key {
				http.Error(w, "unauthorized", 401)
				return
			}
			if store.Health() != nil {
				http.Error(w, "storage error", 503)
				return
			}
			idle := true
			for _, j := range store.Active() {
				if j.Status == "running" {
					idle = false
					break
				}
			}
			json.NewEncoder(w).Encode(map[string]bool{"idle": idle})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/maintenance/") {
			privateMaintenance.ServeHTTP(w, r)
		} else {
			privateJobs.ServeHTTP(w, r)
		}
	})
	server := &http.Server{Handler: privateHandler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	serveErrors := make(chan error, 2)
	go func() { serveErrors <- server.Serve(ln) }()
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(shutdown)
	}()
	if cfg.UploadListen != "" || cfg.PublicURL != "" {
		host, _, err := net.SplitHostPort(cfg.UploadListen)
		if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
			return errors.New("upload_listener_must_be_loopback")
		}
		handler, err := files.PublicHandler(fileStore, cfg.PublicURL, func(owner, source string, ref files.Ref, instruction string) (map[string]any, error) {
			if owner != state.Account.OwnerID {
				return nil, errors.New("invalid_upload_owner")
			}
			current, err := weixin.LoadState(cfg.StatePath)
			if err != nil {
				return nil, errors.New("reply_context_unavailable")
			}
			contextToken := current.Contexts[owner]
			if contextToken == "" {
				return nil, errors.New("reply_context_unavailable")
			}
			selected := sessions.Current()
			job, err := in.enqueue(source, instruction, owner, contextToken, selected.ID, []files.Ref{ref})
			if err != nil {
				return nil, err
			}
			if err = recordJob(sessions, job); err != nil {
				return nil, err
			}
			if snapshot, ok := sessions.Get(job.ConversationID); ok {
				selected = snapshot
			}
			fmt.Printf("{\"type\":\"web_file_job_queued\",\"id\":%q,\"model\":%q,\"effort\":%q,\"files\":1}\n", job.ID, job.Model, job.Effort)
			return map[string]any{"ok": true, "job_id": job.ID, "conversation": selected.Number, "model": job.Model, "effort": job.Effort}, nil
		})
		if err != nil {
			return errors.New("invalid_public_upload_url")
		}
		workspaceHandler := in.workspaceHandler(cfg.StatePath)
		publicHandler := handler
		var accountHandler http.Handler
		if accounts != nil {
			accountHandler, err = accountupload.PublicHandler(accounts, cfg.PublicURL)
			if err != nil {
				return err
			}
		}
		statusHandler := taskStatusHandler(store, outputStore, cfg.PublicURL)
		downloadHandler := outputStore.DownloadHandler(cfg.PublicURL)
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/wechat-files/manage/") {
				workspaceHandler.ServeHTTP(w, r)
			} else if strings.HasPrefix(r.URL.Path, "/wechat-files/accounts/") {
				if accountHandler == nil {
					http.NotFound(w, r)
				} else {
					accountHandler.ServeHTTP(w, r)
				}
			} else if strings.HasPrefix(r.URL.Path, "/wechat-files/task/") {
				statusHandler.ServeHTTP(w, r)
			} else if strings.HasPrefix(r.URL.Path, "/wechat-files/watch/") {
				in.watchHandler().ServeHTTP(w, r)
			} else if strings.HasPrefix(r.URL.Path, "/wechat-files/result/") {
				downloadHandler.ServeHTTP(w, r)
			} else {
				publicHandler.ServeHTTP(w, r)
			}
		})
		uploadListener, err := net.Listen("tcp", cfg.UploadListen)
		if err != nil {
			return errors.New("upload_listener_unavailable")
		}
		uploadServer := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16384}
		go func() { serveErrors <- uploadServer.Serve(uploadListener) }()
		defer func() {
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			uploadServer.Shutdown(shutdown)
		}()
	}

	if accounts != nil {
		go activateAccounts(ctx, cfg, key, accounts, reports)
	}
	go runSchedules(ctx, assistantStore, store, sessions, outbound, state.Account.OwnerID)
	go deliverUserQuestions(ctx, outbound, store)
	go deliverMaintenance(ctx, outbound, reports, state.Account.OwnerID)
	go deliver(ctx, outbound, store, sessions, outputStore, cfg.PublicURL)
	choice := preferences.Current()
	fmt.Printf("{\"type\":\"relay_ready\",\"storage\":\"sqlite-v1\",\"model\":%q,\"effort\":%q}\n", choice.Model, choice.Effort)
	for {
		err = client.Drain(ctx, state, in.handle, func(s *weixin.State) error { return weixin.SaveState(cfg.StatePath, s) })
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, weixin.ErrSessionExpired) {
			return weixin.ErrSessionExpired
		}
		if err != nil {
			fmt.Println(`{"type":"wechat_poll_retry"}`)
			if !pause(ctx, 3*time.Second) {
				return nil
			}
		}
		select {
		case e := <-serveErrors:
			if e != nil && !errors.Is(e, http.ErrServerClosed) {
				return errors.New("private_server_stopped")
			}
			return nil
		default:
		}
		if !pause(ctx, 200*time.Millisecond) {
			return nil
		}
	}
}
func safeID(s string) string {
	var out strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			out.WriteRune(r)
		}
	}
	v := out.String()
	if len(v) > 32 {
		v = v[:32]
	}
	return v
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
func deliver(ctx context.Context, client *liveResultSender, store *jobs.Store, sessions *conversations.Store, outputs *files.Store, publicURL string) {
	textDone := make(chan struct{})
	go func() {
		defer close(textDone)
		deliverTexts(ctx, client, store, sessions, outputs, publicURL)
	}()
	defer func() { <-textDone }()
	for pause(ctx, time.Second) {
		for _, j := range selectMediaJobs(store.DeliveryHistory()) {
			if !j.PartDelivered("text") {
				continue
			}
			if inlinePackageLinkAccepted(j, publicURL) {
				store.CommitPart(j.ID, "package-link:"+j.MediaPackage.ID)
				markFullyDelivered(store, j.ID)
				continue
			}
			pending := j
			pending.Outputs = nil
			if len(j.Outputs) > 1 {
				var e error
				j, e = ensureResultPackage(store, outputs, j)
				if e != nil {
					fmt.Println(deliveryRetryLog(j.ID, e))
					continue
				}
				pending = j
				pending.Outputs = []files.Ref{j.MediaPackage}
			}
			// Original downloads precede previews; one item per job keeps the queue fair.
			for _, ref := range j.Outputs {
				if len(pending.Outputs) > 0 {
					break
				}
				if !j.PartDelivered(ref.ID) && ref.Size > directMediaBytes {
					pending.Outputs = append(pending.Outputs, ref)
					break
				}
			}
			if len(pending.Outputs) == 0 {
				for _, ref := range j.Outputs {
					if !j.PartDelivered(ref.ID) {
						pending.Outputs = append(pending.Outputs, ref)
						break
					}
				}
			}
			if err := deliverJob(ctx, client, store, outputs, publicURL, pending, ""); err != nil {
				fmt.Println(deliveryRetryLog(j.ID, err))
				if e := store.DeferMedia(j.ID, j.MediaContext()); e != nil {
					fmt.Println(`{"type":"media_pause_save_failed"}`)
				}
				continue
			}
			markFullyDelivered(store, j.ID)
		}
	}
}
func markFullyDelivered(store *jobs.Store, id string) {
	j, ok := store.Snapshot(id)
	if !ok || j.Status != "done" || !j.PartDelivered("text") || j.OutputPending {
		return
	}
	if hasUnsentOutputs(j) {
		return
	}
	if err := store.Delivered(id); err != nil {
		fmt.Println(`{"type":"delivery_commit_failed"}`)
		return
	}
	fmt.Printf("{\"type\":\"job_delivered\",\"id\":%q,\"tool_count\":%d,\"failed\":%t}\n", id, j.ToolCount, j.Error != "")
}
func deliverTexts(ctx context.Context, client *liveResultSender, store *jobs.Store, sessions *conversations.Store, outputs *files.Store, publicURL string) {
	retries := map[string]deliveryBackoff{}
	packageRetry := map[string]time.Time{}
	for pause(ctx, time.Second) {
		ready := store.Ready()
		focus := focusedMediaJobs(ready)
		for index := len(ready) - 1; index >= 0; index-- {
			j := ready[index]
			if j.VerificationOnly {
				continue
			}
			if id := focus[j.Owner]; id != "" && id != j.ID {
				continue
			}
			if (j.MediaPackageRequired || !j.MediaDeferred) && len(j.Outputs) > 1 && j.MediaPackage.ID == "" && !time.Now().Before(packageRetry[j.ID]) {
				var e error
				j, e = ensureResultPackage(store, outputs, j)
				if e != nil {
					fmt.Println(deliveryRetryLog(j.ID, e))
					store.DeferMedia(j.ID, j.MediaContext())
					packageRetry[j.ID] = time.Now().Add(time.Minute)
				}
			}
			if j.PartDelivered("text") {
				if inlinePackageLinkAccepted(j, publicURL) {
					store.CommitPart(j.ID, "package-link:"+j.MediaPackage.ID)
				}
				markFullyDelivered(store, j.ID)
				continue
			}
			current, _ := client.contextFor(j.Owner)
			if !shouldAttempt(current, retries[j.ID]) {
				continue
			}
			text := resultText(j, sessions, outputs, publicURL)
			j.Outputs = nil
			if err := deliverJob(ctx, client, store, outputs, publicURL, j, text); err != nil {
				fmt.Println(deliveryRetryLog(j.ID, err))
				retries[j.ID] = failedDelivery(current, retries[j.ID], err)
				continue
			}
			delete(retries, j.ID)
			// A large package URL in this accepted notification already delivers the
			// complete package entry; do not require a second proactive message.
			if j.MediaPackage.ID != "" && j.MediaPackage.Size > directMediaBytes {
				if saved, ok := store.Snapshot(j.ID); ok && strings.Contains(saved.DeliveryText, strings.TrimRight(publicURL, "/")+"/result/"+j.MediaPackage.ID+"?") {
					if e := store.CommitPart(j.ID, "package-link:"+j.MediaPackage.ID); e != nil {
						fmt.Println(`{"type":"package_link_commit_failed"}`)
					}
				}
			}
			markFullyDelivered(store, j.ID)
		}
	}
}

func inlinePackageLinkAccepted(j jobs.Job, origin string) bool {
	return j.PartDelivered("text") && j.MediaPackage.ID != "" && j.MediaPackage.Size > directMediaBytes && strings.Contains(j.DeliveryText, strings.TrimRight(origin, "/")+"/result/"+j.MediaPackage.ID+"?")
}

func recordJob(sessions *conversations.Store, job jobs.Job) error {
	var names []string
	for _, ref := range job.Attachments {
		names = append(names, ref.Name)
	}
	return sessions.RecordTask(job.ConversationID, job.Input, names, job.Created)
}

func fileErrorHint(category string) string {
	switch category {
	case "codex_session_unavailable":
		return "原会话暂时无法恢复，上下文没有被自动替换。请稍后重试，或发送“新建会话 名称”另开会话。"
	case "archive_format_unsupported":
		return "压缩包请使用 ZIP；RAR、7z、tar 暂不自动解压。"
	case "invalid_zip", "zip_entry_unreadable":
		return "ZIP 可能已损坏或带有密码。请重新压缩为无密码 ZIP 后上传。"
	case "zip_entry_limit":
		return fmt.Sprintf("ZIP 内文件和目录超过 %d 项，请拆分后重试。这个错误与文件大小、磁盘剩余空间无关。", files.MaxZIPEntries)
	case "zip_expansion_limit":
		return "ZIP 解压后的数据超过校园磁盘可用空间或异常展开保护预算。"
	case "attachment_storage_full":
		return "校园服务器剩余磁盘空间不足，请清理任务副本后重试。"
	case "zip_unsafe_path", "zip_duplicate_path", "zip_unsupported_entry", "zip_path_conflict":
		return "ZIP 包含不安全路径、重复项或链接。请使用普通文件重新压缩。"
	case "attachment_integrity_failed", "attachment_download_failed", "attachment_prepare_failed":
		return "文件传输或校验未完成，请重新上传后发起任务。"
	}
	return ""
}
