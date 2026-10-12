package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/accesskeys"
	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
)

//go:embed access-keys.js
var accessKeysScript string

var accessKeyInMessage = regexp.MustCompile(`wxk_[A-Za-z0-9_-]{43}`)

func wantsAccessKey(input string) bool {
	command := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, strings.Trim(input, " /。！？?!：:"))
	switch command {
	case "key", "长期key", "访问key", "网页key", "获取key", "获取长期key", "获取访问key", "查看key", "查看长期key", "找回key", "重新获取key", "重新获取长期key", "key过期了", "key过期", "key失效":
		return true
	}
	return false
}

// WeChat owner authentication is independent of browser key/session expiry.
// Run before question routing and material capture; never send credentials to AI.
func (in *inbound) accessKeyCommand(ctx context.Context, msg weixin.Message, input string) (bool, error) {
	if !wantsAccessKey(input) {
		return false, nil
	}
	reply := func(text string) (bool, error) { return true, in.reply(ctx, msg, "access-key", text) }
	if msg.FromUserID != in.owner || msg.GroupID != "" || msg.Type != 1 {
		return true, nil
	}
	if in.access == nil {
		return reply("网页访问 key 尚未配置。")
	}
	g, _, e := in.access.EnsurePrimary(in.owner)
	if e != nil {
		return reply("长期 key 暂时无法读取，请稍后再次发送“获取长期key”。")
	}
	raw, e := in.access.Reveal(in.owner, g.ID)
	if e != nil {
		return reply("长期 key 暂时无法读取，请联系管理员恢复。此操作不会创建 AI 任务。")
	}
	return reply("当前长期访问 key：\n\n" + raw + "\n\n长期 key 本身没有自动到期时间；临时 key 或浏览器登录过期后，可用它重新验证。请粘贴到网页验证框，不要转发给他人。\n管理页面：" + in.publicURL + "manage/\n此指令不调用 AI，也不更改当前会话或已有 key。")
}

var accessPage = template.Must(template.New("access").Parse(`<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>校园助手 · 访问验证</title><style>body{margin:0;background:#eef4f8;color:#253549;font:16px/1.7 system-ui,sans-serif}main{max-width:440px;margin:12vh auto;padding:28px;background:white;border:1px solid #d8e3eb;border-radius:12px}h1{font-size:24px}input,button{box-sizing:border-box;width:100%;font:inherit;padding:12px;border-radius:7px;border:1px solid #d8e3eb}button{margin-top:18px;background:#087e8b;color:white;cursor:pointer}input:focus-visible,button:focus-visible{outline:3px solid #087e8b;outline-offset:3px}p{color:#53697b}@media(max-width:520px){main{margin:8vh 18px;padding:22px}}</style><main><h1>校园助手 · 访问验证</h1><p>请输入微信助手发给你的访问 key。临时 key 的范围和有效期由创建者设置。</p><form method="post" action="/wechat-files/access/login"><label for="key">访问 key</label><input id="key" name="key" type="password" autocomplete="off" required maxlength="128"><input type="hidden" name="return" value="{{.Return}}"><button>验证并继续</button></form><p>长期 key 可在管理页重置。忘记 key 或临时 key 过期？在绑定的微信助手会话发送“获取长期key”。验证成功后，当前浏览器最多保持登录 30 天。</p><p id="access-error" role="alert"></p></main><script nonce="{{.Nonce}}">const f=document.querySelector("form"),err=document.querySelector("#access-error"),btn=f.querySelector("button");f.addEventListener("submit",async e=>{e.preventDefault();btn.disabled=true;err.textContent="正在验证…";try{const r=await fetch(f.action,{method:"POST",body:new URLSearchParams(new FormData(f))});if(!r.ok){err.textContent=await r.text();return}f.elements.key.value="";location.replace(r.url)}catch(e){err.textContent="连接暂时中断，请重试。"}finally{btn.disabled=false}});</script></html>`))

