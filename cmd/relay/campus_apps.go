package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var appRootLiteral = regexp.MustCompile(`(["'\x60])/([A-Za-z0-9_.-][^"'\x60\s<>]*|)(["'\x60])`)
var campusAppID = regexp.MustCompile(`^[a-f0-9]{24}$`)

const campusRestartHTML = `<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta http-equiv="refresh" content="5"><title>校园应用恢复中</title><body style="font:16px/1.7 system-ui;background:#eef4f8;color:#253549;padding:28px"><h1>校园应用暂时不可用</h1><p>应用可能正在重启或已经停止。此访问链接仍有效，无需重新生成。</p><p>页面每 5 秒自动重试，应用恢复后会继续打开。</p></body></html>`

func adaptCampusAppBody(body []byte, base string) []byte {
	var out bytes.Buffer
	last := 0
	for _, m := range appRootLiteral.FindAllSubmatchIndex(body, -1) {
		out.Write(body[last:m[0]])
		path := string(body[m[4]:m[5]])
		before := m[0] - 1
		for before >= 0 && strings.ContainsRune(" \t\r\n", rune(body[before])) {
			before--
		}
		// A suffix in '/api/projects/'+id+'/meta' belongs to the existing
		// URL. Rewriting it again creates an invalid duplicate prefix.
		skip := before >= 0 && body[before] == '+'
		skip = skip || strings.HasPrefix("/"+path, "/wechat-files/")
		if path == "" {
			left := strings.ToLower(strings.TrimSpace(string(body[max(0, m[0]-24):m[0]])))
			urlContext := false
			for _, suffix := range []string{"href=", "src=", "action=", "url(", "fetch(", "api("} {
				urlContext = urlContext || strings.HasSuffix(left, suffix)
			}
			skip = skip || !urlContext // Do not rewrite split('/'), join('/'), etc.
		}
		if skip {
			out.Write(body[m[0]:m[1]])
		} else {
			out.Write(body[m[2]:m[3]])
			out.WriteString(base + path)
			out.Write(body[m[6]:m[7]])
		}
		last = m[1]
	}
	out.Write(body[last:])
	return out.Bytes()
}
func (in *inbound) serveCampusApp(w http.ResponseWriter, r *http.Request, owner string) {
	if in.library == nil {
		http.Error(w, "校园通道未连接。", 503)
		return
	}
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/wechat-files/manage/apps/"), "/", 2)
	if len(parts) != 2 || !campusAppID.MatchString(parts[0]) {
		http.NotFound(w, r)
		return
	}
	u, _ := url.Parse(in.library.URL)
	p := httputil.NewSingleHostReverseProxy(u)
	director := p.Director
	base := "/wechat-files/manage/apps/" + parts[0] + "/"
	p.Director = func(req *http.Request) {
		appAuth := req.Header.Get("Authorization")
		cookies := req.Cookies()
		director(req)
		req.URL.Path = "/library/app/" + parts[0] + "/" + parts[1]
		req.URL.RawPath = ""
		req.Host = u.Host
		req.Header.Del("Cookie")
		for _, c := range cookies {
			if strings.HasPrefix(c.Name, "campus_"+parts[0]+"_") {
				c.Name = strings.TrimPrefix(c.Name, "campus_"+parts[0]+"_")
				req.AddCookie(c)
			}
		}
		req.Header.Del("X-Campus-App-Authorization")
		if appAuth != "" {
			req.Header.Set("X-Campus-App-Authorization", appAuth)
		}
		req.Header.Set("Authorization", "Bearer "+in.library.Key)
		req.Header.Set("X-Campus-App-Owner", owner)
		req.Header.Set("Accept-Encoding", "identity")
	}
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	t.ResponseHeaderTimeout = 25 * time.Second
	p.Transport = t
	defer t.CloseIdleConnections()
	p.ModifyResponse = func(res *http.Response) error {
		if parts[1] == "" && r.Method == "GET" && strings.Contains(r.Header.Get("Accept"), "text/html") && (res.StatusCode == 502 || res.StatusCode == 503) {
			res.Body.Close()
			res.Body = io.NopCloser(strings.NewReader(campusRestartHTML))
			res.ContentLength = int64(len(campusRestartHTML))
			res.Header.Set("Content-Type", "text/html; charset=utf-8")
			res.Header.Del("Content-Length")
			res.Header.Del("Content-Encoding")
			res.Header.Del("ETag")
			res.Header.Set("Cache-Control", "no-store")
			return nil
		}
		if location := res.Header.Get("Location"); strings.HasPrefix(location, "/") && !strings.HasPrefix(location, "//") {
			res.Header.Set("Location", base+strings.TrimPrefix(location, "/"))
		}
		cookies := res.Cookies()
		res.Header.Del("Set-Cookie")
		for _, c := range cookies {
			c.Name = "campus_" + parts[0] + "_" + c.Name
			c.Domain = ""
			c.Path = base + strings.TrimPrefix(c.Path, "/")
			c.Secure = true
			c.SameSite = http.SameSiteStrictMode
			res.Header.Add("Set-Cookie", c.String())
		}
		ct := res.Header.Get("Content-Type")
		if res.Header.Get("Content-Encoding") != "" || (!strings.Contains(ct, "text/html") && !strings.Contains(ct, "javascript") && !strings.Contains(ct, "text/css")) || res.ContentLength > 8<<20 {
			return nil
		}
		body, e := io.ReadAll(io.LimitReader(res.Body, (8<<20)+1))
		if e != nil {
			return e
		}
		if len(body) > 8<<20 {
			res.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), res.Body))
			return nil
		}
		res.Body.Close()
		body = adaptCampusAppBody(body, base)
		res.Body = io.NopCloser(bytes.NewReader(body))
		res.ContentLength = int64(len(body))
		res.Header.Del("Content-Length")
		res.Header.Del("ETag")
		res.Header.Del("Content-MD5")
		return nil
	}
	p.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
		if parts[1] == "" && r.Method == "GET" && strings.Contains(r.Header.Get("Accept"), "text/html") {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(502)
			io.WriteString(w, campusRestartHTML)
			return
		}
		http.Error(w, "校园应用暂时不可用，请检查服务是否已启动。", 502)
	}
	p.ServeHTTP(w, r)
}
