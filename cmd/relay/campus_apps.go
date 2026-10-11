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

func adaptCampusAppBody(body []byte, base string) []byte {
	return appRootLiteral.ReplaceAllFunc(body, func(match []byte) []byte {
		parts := appRootLiteral.FindSubmatch(match)
		return []byte(string(parts[1]) + base + string(parts[2]) + string(parts[3]))
	})
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
		http.Error(w, "校园应用暂时不可用，请检查服务是否已启动。", 502)
	}
	p.ServeHTTP(w, r)
}
