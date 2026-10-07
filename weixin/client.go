package weixin

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var ErrSessionExpired = errors.New("微信授权已失效，请重新扫码登录")

type APIError struct {
	Operation string
	Ret, Code int
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s failed (ret=%d, errcode=%d)", e.Operation, e.Ret, e.Code)
}
func (e *APIError) Is(target error) bool {
	return target == ErrSessionExpired && (e.Ret == -14 || e.Code == -14)
}

type Options struct {
	BaseURL, LoginURL, CDNURL, Token      string
	HTTPClient                            *http.Client
	APITimeout, PollTimeout, MediaTimeout time.Duration
	MaxMediaBytes                         int64
	// For local protocol tests only. This accepts loopback hosts, never arbitrary HTTP.
	AllowLocalHTTP bool
}

type Client struct {
	opts Options
	http *http.Client
}

func New(opts Options) (*Client, error) {
	if opts.BaseURL == "" {
		opts.BaseURL = DefaultAPI
	}
	if opts.LoginURL == "" {
		opts.LoginURL = DefaultAPI
	}
	if opts.CDNURL == "" {
		opts.CDNURL = DefaultCDN
	}
	if opts.APITimeout <= 0 {
		opts.APITimeout = 15 * time.Second
	}
	if opts.PollTimeout <= 0 {
		opts.PollTimeout = 40 * time.Second
	}
	if opts.MediaTimeout <= 0 {
		opts.MediaTimeout = 2 * time.Minute
	}
	if opts.MaxMediaBytes <= 0 {
		opts.MaxMediaBytes = 25 * 1024 * 1024
	}
	if opts.MaxMediaBytes > 100*1024*1024 {
		return nil, errors.New("media limit exceeds 100 MiB")
	}
	opts.Token = strings.TrimSpace(opts.Token)
	if strings.ContainsAny(opts.Token, "\r\n") {
		return nil, errors.New("invalid bot credential")
	}
	client := &Client{opts: opts}
	for _, raw := range []string{opts.BaseURL, opts.LoginURL, opts.CDNURL} {
		if err := client.validateURL(raw, true); err != nil {
			return nil, err
		}
	}
	h := http.Client{}
	if opts.HTTPClient != nil {
		h = *opts.HTTPClient
	}
	// An HTTP redirect must never forward bot authorization or signed CDN requests.
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client.http = &h
	return client, nil
}

func (c *Client) validateURL(raw string, base bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || (base && u.RawQuery != "") {
		return errors.New("invalid service URL")
	}
	host := strings.ToLower(u.Hostname())
	ip := net.ParseIP(host)
	loopback := host == "localhost" || (ip != nil && ip.IsLoopback())
	if c.opts.AllowLocalHTTP && loopback && (u.Scheme == "http" || u.Scheme == "https") {
		return nil
	}
	if u.Scheme != "https" || !(host == "weixin.qq.com" || strings.HasSuffix(host, ".weixin.qq.com")) || (u.Port() != "" && u.Port() != "443") {
		return errors.New("service URL must use HTTPS on a trusted Weixin host")
	}
	return nil
}

func info() BaseInfo {
	return BaseInfo{ChannelVersion: ProtocolVersion, BotAgent: "CampusWechatGo/0.1.0"}
}

func randomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "campus-go-" + hex.EncodeToString(b[:]), nil
}

func (s apiStatus) check(operation string) error {
	if s.Ret != 0 || s.ErrCode != 0 {
		return &APIError{operation, s.Ret, s.ErrCode}
	}
	return nil
}

func readBounded(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errors.New("response exceeds configured size limit")
	}
	return b, nil
}

// Network errors deliberately omit the URL and server body, which may contain credentials.
func requestError(ctx context.Context, operation string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return fmt.Errorf("%s network request failed: %w", operation, err)
}

func (c *Client) jsonRequest(ctx context.Context, base, endpoint, method string, body any, authenticated bool, timeout time.Duration, out any) error {
	if err := c.validateURL(base, true); err != nil {
		return err
	}
	if authenticated && c.opts.Token == "" {
		return errors.New("bot credential is required")
	}
	var encoded []byte
	var err error
	if body != nil {
		encoded, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, method, strings.TrimRight(base, "/")+"/"+endpoint, bytes.NewReader(encoded))
	if err != nil {
		return errors.New("cannot construct API request")
	}
	req.Header.Set("iLink-App-Id", "bot")
	req.Header.Set("iLink-App-ClientVersion", "132105")
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("AuthorizationType", "ilink_bot_token")
		var b [4]byte
		if _, err := rand.Read(b[:]); err != nil {
			return err
		}
		req.Header.Set("X-WECHAT-UIN", base64.StdEncoding.EncodeToString([]byte(strconv.FormatUint(uint64(binary.BigEndian.Uint32(b[:])), 10))))
	}
	if authenticated {
		req.Header.Set("Authorization", "Bearer "+c.opts.Token)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return requestError(callCtx, strings.Split(endpoint, "?")[0], err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("Weixin API returned HTTP %d", res.StatusCode)
	}
	data, err := readBounded(res.Body, 4*1024*1024)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, out); err != nil {
		return errors.New("invalid Weixin API response JSON")
	}
	return nil
}

func (c *Client) GetUpdates(ctx context.Context, cursor string) (Updates, error) {
	var out Updates
	body := struct {
		Cursor string   `json:"get_updates_buf"`
		Info   BaseInfo `json:"base_info"`
	}{cursor, info()}
	err := c.jsonRequest(ctx, c.opts.BaseURL, "ilink/bot/getupdates", http.MethodPost, body, true, c.opts.PollTimeout, &out)
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return Updates{Cursor: cursor}, nil
	}
	if err != nil {
		return out, err
	}
	return out, out.apiStatus.check("getUpdates")
}

func (c *Client) send(ctx context.Context, reply Reply, item Item) (SendResult, error) {
	var out SendResult
	if reply.ToUserID == "" || reply.ContextToken == "" {
		return out, errors.New("reply requires recipient and inbound context token")
	}
	if reply.ClientID == "" {
		var err error
		reply.ClientID, err = randomID()
		if err != nil {
			return out, err
		}
	}
	body := struct {
		Message Message  `json:"msg"`
		Info    BaseInfo `json:"base_info"`
	}{
		Message{ToUserID: reply.ToUserID, ClientID: reply.ClientID, Type: 2, State: 2, Items: []Item{item}, ContextToken: reply.ContextToken, RunID: reply.RunID}, info(),
	}
	err := c.jsonRequest(ctx, c.opts.BaseURL, "ilink/bot/sendmessage", http.MethodPost, body, true, c.opts.APITimeout, &out)
	out.ClientID = reply.ClientID
	if err != nil {
		return out, err
	}
	return out, out.apiStatus.check("sendMessage")
}

func (c *Client) SendText(ctx context.Context, reply Reply, text string) (SendResult, error) {
	if strings.TrimSpace(text) == "" || len(text) > 64*1024 {
		return SendResult{}, errors.New("text must be nonempty and at most 64 KiB")
	}
	return c.send(ctx, reply, Item{Type: TextType, Text: &TextItem{Text: text}})
}
