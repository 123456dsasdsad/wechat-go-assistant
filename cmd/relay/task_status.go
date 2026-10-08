package main

import (
	"encoding/json"
	"fmt"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"io"
	"net/http"
	"strings"
	"time"
)

func taskStatusLabel(j jobs.Job) string {
	if j.Training.State == "running" {
		return "训练运行中（校园进程独立执行）"
	}
	if j.CancelRequested && j.Status == "running" {
		return "停止中，等待校园端确认"
	}
	if j.Error == "user_canceled" {
		return "已停止"
	}
	if j.OutputPending {
		return "AI已完成，附件回传中（自动重试）"
	}
	if j.WaitingForUser() {
		return "等待你回答问题（请引用微信问题回复）"
	}
	if j.Status == "done" && j.MediaDeferred && hasUnsentOutputs(j) {
		return "已完成，附件发送已暂停；需明确回传"
	}
	switch j.Status {
	case "queued":
		return "排队中"
	case "running":
		return "校园服务器执行中"
	case "done":
		return "处理已完成，微信发送中"
	case "delivered":
		if j.PackageDelivered() {
			return fmt.Sprintf("完整结果包已被微信接口接受（包含 %d 个原件）", len(j.Outputs))
		}
		return "处理已完成，微信接口已接收"
	}
	return "状态待确认"
}
func taskStatusText(store *jobs.Store, outputs *files.Store, origin, owner string) string {
	var b strings.Builder
	b.WriteString("最近任务：\n")
	history := store.Recent(owner, 5)
	count := 0
	for i := len(history) - 1; i >= 0 && count < 5; i-- {
		j := history[i]
		if j.Owner != owner || j.VerificationOnly {
			continue
		}
		count++
		label := taskStatusLabel(j)
		if store.Superseded(j) && !j.MediaDeferred && hasUnsentOutputs(j) {
			label = "已完成，旧版附件保留在任务页"
		}
		fmt.Fprintf(&b, "%s\n%s · 已过 %.0f 分钟\n", questionIdentity(j), label, time.Since(j.Created).Minutes())
		if j.MediaDeferred && hasUnsentOutputs(j) {
			fmt.Fprintf(&b, "继续附件：回传任务 %s\n", j.ID[:8])
		}
		if j.Error != "" {
			fmt.Fprintf(&b, "执行结果：%s\n", j.Error)
		}
		if outputs != nil && origin != "" {
			if link, e := outputs.TaskLink(origin, owner, j.ID); e == nil {
				b.WriteString(link + "\n")
			}
		}
	}
	if count == 0 {
		b.WriteString("还没有任务。")
	}
	return b.String()
}
func taskStatusHandler(store *jobs.Store, outputs *files.Store, origin string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; img-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
		id := strings.TrimPrefix(r.URL.Path, "/wechat-files/task/")
		j, ok := store.Snapshot(id)
		if r.Method != "GET" || !ok || !outputs.AuthorizeTask(j.Owner, id, r.URL.Query().Get("e"), r.URL.Query().Get("token")) {
			http.Error(w, "任务链接已过期或未授权，请在微信发送“任务状态”获取新链接。", 401)
			return
		}
		if fileID := r.URL.Query().Get("file"); fileID != "" {
			for _, ref := range j.Outputs {
				if ref.ID != fileID || !(strings.HasSuffix(strings.ToLower(ref.Name), ".png") || strings.HasSuffix(strings.ToLower(ref.Name), ".jpg") || strings.HasSuffix(strings.ToLower(ref.Name), ".jpeg")) {
					continue
				}
				f, err := outputs.OpenBlob(ref)
				if err != nil {
					http.NotFound(w, r)
					return
				}
				defer f.Close()
				if strings.HasSuffix(strings.ToLower(ref.Name), ".png") {
					w.Header().Set("Content-Type", "image/png")
				} else {
					w.Header().Set("Content-Type", "image/jpeg")
				}
				http.ServeContent(w, r, ref.Name, j.Created, f)
				return
			}
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("format") == "json" {
			items := []map[string]any{}
			refs := append([]files.Ref(nil), j.Outputs...)
			if j.MediaPackage.ID != "" {
				refs = append([]files.Ref{j.MediaPackage}, refs...)
			}
			for _, ref := range refs {
				link, err := outputs.DownloadLink(origin, j.Owner, ref)
				if err != nil {
					continue
				}
				preview := ""
				if strings.HasSuffix(strings.ToLower(ref.Name), ".png") || strings.HasSuffix(strings.ToLower(ref.Name), ".jpg") || strings.HasSuffix(strings.ToLower(ref.Name), ".jpeg") {
					u := *r.URL
					q := u.Query()
					q.Del("format")
					q.Set("file", ref.ID)
					u.RawQuery = q.Encode()
					preview = u.String()
				}
				items = append(items, map[string]any{"name": ref.Name, "bytes": ref.Size, "url": link, "preview": preview, "package": ref.ID == j.MediaPackage.ID})
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			json.NewEncoder(w).Encode(map[string]any{"pending_questions": pendingQuestionsText(j), "id": j.ID[:8], "question": questionIdentity(j), "state": j.Status, "status": taskStatusLabel(j), "result": j.Result, "progress": j.Progress, "progress_sequence": j.ProgressSequence, "progress_updated": j.ProgressUpdated, "error": j.Error, "model": j.Model, "effort": j.Effort, "files": items, "delivery_parts": len(j.DeliveryParts), "expected_parts": len(j.Outputs) + 1, "original_files": len(j.Outputs), "output_pending": j.OutputPending, "training": j.Training, "usage": j.Usage, "package_pending": len(j.Outputs) > 1 && j.MediaPackage.ID == "" && (j.MediaPackageRequired || !j.MediaDeferred), "package_accepted": j.PackageDelivered(), "media_paused": j.MediaDeferred, "older_revision": store.Superseded(j)})
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, taskStatusPage)
	})
}

