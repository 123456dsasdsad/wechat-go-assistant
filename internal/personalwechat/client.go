// Package personalwechat is an opt-in personal-account transport candidate.
// Activation requires a separate QR/forward acceptance; iLink remains default.
package personalwechat

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
	"github.com/eatmoreapple/openwechat"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Config struct {
	CookiePath   string `json:"cookie_path"`
	Root         string `json:"root"`
	FriendRemark string `json:"friend_remark"`
	OwnerAlias   string `json:"owner_alias"`
}
type Client struct {
	bot    *openwechat.Bot
	friend *openwechat.Friend
	cfg    Config
	db     *sql.DB
	mu     sync.Mutex
	err    error
}

func Login(ctx context.Context, path string, qr func(string)) ([]string, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("cookie_path_must_be_absolute")
	}
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return nil, e
	}
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	f.Close()
	bot := openwechat.New(ctx)
	openwechat.Desktop.Prepare(bot)
	trace := &loginTrace{stage: "start"}
	bot.Caller.Client.AddHttpHook(trace)
	bot.UUIDCallback = func(uuid string) { qr(openwechat.GetQrcodeUrl(uuid)) }
	store := openwechat.NewFileHotReloadStorage(path)
	defer store.Close()
	if e = bot.HotLogin(store, openwechat.HotLoginWithRetry(true)); e != nil {
		return nil, trace.failure(e)
	}
	if e = bot.DumpHotReloadStorage(); e != nil {
		return nil, errors.New("personal_wechat_cookie_save_failed")
	}
	_ = os.Chmod(path, 0600)
	self, e := bot.GetCurrentUser()
	if e != nil {
		return nil, errors.New("personal_wechat_login_not_initialized")
	}
	friends, e := self.Friends()
	if e != nil {
		return nil, trace.failure(e)
	}
	rows := []string{}
	for _, f := range friends {
		rows = append(rows, f.NickName+"（备注："+f.RemarkName+"）")
	}
	return rows, nil
}

// Keep useful error categories without returning response bodies, login URLs or
// session credentials from the SDK.
func loginError(err error) error {
	switch {
	case errors.Is(err, openwechat.ErrForbidden):
		return errors.New("personal_wechat_login_denied")
	case errors.Is(err, openwechat.ErrLoginTimeout):
		return errors.New("personal_wechat_qr_expired")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return errors.New("personal_wechat_login_canceled")
	}
	var network net.Error
	if errors.As(err, &network) || openwechat.IsNetworkError(err) {
		return errors.New("personal_wechat_login_network_error")
	}
	var ret openwechat.Ret
	if errors.As(err, &ret) && (ret == 1203 || ret == 1205 || ret == 1100 || ret == 1101) {
		return errors.New("personal_wechat_login_denied")
	}
	if err != nil {
		text := strings.ToLower(err.Error())
		for _, marker := range []string{"不允许", "禁止登录", "环境异常", "login forbidden", "restricted from logging in", "不能登录网页版", "不能登录微信网页版"} {
			if strings.Contains(text, marker) {
				return errors.New("personal_wechat_login_denied")
			}
		}
	}
	return errors.New("personal_wechat_login_failed")
}

// Retain only fixed stage names, numeric status/return codes and the presence of
// a redirect. Never retain URLs, headers, response bodies or SDK error text.
type loginTrace struct {
	mu       sync.Mutex
	stage    string
	status   int
	ret      *int
	location bool
}

func (t *loginTrace) BeforeRequest(req *http.Request) {
	stage := "other"
	if req != nil && req.URL != nil {
		switch filepath.Base(req.URL.Path) {
		case "jslogin":
			stage = "qr"
		case "login":
			stage = "scan_poll"
		case "webwxnewloginpage":
			stage = "confirm"
		case "webwxinit":
			stage = "initialize"
		case "webwxstatusnotify":
			stage = "notify"
		case "webwxgetcontact":
			stage = "contacts"
		}
	}
	t.mu.Lock()
	t.stage, t.status, t.ret, t.location = stage, 0, nil, false
	t.mu.Unlock()
}

