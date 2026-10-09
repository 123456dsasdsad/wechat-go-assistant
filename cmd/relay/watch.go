package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/watches"
)

func (in *inbound) watchLink(v watches.Watch) string {
	if in.outputs == nil {
		return ""
	}
	link, _ := in.outputs.TaskLink(in.publicURL, v.Owner, v.ID)
	return strings.Replace(link, "/task/", "/watch/", 1)
}
func (in *inbound) watchText(v watches.Watch) string {
	session, _ := in.sessions.Get(v.Conversation)
	summary := v.Summary(time.Now())
	runes := []rune(summary)
	if len(runes) > 1600 {
		summary = string(runes[:1600]) + "\n…完整进度见页面。"
	}
	return "会话 " + strconv.Itoa(session.Number) + " · " + session.Title + "\n" + summary + "\n\n" + in.watchLink(v) + "\n页面自动刷新。远程训练是否结束以最新进度说明为准。"
}
func (in *inbound) watchUpdate(key string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+key {
			http.Error(w, "unauthorized", 401)
			return
		}
		if r.Method != "POST" || in.watches == nil {
			http.Error(w, "method not allowed", 405)
			return
		}
		var u watches.Update
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32768)).Decode(&u) != nil {
			http.Error(w, "invalid watch", 400)
			return
		}
		if _, ok := in.sessions.Get(u.Conversation); !ok {
			http.Error(w, "conversation unavailable", 400)
			return
		}
		v, changed, e := in.watches.Put(in.owner, u, time.Now())
		if e != nil {
			http.Error(w, e.Error(), 409)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": v.ID, "changed": changed})
	})
}
func (in *inbound) watchHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'")
		id := strings.TrimPrefix(r.URL.Path, "/wechat-files/watch/")
		v, ok := in.watches.Find(id)
		if r.Method != "GET" || !ok || in.outputs == nil || !in.outputs.AuthorizeTask(v.Owner, id, r.URL.Query().Get("e"), r.URL.Query().Get("token")) {
			http.Error(w, "链接已过期或未授权，请在进度会话发送“任务状态”获取新链接。", 401)
			return
		}
		if r.URL.Query().Get("format") == "json" {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			json.NewEncoder(w).Encode(map[string]any{"watch": v, "status": v.Status(time.Now()), "stale": time.Since(v.Synced) > 90*time.Second})
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, watchPage)
	})
}

const watchPage = `<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Codex 任务进度</title><style>body{margin:0;background:#eef4f8;color:#253549;font:16px/1.7 system-ui,sans-serif}main{max-width:850px;margin:auto;padding:24px 18px}h1{font-size:24px}article{background:white;border-radius:10px;padding:18px;margin:16px 0}pre{white-space:pre-wrap;overflow-wrap:anywhere;font:inherit}small{color:#53697b}#status{padding:12px;background:#e4f2f3;border-radius:8px}.stale{background:#fff1d5!important}</style><main><h1 id="title">Codex 任务进度</h1><p id="status" role="status">正在读取…</p><small id="sync"></small><article><h2>最新进度</h2><pre id="latest"></pre></article><h2>进度记录</h2><section id="events"></section><small id="limitations">每5秒刷新。本页显示原 Codex 会话公开输出的进度；原会话停止或结束后，独立的远程训练可能仍在运行。同步依赖运行 Codex 的电脑，离线时会保留最后记录并标注中断。链接24小时有效，可在微信重新获取。</small></main><script>const $=s=>document.querySelector(s);async function refresh(){try{let u=new URL(location.href);u.searchParams.set('format','json');let r=await fetch(u,{cache:'no-store'});if(!r.ok){$('#status').textContent='链接已过期，请在微信进度会话发送“任务状态”。';return}let d=await r.json(),v=d.watch;$('#title').textContent=v.title;$('#status').textContent=d.status;$('#status').classList.toggle('stale',d.stale);$('#sync').textContent='进度更新：'+new Date(v.observed).toLocaleString()+' / 同步时间：'+new Date(v.synced).toLocaleString();$('#latest').textContent=v.text;$('#limitations').textContent=v.source==='training'?'每5秒刷新。训练进度由校园服务器每30秒同步，无需本机电脑保持开机。调度时间与各实验的训练记录更新时间会分别显示。链接24小时有效，可在微信重新获取。':'每5秒刷新。显示原 Codex 会话公开输出的进度；独立远程训练可能仍在运行。同步依赖本机电脑，离线时标注中断。链接24小时有效。';let box=$('#events');box.replaceChildren();for(let e of [...v.events].reverse()){let a=document.createElement('article'),s=document.createElement('small'),p=document.createElement('pre');s.textContent=new Date(e.at).toLocaleString();p.textContent=e.text;a.append(s,p);box.append(a)}}catch(e){$('#status').textContent='网络暂时断开，正在重试…'}setTimeout(refresh,5000)}refresh();</script></html>`
