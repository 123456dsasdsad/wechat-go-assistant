package accountupload

import (
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
)

//go:embed page.html
var page string

const pathPrefix = "/wechat-files/accounts/"
const cookieName = "wechat_accounts"

func headers(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
}
func jsonResponse(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}

func PublicHandler(store *Store, publicURL, root string) (http.Handler, error) {
	u, e := url.Parse(publicURL)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "/wechat-files/" || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("invalid_public_account_url")
	}
	origin := u.Scheme + "://" + u.Host
	busy := make(chan struct{}, 1)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers(w)
		if r.Method == "GET" && r.URL.Path == pathPrefix {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			io.WriteString(w, page)
			return
		}
		if r.URL.Path != pathPrefix+"session" && r.URL.Path != pathPrefix+"upload" && r.URL.Path != pathPrefix+"status" && r.URL.Path != pathPrefix+"list" && r.URL.Path != pathPrefix+"delete" {
			http.NotFound(w, r)
			return
		}
		if r.Method == "POST" && r.Header.Get("Origin") != origin {
			http.Error(w, "请求来源不正确，请从微信重新打开账号上传链接。", 403)
			return
		}
		if r.Method == "POST" && r.URL.Path == pathPrefix+"session" {
			var body struct {
				Token string `json:"token"`
			}
			d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
			d.DisallowUnknownFields()
			if d.Decode(&body) != nil {
				http.Error(w, "账号上传链接无效。", 400)
				return
			}
			var extra any
			if d.Decode(&extra) != io.EOF {
				http.Error(w, "账号上传链接无效。", 400)
				return
			}
			if _, ok := store.Authorize(body.Token); !ok {
				http.Error(w, "链接已过期，请在微信发送“上传账号”获取新链接。", 401)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: cookieName, Value: body.Token, Path: pathPrefix, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 1800})
			jsonResponse(w, map[string]any{"ok": true})
			return
		}
		if !(r.Method == "POST" && (r.URL.Path == pathPrefix+"upload" || r.URL.Path == pathPrefix+"delete") || r.Method == "GET" && (r.URL.Path == pathPrefix+"status" || r.URL.Path == pathPrefix+"list")) {
			http.Error(w, "请求方式不正确。", 405)
			return
		}
		cookie, e := r.Cookie(cookieName)
		if e != nil {
			http.Error(w, "请在微信发送“上传账号”，从新链接打开页面。", 401)
			return
		}
		owner, ok := store.Authorize(cookie.Value)
		if !ok {
			http.Error(w, "链接已过期，请在微信发送“上传账号”获取新链接。", 401)
			return
		}
		if r.URL.Path == pathPrefix+"list" || r.URL.Path == pathPrefix+"delete" {
			unlock, locked, err := MutationLock(root)
			if err != nil || !locked {
				http.Error(w, "账号池正在更新，请稍后刷新。", 409)
				return
			}
			defer unlock()
			if r.Method == "GET" {
				views, err := ListAccounts(root)
				if err != nil {
					http.Error(w, "账号列表暂时无法读取，请稍后刷新。", 503)
					return
				}
				store.MarkPendingDeletes(views)
				jsonResponse(w, map[string]any{"ok": true, "accounts": views})
				return
			}
			var body struct {
				AccountIDs []string `json:"account_ids"`
				RequestID  string   `json:"request_id"`
			}
			d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
			d.DisallowUnknownFields()
			if d.Decode(&body) != nil || !batchID(body.RequestID) {
				http.Error(w, "删除请求无效，请刷新页面重试。", 400)
				return
			}
			var extra any
			if d.Decode(&extra) != io.EOF {
				http.Error(w, "删除请求无效。", 400)
				return
			}
			receipt, err := store.StageDelete(owner, cookie.Value+":"+body.RequestID, root, body.AccountIDs)
			if err != nil {
				http.Error(w, "未删除账号：请刷新列表并重新选择。", 400)
				return
			}
			jsonResponse(w, map[string]any{"ok": true, "receipt": receipt, "message": receipt.Text()})
			return
		}
		if r.Method == "GET" {
			id := r.URL.Query().Get("id")
			if id == "" {
				jsonResponse(w, map[string]any{"ok": true, "receipts": store.Recent(owner)})
				return
			}
			receipt, e := store.Status(owner, id)
			if e != nil {
				http.NotFound(w, r)
				return
			}
			jsonResponse(w, map[string]any{"ok": true, "receipt": receipt, "message": receipt.Text()})
			return
		}
		select {
		case busy <- struct{}{}:
			defer func() { <-busy }()
		default:
			http.Error(w, "正在接收另一份账号文件，请稍后重试。", 429)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, MaxUploadBytes+(1<<20))
		reader, e := r.MultipartReader()
		if e != nil {
			http.Error(w, "请选择一个 JSON 文件。", 400)
			return
		}
		part, e := reader.NextPart()
		if e != nil || part.FormName() != "file" || !strings.EqualFold(filepath.Ext(part.FileName()), ".json") {
			http.Error(w, "请选择一个以 .json 结尾的账号文件。", 400)
			return
		}
		defer part.Close()
		data, e := io.ReadAll(io.LimitReader(part, MaxUploadBytes+1))
		if e != nil || len(data) > MaxUploadBytes {
			http.Error(w, "账号 JSON 超过 32 MiB 或上传未完成，请分批上传。", 413)
			return
		}
		if _, e = reader.NextPart(); e != io.EOF {
			http.Error(w, "每次请选择一个 JSON 文件。", 400)
			return
		}
		receipt, e := store.Stage(owner, cookie.Value, data)
		if e != nil {
			http.Error(w, "JSON 未导入：请使用完整的 Codex 账号导出文件，确保每项包含 access_token、id_token 和账号身份。账号身份不一致或混入其他类型时整批不导入。", 400)
			return
		}
		jsonResponse(w, map[string]any{"ok": true, "receipt": receipt, "message": receipt.Text()})
	}), nil
}
