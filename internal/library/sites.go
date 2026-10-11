package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

type CampusApp struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	URL     string    `json:"url"`
	Updated time.Time `json:"updated"`
}

func campusAppURL(raw string) (*url.URL, error) {
	u, e := url.Parse(strings.TrimSpace(raw))
	if e != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Port() == "" {
		return nil, errors.New("campus_app_requires_local_http_address")
	}
	if u.Hostname() == "localhost" {
		u.Host = net.JoinHostPort("127.0.0.1", u.Port())
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return nil, errors.New("campus_app_requires_local_http_address")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u, nil
}
func probeCampusApp(ctx context.Context, raw string) (int, error) {
	u, e := campusAppURL(raw)
	if e != nil {
		return 0, e
	}
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	defer t.CloseIdleConnections()
	client := http.Client{Transport: t, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r, e := http.NewRequestWithContext(ctx, "GET", u.String()+"/", nil)
	if e != nil {
		return 0, e
	}
	res, e := client.Do(r)
	if e != nil {
		return 0, errors.New("campus_app_unreachable")
	}
	defer res.Body.Close()
	io.Copy(io.Discard, io.LimitReader(res.Body, 2048))
	return res.StatusCode, nil
}
func (s *Store) CampusApps(owner string) ([]CampusApp, error) {
	rows, e := s.db.Query("SELECT document FROM campus_apps WHERE owner=? ORDER BY title,id", owner)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	apps := []CampusApp{}
	for rows.Next() {
		var raw []byte
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		var a CampusApp
		if e = decode(raw, &a); e != nil {
			return nil, e
		}
		apps = append(apps, a)
	}
	return apps, rows.Err()
}
func (s *Store) CampusApp(owner, id string) (CampusApp, error) {
	var raw []byte
	e := s.db.QueryRow("SELECT document FROM campus_apps WHERE owner=? AND id=?", owner, id).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return CampusApp{}, ErrNotFound
	}
	var a CampusApp
	if e == nil {
		e = decode(raw, &a)
	}
	return a, e
}
func (s *Store) AddCampusApp(ctx context.Context, owner, title, raw string) (CampusApp, error) {
	if !validName(title) {
		return CampusApp{}, errors.New("invalid_campus_app_title")
	}
	u, e := campusAppURL(raw)
	if e != nil {
		return CampusApp{}, e
	}
	status, e := probeCampusApp(ctx, u.String())
	if e != nil {
		return CampusApp{}, e
	}
	if status >= 500 {
		return CampusApp{}, errors.New("campus_app_server_error")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a := CampusApp{ID: id(), Title: title, URL: u.String(), Updated: time.Now().UTC()}
	var old []byte
	e = s.db.QueryRow("SELECT document FROM campus_apps WHERE owner=? AND upstream=?", owner, a.URL).Scan(&old)
	if e == nil {
		var prior CampusApp
		if e = decode(old, &prior); e != nil {
			return a, e
		}
		a.ID = prior.ID
	} else if !errors.Is(e, sql.ErrNoRows) {
		return a, e
	}
	var count int
	if e = s.db.QueryRow("SELECT COUNT(*) FROM campus_apps WHERE owner=?", owner).Scan(&count); e != nil {
		return a, e
	}
	if count >= 30 && len(old) == 0 {
		return a, errors.New("campus_app_limit_reached")
	}
	b, _ := json.Marshal(a)
	_, e = s.db.Exec(`INSERT INTO campus_apps(owner,id,title,upstream,document) VALUES(?,?,?,?,?) ON CONFLICT(owner,upstream) DO UPDATE SET title=excluded.title,document=excluded.document`, owner, a.ID, title, a.URL, b)
	return a, e
}
func (s *Store) RemoveCampusApp(owner, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, e := s.db.Exec("DELETE FROM campus_apps WHERE owner=? AND id=?", owner, id)
	if e != nil {
		return e
	}
	n, e := res.RowsAffected()
	if e == nil && n == 0 {
		return ErrNotFound
	}
	return e
}
func (s *Store) CheckCampusApp(ctx context.Context, owner, id string) (any, error) {
	a, e := s.CampusApp(owner, id)
	if e != nil {
		return nil, e
	}
	status, e := probeCampusApp(ctx, a.URL)
	if e != nil {
		return map[string]any{"ok": false, "status": 0, "error": e.Error()}, nil
	}
	return map[string]any{"ok": status < 500, "status": status}, nil
}
func (s *Store) serveCampusApp(w http.ResponseWriter, r *http.Request) {
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/library/app/"), "/", 2)
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	a, e := s.CampusApp(r.Header.Get("X-Campus-App-Owner"), parts[0])
	if e != nil {
		http.NotFound(w, r)
		return
	}
	u, e := campusAppURL(a.URL)
	if e != nil {
		http.Error(w, "invalid_campus_app", 502)
		return
	}
	p := httputil.NewSingleHostReverseProxy(u)
	director := p.Director
	p.Director = func(req *http.Request) {
		director(req)
		req.URL.Path = u.Path + "/" + parts[1]
		req.URL.RawPath = ""
		req.Host = u.Host
		req.Header.Del("Authorization")
		if auth := req.Header.Get("X-Campus-App-Authorization"); auth != "" {
			req.Header.Set("Authorization", auth)
		}
		req.Header.Del("X-Campus-App-Authorization")
		req.Header.Del("X-Campus-App-Owner")
		cookies := req.Cookies()
		req.Header.Del("Cookie")
		for _, c := range cookies {
			if c.Name != "wechat_manage" {
				req.AddCookie(c)
			}
		}
		req.Header.Set("Accept-Encoding", "identity")
	}
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	t.ResponseHeaderTimeout = 20 * time.Second
	p.Transport = t
	defer t.CloseIdleConnections()
	p.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) { http.Error(w, "campus_app_unreachable", 502) }
	p.ServeHTTP(w, r)
}
