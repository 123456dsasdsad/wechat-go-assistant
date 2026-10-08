package main

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/conversations"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/library"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/models"
	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

//go:embed workspace.html
var workspacePage string

type workspaceRequest struct {
	Action       string                `json:"action"`
	Source       string                `json:"source"`
	Token        string                `json:"token"`
	ID           string                `json:"id"`
	Conversation string                `json:"conversation"`
	Input        string                `json:"input"`
	Name         string                `json:"name"`
	Body         string                `json:"body"`
	Model        string                `json:"model"`
	Effort       string                `json:"effort"`
	Profile      conversations.Profile `json:"profile"`
	Pinned       bool                  `json:"pinned"`
	Archived     bool                  `json:"archived"`
	Files        []string              `json:"files"`
	Size         int64                 `json:"size"`
	Batch        string                `json:"batch"`
	Question     string                `json:"question"`
	Library      *library.Request      `json:"library,omitempty"`
}

func randomSource() string {
	var b [12]byte
	if _, e := rand.Read(b[:]); e != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}
func (in *inbound) workspaceHandler(statePath string) http.Handler {
	u, _ := url.Parse(in.publicURL)
	origin := u.Scheme + "://" + u.Host
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; img-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		if r.Method == "GET" && r.URL.Path == "/wechat-files/manage/" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			io.WriteString(w, workspacePage)
			return
		}
		if r.Method != "GET" && (r.Method != "POST" || r.Header.Get("Origin") != origin) {
			http.Error(w, "请求来源不正确。", 403)
			return
		}
		var b workspaceRequest
		if r.Method == "POST" && r.URL.Path == "/wechat-files/manage/api" {
			d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 96<<10))
			d.DisallowUnknownFields()
			if d.Decode(&b) != nil {
				http.Error(w, "请求格式无效。", 400)
				return
			}
		}
		if b.Action == "login" {
			owner, ok := in.files.Authorize(b.Token)
			if !ok || owner != in.owner {
				http.Error(w, "验证链接已过期。请在微信发送“管理页面”。", 401)
				return
			}
			device, e := in.files.Grant(owner, "device:"+randomSource(), 30*24*time.Hour)
			if e != nil {
				http.Error(w, "登录失败。", 503)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "wechat_manage", Value: device, Path: "/wechat-files/manage/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 30 * 86400})
			json.NewEncoder(w).Encode(map[string]bool{"ok": true})
			return
		}
		cookie, e := r.Cookie("wechat_manage")
		if e != nil {
			http.Error(w, "请在微信发送“管理页面”，从验证链接首次进入。", 401)
			return
		}
		owner, ok := in.files.Authorize(cookie.Value)
		if !ok || owner != in.owner {
			http.Error(w, "登录已过期。请在微信重新获取管理链接。", 401)
			return
		}
		if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/wechat-files/manage/file/") {
			ref, e := in.files.Get(owner, strings.TrimPrefix(r.URL.Path, "/wechat-files/manage/file/"))
			if e != nil {
				http.NotFound(w, r)
				return
			}
			f, e := in.files.OpenBlob(ref)
			if e != nil {
				http.NotFound(w, r)
				return
			}
			defer f.Close()
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename*=UTF-8''%s", url.PathEscape(ref.Name)))
			http.ServeContent(w, r, ref.Name, time.Time{}, f)
			return
		}
		if r.Method == "POST" && r.URL.Path == "/wechat-files/manage/chunk" {
			offset, e := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
			if e != nil {
				http.Error(w, "上传位置无效。", 400)
				return
			}
			v, e := in.files.AppendUpload(owner, r.URL.Query().Get("id"), offset, http.MaxBytesReader(w, r.Body, files.ChunkBytes+1))
			if e != nil {
				http.Error(w, e.Error(), 409)
				return
			}
			json.NewEncoder(w).Encode(v)
			return
		}
		if r.URL.Path != "/wechat-files/manage/api" {
			if r.Method == "GET" && r.URL.Path == "/wechat-files/manage/library-blob" {
				if in.library == nil {
					http.NotFound(w, r)
					return
				}
				id, e := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
				if e != nil {
					http.NotFound(w, r)
					return
				}
				var m library.Material
				if e = in.library.Call(r.Context(), library.Request{Owner: owner, Action: "get", MaterialID: id}, &m); e != nil {
					http.NotFound(w, r)
					return
				}
				sha := r.URL.Query().Get("sha")
				name := ""
				for _, a := range m.Assets {
					if a.SHA256 == sha {
						name = a.Name
					}
				}
				if name == "" {
					http.NotFound(w, r)
					return
				}
				src, e := in.library.Download(r.Context(), owner, sha)
				if e != nil {
					http.NotFound(w, r)
					return
				}
				defer src.Close()
				w.Header().Set("Content-Type", "application/octet-stream")
				w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(name))
				io.Copy(w, src)
				return
			}
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if r.Method == "GET" {
			data, e := in.workspaceSnapshot(owner, r.URL.Query().Get("q"), r.URL.Query().Get("archived") == "1")
			if e != nil {
				http.Error(w, "数据读取失败，请稍后重试。", 503)
				return
			}
			json.NewEncoder(w).Encode(data)
			return
		}
		if b.Action == "logout" {
			in.files.Revoke(cookie.Value)
			http.SetCookie(w, &http.Cookie{Name: "wechat_manage", Value: "", Path: "/wechat-files/manage/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
			json.NewEncoder(w).Encode(map[string]bool{"ok": true})
			return
		}
		if len(b.Source) != 24 {
			http.Error(w, "缺少操作凭证，请刷新页面。", 400)
			return
		}
		if _, e = hex.DecodeString(b.Source); e != nil {
			http.Error(w, "操作凭证无效。", 400)
			return
		}
		result, e := in.workspaceAction(owner, b, statePath)
		if e != nil {
			http.Error(w, workspaceError(e), 409)
			return
		}
		json.NewEncoder(w).Encode(result)
	})
}
func (in *inbound) workspaceSnapshot(owner, query string, archived bool) (map[string]any, error) {
	if e := in.queue.Health(); e != nil {
		return nil, e
	}
	rows := in.queue.Recent(owner, 40)
	if query != "" {
		rows = in.queue.Search(owner, query, 40)
	}
	tasks := []map[string]any{}
	for i := len(rows) - 1; i >= 0; i-- {
		j := rows[i]
		link := ""
		if in.outputs != nil {
			link, _ = in.outputs.TaskLink(in.publicURL, owner, j.ID)
		}
		cost, priced := jobs.Cost(j)
		qs := []map[string]any{}
		for _, q := range j.Questions {
			if q.State == "pending" && q.Attempt == j.Attempts {
				for _, item := range q.Request.Questions {
					if _, answered := q.Answers[item.ID]; !answered {
						qs = append(qs, map[string]any{"batch": q.ID, "question": item})
					}
				}
			}
		}
		tasks = append(tasks, map[string]any{"id": j.ID, "conversation": j.ConversationID, "input": shortPreview(j.Input, 180), "status": taskStatusLabel(j), "state": j.Status, "created": j.Created, "error": j.Error, "progress": tailText(j.Progress, 12000), "result": shortPreview(j.Result, 2000), "model": j.Model, "effort": j.Effort, "files": j.Outputs, "questions": qs, "link": link, "usage": j.Usage, "usd": cost, "priced": priced, "cancelling": j.CancelRequested, "budget_reached": j.BudgetReached, "budget_usd": j.BudgetUSD, "training": j.Training})
	}
	current := in.sessions.Current()
	total, byConversation, e := in.queue.UsageByConversation(owner)
	if e != nil {
		return nil, e
	}
	sessionRows := []map[string]any{}
	for _, v := range in.sessions.List(query, archived) {
		u := byConversation[v.ID]
		sessionRows = append(sessionRows, map[string]any{"session": v, "choice": in.preferences.Current(v.ID), "usage": u})
	}
	data := map[string]any{"tasks": tasks, "sessions": sessionRows, "current": current.ID, "files": in.files.List(owner), "models": in.preferences.Catalog().Models, "usage": total, "templates": in.templates.List()}
	if in.reports != nil {
		data["accounts"] = in.reports.Latest("accounts")
		data["updates"] = in.reports.Latest("updates")
	}
	return data, nil
}
func (in *inbound) workspaceAction(owner string, b workspaceRequest, statePath string) (any, error) {
	reply := ""
	if statePath != "" {
		s, e := weixin.LoadState(statePath)
		if e != nil {
			return nil, errors.New("reply_context_unavailable")
		}
		reply = s.Contexts[owner]
	}
	switch b.Action {
	case "library":
		if b.Library == nil || in.library == nil {
			return nil, errors.New("library_unavailable")
		}
		q := *b.Library
		q.Owner = owner
		allowed := map[string]bool{"search": true, "topics": true, "get": true, "review": true, "snapshot": true, "move": true, "tags": true, "trash": true, "restore": true, "notes": true, "lock": true, "import": true, "restore_review": true}
		if !allowed[q.Action] {
			return nil, errors.New("unsupported_library_action")
		}
		var result any
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if e := in.library.Call(ctx, q, &result); e != nil {
			return nil, e
		}
		if q.Action == "move" || q.Action == "trash" || q.Action == "restore" || q.Action == "notes" || q.Action == "tags" || q.Action == "lock" || q.Action == "import" {
			if _, e := in.queueLibrary(owner, reply, "portal:"+b.Source, "review_update", "*", "资料修改后更新所有受影响综述"); e != nil {
				return nil, e
			}
		}
		return result, nil
	case "library_research":
		if in.libraryDrafts == nil {
			return nil, errors.New("library_unavailable")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cid := b.Conversation
		if cid == "" {
			cid = in.sessions.Current().ID
		}
		draft, e := in.libraryDrafts.Begin(owner, "portal:"+b.Source, cid, b.Name)
		if e != nil {
			return nil, e
		}
		var assets []library.Asset
		for _, id := range b.Files {
			ref, e := in.files.Get(owner, id)
			if e != nil {
				return nil, e
			}
			f, e := in.files.OpenBlob(ref)
			if e != nil {
				return nil, e
			}
			a := library.Asset{SHA256: ref.SHA256, Size: ref.Size, Name: ref.Name}
			e = in.libraryDrafts.PutBlob(a, f)
			f.Close()
			if e != nil {
				return nil, e
			}
			assets = append(assets, a)
		}
		if e = in.libraryDrafts.Append(owner, draft.ID, b.Source, b.Input, assets); e != nil {
			return nil, e
		}
		if e = in.libraryDrafts.State(owner, draft.ID, "syncing"); e != nil {
			return nil, e
		}
		j, e := in.syncLibrary(ctx, draft, reply)
		if e != nil {
			return map[string]any{"ok": true, "pending": true, "intake": draft.ID}, nil
		}
		return map[string]any{"ok": true, "id": j.ID}, nil
	case "library_update":
		j, e := in.queueLibrary(owner, reply, "portal:"+b.Source, "review_update", b.Name, "更新类别综述")
		return map[string]any{"id": j.ID, "ok": e == nil}, e
	case "task":
		refs := []files.Ref{}
		if len(b.Files) > 4 {
			return nil, errors.New("too_many_files")
		}
		for _, id := range b.Files {
			ref, e := in.files.Get(owner, id)
			if e != nil {
				return nil, e
			}
			refs = append(refs, ref)
		}
		j, e := in.enqueue("portal:"+b.Source, b.Input, owner, reply, b.Conversation, refs)
		return map[string]any{"ok": e == nil, "id": j.ID}, e
	case "cancel", "retry", "resend":
		j, e := in.queue.Find(owner, b.ID)
		if e != nil {
			return nil, e
		}
		switch b.Action {
		case "cancel":
			j, e = in.queue.Cancel(owner, j.ID)
		case "retry":
			j, e = in.queue.Retry(owner, j.ID, "portal:"+b.Source, reply)
		case "resend":
			j, e = in.queue.RequestMedia(owner, j.ID, reply)
		}
		return map[string]any{"ok": e == nil, "id": j.ID}, e
	case "conversation":
		handled, text, e := in.sessions.Handle("portal:"+b.Source, b.Input)
		if !handled {
			return nil, errors.New("invalid_conversation_command")
		}
		return map[string]any{"ok": e == nil, "message": text}, e
	case "mark":
		e := in.sessions.Mark(b.Conversation, b.Pinned, b.Archived)
		return map[string]bool{"ok": e == nil}, e
	case "profile":
		if _, ok := in.sessions.Get(b.Conversation); !ok {
			return nil, errors.New("conversation_unavailable")
		}
		if _, e := in.preferences.Catalog().Resolve(b.Model, b.Effort); e != nil {
			return nil, e
		}
		if !conversations.ValidProject(b.Profile.Project) {
			return nil, errors.New("invalid_profile")
		}
		if e := in.sessions.UpdateProfile(b.Conversation, b.Profile); e != nil {
			return nil, e
		}
		_, _, e := in.preferences.Handle(b.Source+":model", "默认模型 "+b.Model, b.Conversation)
		if e == nil {
			_, _, e = in.preferences.Handle(b.Source+":effort", "推理强度 "+b.Effort, b.Conversation)
		}
		return map[string]bool{"ok": e == nil}, e
	case "template_save":
		e := in.templates.Save(templateValue(b.Name, b.Body))
		return map[string]bool{"ok": e == nil}, e
	case "template_delete":
		e := in.templates.Delete(b.Name)
		return map[string]bool{"ok": e == nil}, e
	case "template_run":
		input, e := in.templates.Expand(b.Name, b.Input)
		if e != nil {
			return nil, e
		}
		b.Action = "task"
		b.Input = input
		return in.workspaceAction(owner, b, statePath)
	case "upload_begin":
		return in.files.BeginUpload(owner, b.Source, b.Name, b.Size)
	case "upload_finish":
		return in.files.FinishUpload(owner, b.ID)
	case "file_delete":
		for _, j := range in.queue.Active() {
			for _, f := range j.Attachments {
				if f.ID == b.ID {
					return nil, errors.New("file_in_active_task")
				}
			}
		}
		e := in.files.Delete(owner, b.ID)
		return map[string]bool{"ok": e == nil}, e
	case "answer":
		ok, e := in.queue.AnswerQuestion(owner, b.ID, b.Batch, b.Question, "portal:"+b.Source, b.Input, time.Now())
		return map[string]bool{"ok": ok}, e
	case "accounts":
		if in.accounts == nil {
			return nil, errors.New("accounts_unavailable")
		}
		token, e := in.accounts.Grant(owner, "portal:"+b.Source)
		return map[string]string{"url": in.publicURL + "accounts/#" + token}, e
	case "training_stop", "training_resume":
		j, e := in.queue.Find(owner, b.ID)
		if e != nil {
			return nil, e
		}
		e = in.queue.RequestTraining(owner, j.ID, strings.TrimPrefix(b.Action, "training_"))
		return map[string]bool{"ok": e == nil}, e
	}
	return nil, errors.New("unknown_action")
}
func workspaceError(e error) string {
	messages := map[string]string{"file_in_active_task": "文件正用于排队或运行的任务，请结束任务后删除。", "invalid_profile": "项目目录请使用相对路径，预算需为非负数。", "reply_context_unavailable": "微信回复凭证暂不可用，请先在微信发送一条消息。", "switch_conversation_before_archiving_current": "请先切换到其他会话，再归档当前会话。", "only_failed_tasks_can_retry": "只有失败或已停止的任务可重试。", "file_storage_full": "服务器磁盘空间不足，请先清理文件。"}
	if v := messages[e.Error()]; v != "" {
		return v
	}
	return e.Error()
}
func tailText(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return "…\n" + string(r[len(r)-n:])
	}
	return s
}

var _ = models.Choice{}