func (in *inbound) accessCookies(w http.ResponseWriter, g accesskeys.Grant) error {
	raw, expires, e := in.access.Session(g)
	if e != nil {
		return e
	}
	device, e := in.files.Grant(g.Owner, "access-device:"+randomSource(), time.Duration(expires-time.Now().Unix())*time.Second)
	if e != nil {
		return e
	}
	maxAge := int(expires - time.Now().Unix())
	if maxAge < 1 {
		maxAge = 1
	}
	http.SetCookie(w, &http.Cookie{Name: "wechat_access", Value: raw, Path: "/wechat-files/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: maxAge})
	http.SetCookie(w, &http.Cookie{Name: "wechat_manage", Value: device, Path: "/wechat-files/manage/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: maxAge})
	return nil
}
func readonlyRequest(r *http.Request) bool {
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return false
	}
	if r.Method == "GET" || r.Method == "HEAD" {
		return true
	}
	if r.Method != "POST" || r.URL.Path != "/wechat-files/manage/api" {
		return false
	}
	body, e := io.ReadAll(io.LimitReader(r.Body, (96<<10)+1))
	r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(body))
	if e != nil || len(body) > 96<<10 {
		return false
	}
	var b workspaceRequest
	if json.Unmarshal(body, &b) != nil {
		return false
	}
	if b.Action == "commands" || b.Action == "logout" {
		return true
	}
	if b.Action != "library" || b.Library == nil {
		return false
	}
	return map[string]bool{"map": true, "map_branch": true, "topics": true, "search": true, "get": true, "review": true, "snapshot": true, "site_list": true, "site_check": true}[b.Library.Action]
}
func (in *inbound) accessGate(next http.Handler) http.Handler {
	u, _ := url.Parse(in.publicURL)
	origin := u.Scheme + "://" + u.Host
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if !strings.HasPrefix(r.URL.Path, "/wechat-files/") {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/wechat-files/access/login" {
			if r.Method != "POST" || r.Header.Get("Origin") != origin {
				http.Error(w, "请求来源不正确。", 403)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 4096)
			if r.ParseForm() != nil {
				http.Error(w, "验证请求无效。", 400)
				return
			}
			g, e := in.access.Lookup(strings.TrimSpace(r.Form.Get("key")))
			if e != nil || g.Owner != in.owner {
				http.Error(w, "访问 key 无效、已撤销或已过期。", 401)
				return
			}
			target := r.Form.Get("return")
			dest, e := url.Parse(target)
			if e != nil || dest.IsAbs() || dest.Host != "" || !strings.HasPrefix(target, "/wechat-files/") || !accesskeys.InScope(g, dest.Path) {
				target = g.Scope
			}
			if e = in.accessCookies(w, g); e != nil {
				http.Error(w, "登录暂时不可用。", 503)
				return
			}
			http.Redirect(w, r, target, 303)
			return
		}
		if r.URL.Path == "/wechat-files/access/handoff" && r.Method == "GET" {
			g, e := in.access.ConsumeHandoff(r.URL.Query().Get("code"))
			if e != nil || g.Owner != in.owner {
				http.Error(w, "一次性登录已过期。", 401)
				return
			}
			if e = in.accessCookies(w, g); e != nil {
				http.Error(w, "登录暂时不可用。", 503)
				return
			}
			http.Redirect(w, r, "/wechat-files/manage/", 303)
			return
		}
		cookie, e := r.Cookie("wechat_access")
		var g accesskeys.Grant
		if e == nil {
			g, e = in.access.AuthorizeSession(cookie.Value)
		}
		if e != nil || g.Owner != in.owner {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if (r.Method == "GET" || r.Method == "HEAD") && !strings.Contains(r.Header.Get("Accept"), "application/json") && r.URL.Path != "/wechat-files/manage/api" {
				nonce := randomSource()
				w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+nonce+"'; style-src 'unsafe-inline'; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
				w.Header().Set("Referrer-Policy", "same-origin")
				w.WriteHeader(401)
				// A form at the requested URL preserves its destination without putting the key in any URL.
				accessPage.Execute(w, struct{ Return, Nonce string }{r.URL.RequestURI(), nonce})
			} else {
				w.WriteHeader(401)
				io.WriteString(w, "需要访问 key 验证，请重新打开页面。")
			}
			return
		}
		if !accesskeys.InScope(g, r.URL.Path) || (g.ReadOnly && !readonlyRequest(r)) {
			http.Error(w, "临时 key 不允许访问此页面或执行修改。", 403)
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" && r.Header.Get("Origin") != origin {
			http.Error(w, "请求来源不正确。", 403)
			return
		}
		if r.URL.Path == "/wechat-files/manage/api" && r.Method == "POST" {
			body, e := io.ReadAll(io.LimitReader(r.Body, (96<<10)+1))
			r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(body))
			if e == nil && len(body) <= 96<<10 {
				var b workspaceRequest
				if json.Unmarshal(body, &b) == nil && strings.HasPrefix(b.Action, "access_") && g.Kind != "primary" {
					http.Error(w, "访问 key 管理需要长期 key 的管理员权限。", 403)
					return
				}
				if json.Unmarshal(body, &b) == nil && b.Action == "logout" {
					in.access.EndSession(cookie.Value)
					http.SetCookie(w, &http.Cookie{Name: "wechat_access", Value: "", Path: "/wechat-files/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}
func (in *inbound) deliverAccessKeys(ctx context.Context) {
	send := func() {
		pending, e := in.access.Pending(in.owner)
		if e != nil {
			return
		}
		for _, p := range pending {
			if _, e = in.client.SendText(ctx, weixin.Reply{ToUserID: p.Owner, ClientID: "go-access-" + p.ID}, p.Text); e == nil {
				in.access.Delivered(p.Owner, p.ID)
			}
		}
	}
	send()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			send()
		}
	}
}