func (t *loginTrace) AfterRequest(resp *http.Response, _ error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if resp != nil {
		t.status = resp.StatusCode
		t.location = resp.Header.Get("Location") != ""
		if t.stage == "confirm" && resp.Body != nil {
			// Read only the return-code field, preserving the exact stream for the
			// SDK. No session fields or response text are decoded or retained.
			body := resp.Body
			prefix, err := io.ReadAll(io.LimitReader(body, 64<<10))
			rest := io.Reader(body)
			if err != nil {
				rest = loginReadError{err}
			}
			resp.Body = struct {
				io.Reader
				io.Closer
			}{io.MultiReader(bytes.NewReader(prefix), rest), body}
			if err == nil {
				var status struct {
					Ret *int `xml:"ret"`
				}
				if xml.Unmarshal(prefix, &status) == nil {
					t.ret = status.Ret
				}
			}
		}
	}
}

type loginReadError struct{ err error }

func (r loginReadError) Read([]byte) (int, error) { return 0, r.err }

func (t *loginTrace) failure(err error) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	code := loginError(err)
	if t.ret != nil {
		if typed := loginError(openwechat.Ret(*t.ret)); typed.Error() == "personal_wechat_login_denied" {
			code = typed
		}
		return fmt.Errorf("%s; stage=%s; http=%d; ret=%d; location=%t", code, t.stage, t.status, *t.ret, t.location)
	}
	return fmt.Errorf("%s; stage=%s; http=%d; location=%t", code, t.stage, t.status, t.location)
}
func New(ctx context.Context, cfg Config) (*Client, error) {
	if !filepath.IsAbs(cfg.Root) || !filepath.IsAbs(cfg.CookiePath) || cfg.FriendRemark == "" || cfg.OwnerAlias == "" {
		return nil, errors.New("personal_wechat_unconfigured")
	}
	if e := os.MkdirAll(filepath.Join(cfg.Root, "media"), 0700); e != nil {
		return nil, e
	}
	db, e := metadb.Open(filepath.Join(cfg.Root, "inbox.sqlite"))
	if e != nil {
		return nil, e
	}
	if _, e = db.Exec("CREATE TABLE IF NOT EXISTS inbox(id TEXT PRIMARY KEY,body BLOB NOT NULL,done INTEGER NOT NULL DEFAULT 0,created INTEGER NOT NULL)"); e != nil {
		return nil, e
	}
	c := &Client{cfg: cfg, db: db}
	bot := openwechat.New(ctx)
	openwechat.Desktop.Prepare(bot)
	c.bot = bot
	bot.UUIDCallback = func(string) {}
	// Never select the first sender. Only one exact contact remark is authorized.
	bot.MessageHandler = c.handleMessage
	storage := openwechat.NewFileHotReloadStorage(cfg.CookiePath)
	if e = bot.HotLogin(storage); e != nil {
		storage.Close()
		return nil, errors.New("personal_wechat_login_required")
	}
	self, e := bot.GetCurrentUser()
	if e != nil {
		return nil, e
	}
	friends, e := self.Friends()
	if e != nil {
		return nil, e
	}
	c.mu.Lock()
	for _, f := range friends {
		if f.RemarkName == cfg.FriendRemark {
			if c.friend != nil {
				c.mu.Unlock()
				bot.Exit()
				storage.Close()
				return nil, errors.New("personal_wechat_ambiguous_owner")
			}
			c.friend = f
		}
	}
	c.mu.Unlock()
	if c.friend == nil {
		bot.Exit()
		storage.Close()
		return nil, errors.New("personal_wechat_owner_not_found")
	}
	// SDK retains the storage; close it when the bot context exits.
	go func() { <-ctx.Done(); storage.Close() }()
	return c, nil
}
func (c *Client) handleMessage(m *openwechat.Message) {
	if m == nil {
		return
	}
	c.mu.Lock()
	allowed := c.friend != nil && m.FromUserName == c.friend.UserName
	c.mu.Unlock()
	if !allowed || strings.HasPrefix(m.FromUserName, "@@") || strings.HasPrefix(m.ToUserName, "@@") {
		return
	}
	if e := c.receive(m); e != nil {
		c.mu.Lock()
		c.err = e
		c.mu.Unlock()
	}
}
func (c *Client) receive(m *openwechat.Message) error {
	msg := weixin.Message{MessageID: weixin.ID("personal:" + m.MsgId), FromUserID: c.cfg.OwnerAlias, Type: 1, CreatedAt: m.CreateTime * 1000, ContextToken: "personal"}
	item := weixin.Item{Type: weixin.TextType, Text: &weixin.TextItem{Text: m.Content}}
	if m.IsPicture() || m.IsVoice() || m.IsVideo() || (m.IsMedia() && m.AppMsgType == openwechat.AppMsgTypeAttach) {
		res, e := m.GetFile()
		if e != nil {
			return errors.New("personal_media_download_failed")
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			return errors.New("personal_media_download_failed")
		}
		raw, e := io.ReadAll(io.LimitReader(res.Body, 25<<20+1))
		if e != nil || len(raw) > 25<<20 {
			return errors.New("personal_media_too_large")
		}
		sum := sha256.Sum256(raw)
		id := hex.EncodeToString(sum[:])
		path := filepath.Join(c.cfg.Root, "media", id)
		if e = os.WriteFile(path, raw, 0600); e != nil {
			return e
		}
		media := &weixin.Media{DownloadParam: id}
		switch {
		case m.IsPicture():
			item = weixin.Item{Type: weixin.ImageType, Image: &weixin.ImageItem{Media: media}}
		case m.IsVoice():
			item = weixin.Item{Type: weixin.VoiceType, Voice: &weixin.VoiceItem{Media: media, EncodeType: 6}}
		case m.IsVideo():
			item = weixin.Item{Type: weixin.VideoType, Video: &weixin.VideoItem{Media: media}}
		default:
			item = weixin.Item{Type: weixin.FileType, File: &weixin.FileItem{Media: media, Name: m.FileName}}
		}
	} else if m.IsMedia() {
		a, e := weixin.ParseAppMessage(m.Content)
		if e != nil {
			item.Text.Text = "[无法解析的微信卡片，请单独提供原件]"
		} else {
			msg.Sources = a.Sources
			item.Text.Text = a.Text
			item.Ref = a.Ref
		}
	}
	msg.Items = []weixin.Item{item}
	raw, e := json.Marshal(msg)
	if e != nil {
		return e
	}
	_, e = c.db.Exec("INSERT INTO inbox(id,body,created) VALUES(?,?,?) ON CONFLICT(id) DO NOTHING", msg.Key(), raw, time.Now().Unix())
	return e
}
func (c *Client) Drain(ctx context.Context, state *weixin.State, handler weixin.Handler, commit weixin.Commit) error {
	if state.Account.OwnerID != c.cfg.OwnerAlias {
		return errors.New("personal_owner_mismatch")
	}
	var id string
	var raw []byte
	e := c.db.QueryRow("SELECT id,body FROM inbox WHERE done=0 ORDER BY created,id LIMIT 1").Scan(&id, &raw)
	if errors.Is(e, sql.ErrNoRows) {
		c.mu.Lock()
		err := c.err
		c.err = nil
		c.mu.Unlock()
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
			return nil
		}
	}
	if e != nil {
		return e
	}
	var m weixin.Message
	if e = json.Unmarshal(raw, &m); e != nil {
		return e
	}
	state.Contexts[c.cfg.OwnerAlias] = "personal"
	if e = commit(state); e != nil {
		return e
	}
	if e = handler(ctx, m); e != nil {
		return e
	}
	_, e = c.db.Exec("UPDATE inbox SET done=1 WHERE id=?", id)
	return e
}
func (c *Client) Download(ctx context.Context, item weixin.Item) ([]byte, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	var m *weixin.Media
	if item.Image != nil {
		m = item.Image.Media
	}
	if item.File != nil {
		m = item.File.Media
	}
	if item.Voice != nil {
		m = item.Voice.Media
	}
	if item.Video != nil {
		m = item.Video.Media
	}
	if m == nil || len(m.DownloadParam) != 64 {
		return nil, errors.New("personal_media_missing")
	}
	if _, e := hex.DecodeString(m.DownloadParam); e != nil {
		return nil, e
	}
	raw, e := os.ReadFile(filepath.Join(c.cfg.Root, "media", m.DownloadParam))
	if e != nil {
		return nil, e
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != m.DownloadParam {
		return nil, errors.New("personal_media_integrity")
	}
	return raw, nil
}
func accepted(m *openwechat.SentMessage, e error) (weixin.SendResult, error) {
	if e != nil {
		return weixin.SendResult{}, errors.New("personal_wechat_send_failed")
	}
	if m == nil {
		return weixin.SendResult{}, errors.New("personal_wechat_send_unconfirmed")
	}
	return weixin.SendResult{MessageID: weixin.ID(m.MsgId)}, nil
}
func (c *Client) SendText(ctx context.Context, r weixin.Reply, text string) (weixin.SendResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return weixin.SendResult{}, e
	}
	if r.ToUserID != c.cfg.OwnerAlias {
		return weixin.SendResult{}, errors.New("unauthorized_personal_recipient")
	}
	m, e := c.friend.SendText(text)
	return accepted(m, e)
}
func (c *Client) Upload(ctx context.Context, to string, kind int, raw []byte) (weixin.Uploaded, error) {
	if ctx.Err() != nil {
		return weixin.Uploaded{}, ctx.Err()
	}
	if to != c.cfg.OwnerAlias || len(raw) > 25<<20 || (kind != weixin.UploadFile && kind != weixin.UploadImage) {
		return weixin.Uploaded{}, errors.New("invalid_personal_upload")
	}
	var id [16]byte
	if _, e := rand.Read(id[:]); e != nil {
		return weixin.Uploaded{}, e
	}
	s := hex.EncodeToString(id[:])
	e := os.WriteFile(filepath.Join(c.cfg.Root, "media", "upload-"+s), raw, 0600)
	return weixin.Uploaded{DownloadParam: s, Size: int64(len(raw))}, e
}
func (c *Client) sendMedia(ctx context.Context, r weixin.Reply, name string, u weixin.Uploaded, image bool) (weixin.SendResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ctx.Err() != nil {
		return weixin.SendResult{}, ctx.Err()
	}
	if r.ToUserID != c.cfg.OwnerAlias || len(u.DownloadParam) != 32 {
		return weixin.SendResult{}, errors.New("unauthorized_personal_media")
	}
	if _, e := hex.DecodeString(u.DownloadParam); e != nil {
		return weixin.SendResult{}, e
	}
	path := filepath.Join(c.cfg.Root, "media", "upload-"+u.DownloadParam)
	defer os.Remove(path)
	raw, e := os.ReadFile(path)
	if e != nil {
		return weixin.SendResult{}, e
	}
	if int64(len(raw)) != u.Size {
		return weixin.SendResult{}, errors.New("personal_upload_integrity")
	}
	if image {
		m, e := c.friend.SendImage(bytes.NewReader(raw))
		return accepted(m, e)
	}
	if filepath.Base(name) != name || strings.ContainsAny(name, "/\\\x00") {
		return weixin.SendResult{}, errors.New("invalid_personal_file_name")
	}
	dir, e := os.MkdirTemp(c.cfg.Root, "send-")
	if e != nil {
		return weixin.SendResult{}, e
	}
	defer os.RemoveAll(dir)
	file := filepath.Join(dir, name)
	if e = os.WriteFile(file, raw, 0600); e != nil {
		return weixin.SendResult{}, e
	}
	f, e := os.Open(file)
	if e != nil {
		return weixin.SendResult{}, e
	}
	defer f.Close()
	m, e := c.friend.SendFile(f)
	return accepted(m, e)
}
func (c *Client) SendImage(ctx context.Context, r weixin.Reply, u weixin.Uploaded) (weixin.SendResult, error) {
	return c.sendMedia(ctx, r, "image.jpg", u, true)
}
func (c *Client) SendFile(ctx context.Context, r weixin.Reply, name string, u weixin.Uploaded) (weixin.SendResult, error) {
	return c.sendMedia(ctx, r, name, u, false)
}
