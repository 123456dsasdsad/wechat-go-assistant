package library

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (s *Store) Action(q Request) (any, error) {
	if q.Owner == "" {
		return nil, errors.New("owner_required")
	}
	switch q.Action {
	case "begin":
		return s.Begin(q.Owner, q.Key, q.Conversation, q.Collection)
	case "active":
		return s.Active(q.Owner, q.Conversation)
	case "intake":
		return s.Intake(q.Owner, q.ID)
	case "append":
		return map[string]bool{"ok": true}, s.Append(q.Owner, q.ID, q.Key, q.Text, nil)
	case "state":
		return map[string]bool{"ok": true}, s.State(q.Owner, q.ID, q.State)
	case "research":
		return s.Research(q.Owner, q.ID)
	case "commit":
		if q.Research == nil {
			return nil, errors.New("research_required")
		}
		return s.SaveResearch(q.Owner, q.ID, *q.Research)
	case "get":
		return s.Get(q.Owner, q.MaterialID)
	case "search":
		ms, e := s.Search(q.Owner, q.Query)
		for i := range ms {
			ms[i].Text = ""
			ms[i].Claims = nil
			ms[i].Assets = nil
		}
		return ms, e
	case "topics":
		return s.Topics(q.Owner)
	case "snapshot":
		return s.Snapshot(q.Owner, q.Collection)
	case "review":
		return s.Review(q.Owner, q.Collection, q.Version)
	case "publish":
		if q.Review == nil {
			return nil, errors.New("review_required")
		}
		snap, e := s.Snapshot(q.Owner, q.Collection)
		if e != nil {
			return nil, e
		}
		if snap.Topic.Revision != q.Review.CorpusRevision || snap.Topic.Version != q.Version {
			return nil, ErrStale
		}
		return s.Publish(q.Owner, snap, *q.Review)
	case "review_error":
		return map[string]bool{"ok": true}, s.ReviewError(q.Owner, q.Collection, q.Text)
	case "notes":
		return map[string]bool{"ok": true}, s.Notes(q.Owner, q.Collection, q.Text)
	case "import":
		return map[string]bool{"ok": true}, s.Import(q.Owner, q.Collection, q.Text)
	case "restore_review":
		return s.RestoreReview(q.Owner, q.Collection, q.Version)
	case "lock":
		return map[string]bool{"ok": true}, s.Lock(q.Owner, q.Collection, q.Key, q.Text)
	case "move", "tags", "trash", "restore":
		return s.Change(q.Owner, q.MaterialID, q.Topics, q.Tags, q.Action)
	default:
		return nil, errors.New("unsupported_library_action")
	}
}
func Handler(s *Store, key string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if len(key) < 32 || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+key)) != 1 {
			http.Error(w, "unauthorized", 401)
			return
		}
		if r.URL.Path == "/library/health" {
			json.NewEncoder(w).Encode(map[string]bool{"ok": s.db.Ping() == nil})
			return
		}
		if r.URL.Path == "/library/blob" {
			owner := r.URL.Query().Get("owner")
			iid := r.URL.Query().Get("intake")
			sha := r.URL.Query().Get("sha")
			if owner == "" {
				http.Error(w, "owner_required", 400)
				return
			}
			if r.Method == "GET" {
				f, e := s.Blob(owner, sha)
				if e != nil {
					http.NotFound(w, r)
					return
				}
				defer f.Close()
				w.Header().Set("Content-Type", "application/octet-stream")
				io.Copy(w, f)
				return
			}
			if r.Method == "PUT" {
				if _, e := s.Intake(owner, iid); e != nil {
					http.NotFound(w, r)
					return
				}
				size, e := strconv.ParseInt(r.URL.Query().Get("size"), 10, 64)
				a := Asset{SHA256: sha, Name: r.URL.Query().Get("name"), Size: size}
				if e == nil {
					e = s.PutBlob(a, r.Body)
				}
				if e == nil {
					e = s.Append(owner, iid, "asset:"+sha, "", []Asset{a})
				}
				if e != nil {
					http.Error(w, e.Error(), 400)
					return
				}
				json.NewEncoder(w).Encode(map[string]bool{"ok": true})
				return
			}
		}
		if r.Method != "POST" || r.URL.Path != "/library/api" {
			http.NotFound(w, r)
			return
		}
		var q Request
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20))
		d.DisallowUnknownFields()
		if d.Decode(&q) != nil {
			http.Error(w, "invalid_library_request", 400)
			return
		}
		v, e := s.Action(q)
		if e != nil {
			code := 400
			if errors.Is(e, ErrNotFound) {
				code = 404
			}
			if errors.Is(e, ErrStale) {
				code = 409
			}
			http.Error(w, e.Error(), code)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(v)
	})
}

type Client struct {
	URL, Key string
	HTTP     *http.Client
}

func (c *Client) Download(ctx context.Context, owner, sha string) (io.ReadCloser, error) {
	q := url.Values{"owner": {owner}, "sha": {sha}}
	r, e := http.NewRequestWithContext(ctx, "GET", c.URL+"/library/blob?"+q.Encode(), nil)
	if e != nil {
		return nil, e
	}
	r.Header.Set("Authorization", "Bearer "+c.Key)
	client := *c.HTTP
	client.Timeout = 0 // Streaming uses the request context rather than a total-size-dependent deadline.
	res, e := client.Do(r)
	if e != nil {
		return nil, e
	}
	if res.StatusCode != 200 {
		res.Body.Close()
		return nil, ErrNotFound
	}
	return res.Body, nil
}

func NewClient(raw, key string) (*Client, error) {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "http" || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() || len(key) < 32 {
		return nil, errors.New("library_requires_private_loopback")
	}
	return &Client{URL: strings.TrimRight(raw, "/"), Key: key, HTTP: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) Call(ctx context.Context, q Request, out any) error {
	b, e := json.Marshal(q)
	if e != nil {
		return e
	}
	r, e := http.NewRequestWithContext(ctx, "POST", c.URL+"/library/api", bytes.NewReader(b))
	if e != nil {
		return e
	}
	r.Header.Set("Content-Type", "application/json")
	return c.do(r, out)
}
func (c *Client) do(r *http.Request, out any) error {
	r.Header.Set("Authorization", "Bearer "+c.Key)
	res, e := c.HTTP.Do(r)
	if e != nil {
		return errors.New("library_offline")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		if res.StatusCode == 404 {
			return ErrNotFound
		}
		if res.StatusCode == 409 {
			return ErrStale
		}
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 200))
		return errors.New(strings.TrimSpace(string(raw)))
	}
	if out == nil {
		_, e = io.Copy(io.Discard, res.Body)
		return e
	}
	return json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(out)
}
func (c *Client) Upload(ctx context.Context, owner, iid string, a Asset, src io.Reader) error {
	q := url.Values{"owner": {owner}, "intake": {iid}, "sha": {a.SHA256}, "name": {a.Name}, "size": {strconv.FormatInt(a.Size, 10)}}
	r, e := http.NewRequestWithContext(ctx, "PUT", c.URL+"/library/blob?"+q.Encode(), src)
	if e != nil {
		return e
	}
	r.ContentLength = a.Size
	stream := *c
	client := *c.HTTP
	client.Timeout = 0
	stream.HTTP = &client
	return stream.do(r, nil)
}
