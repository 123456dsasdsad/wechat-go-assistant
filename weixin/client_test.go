package weixin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testClient(t *testing.T, handler http.HandlerFunc, opts Options) *Client {
	t.Helper()
	s := httptest.NewServer(handler)
	t.Cleanup(s.Close)
	opts.BaseURL = s.URL
	opts.LoginURL = s.URL
	opts.CDNURL = s.URL + "/c2c"
	opts.AllowLocalHTTP = true
	if opts.Token == "" {
		opts.Token = "secret-token"
	}
	c, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestProtocolHeadersCursorAndLosslessIDs(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/ilink/bot/getupdates" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer secret-token" || r.Header.Get("AuthorizationType") != "ilink_bot_token" {
			t.Error("missing bot authentication")
		}
		if r.Header.Get("iLink-App-Id") != "bot" || r.Header.Get("iLink-App-ClientVersion") != "132105" {
			t.Error("incorrect compatibility version")
		}
		if _, err := base64.StdEncoding.DecodeString(r.Header.Get("X-WECHAT-UIN")); err != nil {
			t.Error(err)
		}
		var body struct {
			Cursor string   `json:"get_updates_buf"`
			Info   BaseInfo `json:"base_info"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Cursor != "saved-cursor" || body.Info.BotAgent != "CampusWechatGo/0.1.0" {
			t.Errorf("unexpected request body: %+v", body)
		}
		w.Write([]byte(`{"ret":0,"get_updates_buf":"next","msgs":[{"message_id":18446744073709551615,"message_type":1,"from_user_id":"owner","context_token":"ctx","item_list":[{"type":1,"text_item":{"text":"你好"}}]}]}`))
	}, Options{})
	u, err := c.GetUpdates(context.Background(), "saved-cursor")
	if err != nil {
		t.Fatal(err)
	}
	if u.Cursor != "next" || string(u.Messages[0].MessageID) != "18446744073709551615" {
		t.Fatalf("IDs or cursor lost: %+v", u)
	}
}

func TestReplyPreservesContextAndDoesNotRetry(t *testing.T) {
	count := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		count++
		var body struct {
			Message Message `json:"msg"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		m := body.Message
		if m.ContextToken != "ctx" || m.ClientID != "stable-id" || m.ToUserID != "owner" || m.Type != 2 || m.State != 2 {
			t.Errorf("incorrect reply: %+v", m)
		}
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("secret-token ctx"))
	}, Options{})
	_, err := c.SendText(context.Background(), Reply{ToUserID: "owner", ContextToken: "ctx", ClientID: "stable-id"}, "你好")
	if err == nil || count != 1 {
		t.Fatalf("must return uncertain send once: %v count=%d", err, count)
	}
	if strings.Contains(err.Error(), "secret-token") || strings.Contains(err.Error(), "ctx") {
		t.Fatal("error leaked credentials")
	}
	_, err = c.SendText(context.Background(), Reply{ToUserID: "owner"}, "hello")
	if err == nil || count != 1 {
		t.Fatal("missing reply context was sent")
	}
}

func TestSessionExpiryAndPollCancellation(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ret":0,"errcode":-14,"errmsg":"secret-token"}`))
	}, Options{})
	_, err := c.GetUpdates(context.Background(), "")
	if !errors.Is(err, ErrSessionExpired) || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("incorrect session error %v", err)
	}
	c = testClient(t, func(w http.ResponseWriter, r *http.Request) { io.Copy(io.Discard, r.Body); <-r.Context().Done() }, Options{PollTimeout: 20 * time.Millisecond})
	u, err := c.GetUpdates(context.Background(), "cursor")
	if err != nil || u.Cursor != "cursor" {
		t.Fatalf("internal timeout should preserve cursor: %+v %v", u, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = c.GetUpdates(ctx, "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("parent cancellation swallowed: %v", err)
	}
}

func TestRejectCredentialRedirectAndUntrustedURL(t *testing.T) {
	for _, endpoint := range []string{"http://example.com", "https://weixin.qq.com.evil.example", "https://user:pass@ilinkai.weixin.qq.com", "https://ilinkai.weixin.qq.com/?token=secret"} {
		if _, err := New(Options{BaseURL: endpoint}); err == nil {
			t.Errorf("accepted untrusted endpoint %s", endpoint)
		}
	}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://example.com/")
		w.WriteHeader(302)
	}, Options{})
	if _, err := c.GetUpdates(context.Background(), ""); err == nil {
		t.Fatal("followed credential redirect")
	}
}
