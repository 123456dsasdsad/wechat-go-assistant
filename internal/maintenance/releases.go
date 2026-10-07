package maintenance

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var repos = map[string]bool{"openai/codex": true, "jlcodes99/cockpit-tools": true, "clash-verge-rev/clash-verge-rev": true, "caddyserver/caddy": true}

type Asset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}
type Release struct {
	Tag        string  `json:"tag_name"`
	Prerelease bool    `json:"prerelease"`
	Draft      bool    `json:"draft"`
	Assets     []Asset `json:"assets"`
}
type Program struct {
	Name         string `json:"name"`
	Repo         string `json:"repo"`
	Version      string `json:"version"`
	AssetPattern string `json:"asset_pattern"`
	Target       string `json:"target"`
}
type Update struct {
	Program
	Latest  string `json:"latest"`
	Archive string `json:"archive,omitempty"`
	SHA256  string `json:"sha256,omitempty"`
	State   string `json:"state"`
}

var versionRE = regexp.MustCompile(`^(?:rust-)?v?(\d+)\.(\d+)\.(\d+)$`)

func Newer(a, b string) bool {
	aa, bb := versionRE.FindStringSubmatch(a), versionRE.FindStringSubmatch(b)
	if aa == nil || bb == nil {
		return false
	}
	for i := 1; i <= 3; i++ {
		x, _ := strconv.Atoi(aa[i])
		y, _ := strconv.Atoi(bb[i])
		if x != y {
			return x > y
		}
	}
	return false
}
func AllowedURL(raw string) bool {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	for repo := range repos {
		if u.Host == "api.github.com" && u.Path == "/repos/"+repo+"/releases/latest" {
			return true
		}
		if u.Host == "github.com" && strings.HasPrefix(u.Path, "/"+repo+"/releases/download/") {
			parts := strings.Split(strings.TrimPrefix(u.Path, "/"+repo+"/releases/download/"), "/")
			if len(parts) == 2 && parts[0] != "" && parts[1] != "" && !strings.Contains(u.Path, "..") && !strings.Contains(u.Path, "\\") {
				return true
			}
		}
	}
	return false
}
func Bridge(key string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if key == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+key)) != 1 {
			http.Error(w, "unauthorized", 401)
			return
		}
		raw := r.URL.Query().Get("url")
		if r.Method != "GET" || !AllowedURL(raw) {
			http.Error(w, "release_url_rejected", 400)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, "GET", raw, nil)
		req.Header.Set("User-Agent", "campus-stack-maintenance/1")
		c := &http.Client{Timeout: 10 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			h := req.URL.Hostname()
			if len(via) > 5 || req.URL.Scheme != "https" || (h != "github.com" && h != "release-assets.githubusercontent.com" && h != "objects.githubusercontent.com") {
				return errors.New("release_redirect_rejected")
			}
			return nil
		}}
		resp, e := c.Do(req)
		if e != nil {
			http.Error(w, "release_fetch_failed", 502)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			http.Error(w, "release_upstream_unavailable", 502)
			return
		}
		if resp.ContentLength > 512*1024*1024 {
			http.Error(w, "release_too_large", 413)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		if resp.ContentLength >= 0 {
			w.Header().Set("Content-Length", fmt.Sprint(resp.ContentLength))
		}
		io.Copy(w, io.LimitReader(resp.Body, 512*1024*1024+1))
	})
}

type Fetcher struct {
	Client         HTTPDoer
	BridgeURL, Key string
}

func (f Fetcher) Get(ctx context.Context, raw string) (io.ReadCloser, int64, error) {
	if !AllowedURL(raw) {
		return nil, 0, errors.New("release_url_rejected")
	}
	u := raw
	if f.BridgeURL != "" {
		u = strings.TrimRight(f.BridgeURL, "/") + "/maintenance/release?url=" + url.QueryEscape(raw)
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	req.Header.Set("User-Agent", "campus-stack-maintenance/1")
	if f.BridgeURL != "" {
		req.Header.Set("Authorization", "Bearer "+f.Key)
	}
	resp, e := f.Client.Do(req)
	if e != nil {
		return nil, 0, errors.New("release_fetch_failed")
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, 0, errors.New("release_upstream_unavailable")
	}
	return resp.Body, resp.ContentLength, nil
}
func (f Fetcher) Stage(ctx context.Context, programs []Program, dir string) ([]Update, error) {
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	var updates []Update
	for _, p := range programs {
		u := Update{Program: p}
		if !repos[p.Repo] {
			return nil, errors.New("unapproved_repository")
		}
		body, _, e := f.Get(ctx, "https://api.github.com/repos/"+p.Repo+"/releases/latest")
		if e != nil {
			u.State = "检查失败，下次重试"
			updates = append(updates, u)
			continue
		}
		var release Release
		e = json.NewDecoder(io.LimitReader(body, 2*1024*1024)).Decode(&release)
		body.Close()
		u.Latest = release.Tag
		if e != nil || release.Prerelease || release.Draft || versionRE.FindStringSubmatch(release.Tag) == nil {
			u.State = "非稳定发布，保留现版"
			updates = append(updates, u)
			continue
		}
		if !Newer(release.Tag, p.Version) {
			u.State = "已是最新稳定版"
			updates = append(updates, u)
			continue
		}
		re, e := regexp.Compile(p.AssetPattern)
		if e != nil {
			return nil, errors.New("invalid_asset_pattern")
		}
		var candidates []Asset
		for _, a := range release.Assets {
			if re.MatchString(a.Name) {
				candidates = append(candidates, a)
			}
		}
		if len(candidates) != 1 {
			u.State = "更新资产不唯一，保留现版"
			updates = append(updates, u)
			continue
		}
		a := candidates[0]
		expected := strings.TrimPrefix(a.Digest, "sha256:")
		if len(expected) != 64 || !strings.HasPrefix(a.Digest, "sha256:") || a.Size <= 0 || a.Size > 512*1024*1024 || filepath.Base(a.Name) != a.Name {
			u.State = "缺少可信 SHA256，保留现版"
			updates = append(updates, u)
			continue
		}
		if free, e := availableSpace(dir); e == nil && free < a.Size*3+512*1024*1024 {
			u.State = "暂存空间不足，保留现版"
			updates = append(updates, u)
			continue
		}
		path := filepath.Join(dir, a.Name)
		body, _, e = f.Get(ctx, a.URL)
		if e != nil {
			u.State = "下载失败，保留现版"
			updates = append(updates, u)
			continue
		}
		file, e := os.CreateTemp(dir, ".download-*")
		if e != nil {
			body.Close()
			return nil, e
		}
		temp := file.Name()
		h := sha256.New()
		n, e := io.Copy(io.MultiWriter(file, h), io.LimitReader(body, a.Size+1))
		body.Close()
		file.Close()
		actual := hex.EncodeToString(h.Sum(nil))
		if e != nil || n != a.Size || actual != expected {
			os.Remove(temp)
			u.State = "校验失败，保留现版"
			updates = append(updates, u)
			continue
		}
		if e = os.Rename(temp, path); e != nil {
			os.Remove(temp)
			return nil, e
		}
		u.Archive = path
		u.SHA256 = actual
		u.State = "已校验，等待安装"
		updates = append(updates, u)
	}
	return updates, nil
}
