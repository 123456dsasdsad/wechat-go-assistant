package files

import (
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
)

//go:embed page.html
var page string

type AnalyzeFunc func(owner, source string, ref Ref, instruction string) (map[string]any, error)

func PublicHandler(store *Store, publicURL string, analyze ...AnalyzeFunc) (http.Handler, error) {
	u, err := url.Parse(publicURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/wechat-files/" {
		return nil, io.ErrUnexpectedEOF
	}
	origin := u.Scheme + "://" + u.Host
	busy := make(chan struct{}, 1)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		if r.Method == "GET" && r.URL.Path == "/wechat-files/" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			io.WriteString(w, page)
			return
		}
		if r.Method != "POST" || r.Header.Get("Origin") != origin {
			http.Error(w, "请求来源不正确，请从微信重新打开上传链接。", 403)
			return
		}
		switch r.URL.Path {
		case "/wechat-files/analyze":
			cookie, err := r.Cookie("wechat_upload")
			if err != nil {
				http.Error(w, "请从微信重新打开上传链接。", 401)
				return
			}
			owner, ok := store.Authorize(cookie.Value)
			if !ok {
				http.Error(w, "链接已过期，请获取新链接。", 401)
				return
			}
			var body struct {
				FileID      string `json:"file_id"`
				Instruction string `json:"instruction"`
				Source      string `json:"source"`
			}
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 12<<10))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&body) != nil || !validHex(body.Source, 24) || len(body.Instruction) > 8192 || len(body.Instruction) == 0 {
				http.Error(w, "请填写 8 KiB 内的分析要求。", 400)
				return
			}
			ref, err := store.Get(owner, body.FileID)
			if err != nil {
				http.Error(w, "文件不存在或已过期。", 404)
				return
			}
			if len(analyze) == 0 || analyze[0] == nil {
				http.Error(w, "请回微信发送页面上的文件指令。", 503)
				return
			}
			result, err := analyze[0](owner, "webtask:"+body.Source, ref, body.Instruction)
			if err != nil {
				http.Error(w, "任务暂未创建，请回微信发送文件指令。", 503)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(result)
		case "/wechat-files/session":
			var body struct {
				Token string `json:"token"`
			}
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&body) != nil {
				http.Error(w, "上传链接无效。", 400)
				return
			}
			if _, ok := store.Authorize(body.Token); !ok {
				http.Error(w, "链接已过期，请在微信发送“上传文件”获取新链接。", 401)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "wechat_upload", Value: body.Token, Path: "/wechat-files/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 1800})
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"ok":true}`)
		case "/wechat-files/upload":
			cookie, err := r.Cookie("wechat_upload")
			if err != nil {
				http.Error(w, "请从微信重新打开上传链接。", 401)
				return
			}
			owner, ok := store.Authorize(cookie.Value)
			if !ok {
				http.Error(w, "链接已过期，请在微信发送“上传文件”获取新链接。", 401)
				return
			}
			select {
			case busy <- struct{}{}:
				defer func() { <-busy }()
			default:
				http.Error(w, "正在处理另一个上传，请稍后重试。", 429)
				return
			}
			multipart, err := r.MultipartReader()
			if err != nil {
				http.Error(w, "请选择一个文件。", 400)
				return
			}
			part, err := multipart.NextPart()
			if err != nil || part.FormName() != "file" || part.FileName() == "" {
				http.Error(w, "请选择一个文件。", 400)
				return
			}
			name := part.FileName()
			defer part.Close()
			var source [16]byte
			if _, err = rand.Read(source[:]); err != nil {
				http.Error(w, "上传暂时失败，请重试。", 500)
				return
			}
			ref, err := store.Save(owner, "web:"+hex.EncodeToString(source[:]), name, &singleUpload{part: part, next: func() error { _, e := multipart.NextPart(); return e }})
			if err != nil {
				if err.Error() == "file_storage_full" {
					http.Error(w, "服务器剩余空间不足，或文件数量已满。请清理后重试。", 507)
					return
				}
				http.Error(w, "文件保存失败，请检查文件名，确保上传完成且只选择一个文件。", 400)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "file": ref, "command": "分析文件 " + ref.ID + "：请概述文件内容"})
		default:
			http.NotFound(w, r)
		}
	}), nil
}

type singleUpload struct {
	part    io.Reader
	next    func() error
	checked bool
}

func (upload *singleUpload) Read(p []byte) (int, error) {
	n, err := upload.part.Read(p)
	if err == io.EOF && !upload.checked {
		upload.checked = true
		if next := upload.next(); next != io.EOF {
			return n, io.ErrUnexpectedEOF
		}
	}
	return n, err
}