const taskStatusPage = `<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>校园助手 · 任务结果</title><style>body{margin:0;background:#f4f6f5;color:#1b3028;font:16px/1.65 system-ui,sans-serif}main{max-width:850px;margin:auto;padding:24px 18px}h1{font-size:25px;margin:0 0 10px}#status{background:#e6f3eb;padding:12px 16px;border-radius:12px}pre{white-space:pre-wrap;overflow-wrap:anywhere;font:inherit;background:white;padding:18px;border-radius:12px}article{background:white;margin:14px 0;padding:16px;border-radius:12px}a{color:#087844;overflow-wrap:anywhere}img{display:block;max-width:100%;height:auto;margin-top:12px}small{color:#63766d}</style><main><h1>任务进度与结果</h1><div id="status">正在读取…</div><small>页面每 3 秒自动更新。任务完成后可直接查看文字、图片和下载原件，无需再发消息。链接 24 小时有效。</small><pre id="question"></pre><h2>待回答问题</h2><pre id="user-questions"></pre><h2>执行过程</h2><small>显示 AI 已输出的进展说明和正在生成的文字；内容可能修订，以最终结果为准。</small><pre id="progress"></pre><h2>最终结果</h2><pre id="result"></pre><section id="files"></section></main><script>let last='';async function refresh(){try{let u=new URL(location.href);u.searchParams.set('format','json');let r=await fetch(u,{cache:'no-store'});if(!r.ok){document.querySelector('#status').textContent='链接已过期，请在微信发送“任务状态”获取新链接。';return}let v=await r.json();document.querySelector('#status').textContent='任务 '+v.id+' · '+v.status+' · '+v.model+' / '+v.effort;document.querySelector('#question').textContent=v.question+(v.older_revision?'\n这是较早版本；附件保留在本页，可在微信发送“回传任务 '+v.id+'”补发尚未发送的附件。':'');document.querySelector('#user-questions').textContent=v.pending_questions||'当前没有待回答问题。';document.querySelector('#progress').textContent=v.progress||(v.state==='queued'?'任务排队中，尚未开始。':v.state==='running'?'任务正在执行，等待 AI 输出进展说明…':'这条任务没有保存过程输出。');document.querySelector('#result').textContent=v.error?('执行结果：'+v.error):v.result;let mark=v.files.map(x=>x.name+':'+x.bytes).join('|');if(mark!==last){last=mark;let box=document.querySelector('#files');box.replaceChildren();for(let f of v.files){let a=document.createElement('a');a.href=f.url;a.textContent=f.name+' · '+(f.bytes/1048576).toFixed(2)+' MiB';let card=document.createElement('article');card.append(a);if(f.preview){let img=document.createElement('img');img.src=f.preview;img.alt=f.name;img.loading='lazy';card.append(img)}box.append(card)}}if(v.state==='queued'||v.state==='running'||v.output_pending||v.training?.state==='running'||v.package_pending){setTimeout(refresh,3000)}}catch(e){document.querySelector('#status').textContent='连接暂时中断，正在重试…';setTimeout(refresh,5000)}}refresh();</script></html>`
