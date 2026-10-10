package personalwechat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
	"github.com/eatmoreapple/openwechat"
)

func TestLoginErrorsRemainActionableWithoutExposingCredentials(t *testing.T) {
	for _, row := range []struct {
		input error
		want  string
	}{
		{fmt.Errorf("wrapped: %w", openwechat.ErrForbidden), "personal_wechat_login_denied"},
		{openwechat.ErrLoginTimeout, "personal_wechat_qr_expired"},
		{context.Canceled, "personal_wechat_login_canceled"},
		{openwechat.Ret(1203), "personal_wechat_login_denied"},
		{&url.Error{Op: "Get", URL: "https://example.invalid/?session=private", Err: context.DeadlineExceeded}, "personal_wechat_login_canceled"},
		{errors.New("微信帐号不允许登录。private-session"), "personal_wechat_login_denied"},
		{errors.New("This account is currently restricted from logging in to WeChat for Web. private-session"), "personal_wechat_login_denied"},
		{errors.New("private-session https://example.invalid/credential"), "personal_wechat_login_failed"},
	} {
		if got := loginError(row.input).Error(); got != row.want {
			t.Fatal(got, row.want)
		}
	}
}

func TestLoginTraceOnlyReturnsFixedStageAndHTTPStatus(t *testing.T) {
	trace := &loginTrace{stage: "start"}
	req, err := http.NewRequest("GET", "https://example.invalid/cgi-bin/mmwebwx-bin/webwxnewloginpage?ticket=private-session", strings.NewReader("private-body"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Cookie", "private-cookie")
	trace.BeforeRequest(req)
	trace.AfterRequest(&http.Response{StatusCode: 403, Request: req}, errors.New("private-network-error"))
	got := trace.failure(errors.New("private-sdk-response")).Error()
	if got != "personal_wechat_login_failed; stage=confirm; http=403; location=false" {
		t.Fatal(got)
	}
	trace.BeforeRequest(req)
	trace.AfterRequest(nil, errors.New("private-network-error"))
	if got := trace.failure(openwechat.NetworkErr).Error(); got != "personal_wechat_login_network_error; stage=confirm; http=0; location=false" {
		t.Fatal(got)
	}
	unknown, _ := http.NewRequest("GET", "https://example.invalid/private-path?ticket=private-session", nil)
	trace.BeforeRequest(unknown)
	trace.AfterRequest(&http.Response{StatusCode: 200}, nil)
	if got := trace.failure(errors.New("private-sdk-response")).Error(); got != "personal_wechat_login_failed; stage=other; http=200; location=false" {
		t.Fatal(got)
	}
}

func TestLoginTracePreservesSDKStreamAndOnlyRetainsReturnCode(t *testing.T) {
	trace := &loginTrace{}
	req, _ := http.NewRequest("GET", "https://example.invalid/cgi-bin/mmwebwx-bin/webwxnewloginpage?ticket=private", nil)
	for _, body := range []string{
		`<error><ret>1203</ret><message>private-server-message</message><skey>private-session</skey></error>`,
		strings.Repeat("invalid-response", 7000),
	} {
		trace.BeforeRequest(req)
		resp := &http.Response{StatusCode: 301, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
		trace.AfterRequest(resp, nil)
		preserved, err := io.ReadAll(resp.Body)
		if err != nil || string(preserved) != body {
			t.Fatal("SDK response stream changed", err)
		}
		got := trace.failure(errors.New("private-sdk-message")).Error()
		if strings.Contains(got, "private") || strings.Contains(got, "invalid-response") {
			t.Fatal("response data leaked", got)
		}
		if strings.HasPrefix(body, "<error>") && got != "personal_wechat_login_denied; stage=confirm; http=301; ret=1203; location=false" {
			t.Fatal(got)
		}
	}
	trace.BeforeRequest(req)
	resp := &http.Response{StatusCode: 301, Body: io.NopCloser(io.MultiReader(strings.NewReader("partial-response"), loginReadError{io.ErrUnexpectedEOF}))}
	trace.AfterRequest(resp, nil)
	partial, err := io.ReadAll(resp.Body)
	if string(partial) != "partial-response" || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal("SDK response read error was swallowed", err)
	}
}

func inboxFixture(t *testing.T, root string) *Client {
	t.Helper()
	db, e := metadb.Open(filepath.Join(root, "inbox.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("CREATE TABLE IF NOT EXISTS inbox(id TEXT PRIMARY KEY,body BLOB NOT NULL,done INTEGER NOT NULL DEFAULT 0,created INTEGER NOT NULL)"); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	return &Client{cfg: Config{Root: root, OwnerAlias: "owner"}, db: db, friend: &openwechat.Friend{User: &openwechat.User{UserName: "@selected-owner"}}}
}

func TestOnlyBoundDirectContactCanEnterPersonalInbox(t *testing.T) {
	c := inboxFixture(t, t.TempDir())
	for _, from := range []string{"@other-friend", "@@group", "@self", ""} {
		c.handleMessage(&openwechat.Message{MsgId: "ignored-" + from, FromUserName: from, MsgType: openwechat.MsgTypeText, Content: "开始收集 或执行命令"})
	}
	c.handleMessage(nil)
	c.handleMessage(&openwechat.Message{MsgId: "group-target", FromUserName: "@selected-owner", ToUserName: "@@group", MsgType: openwechat.MsgTypeText, Content: "群消息不得处理"})
	selected := c.friend
	c.friend = nil
	c.handleMessage(&openwechat.Message{MsgId: "before-binding", FromUserName: "@selected-owner", MsgType: openwechat.MsgTypeText, Content: "未绑定时不得处理"})
	c.friend = selected
	var n int
	if e := c.db.QueryRow("SELECT COUNT(*) FROM inbox").Scan(&n); e != nil || n != 0 {
		t.Fatal("unbound, foreign or group message entered the assistant", n, e)
	}
	message := &openwechat.Message{MsgId: "owned", FromUserName: "@selected-owner", MsgType: openwechat.MsgTypeText, Content: "我的测试指令"}
	c.handleMessage(message)
	c.handleMessage(message)
	if e := c.db.QueryRow("SELECT COUNT(*) FROM inbox").Scan(&n); e != nil || n != 1 {
		t.Fatal("owner message lost or duplicated", n, e)
	}
}

func TestPersonalForwardPersistsAndRetriesWithoutLosingSources(t *testing.T) {
	root := t.TempDir()
	c := inboxFixture(t, root)
	raw := `<msg><appmsg><type>19</type><recorditem><![CDATA[<recordinfo><datalist><dataitem datatype="1"><datadesc>周五提交记录</datadesc><sourcename>导师</sourcename><sourcetime>今天10点</sourcetime></dataitem><dataitem datatype="8"><datatitle>代码.zip</datatitle></dataitem></datalist></recordinfo>]]></recorditem></appmsg></msg>`
	c.handleMessage(&openwechat.Message{MsgId: "forward", FromUserName: "@selected-owner", MsgType: openwechat.MsgTypeApp, AppMsgType: 19, Content: raw})
	state := weixin.NewState(weixin.Account{OwnerID: "owner"})
	commitFail := func(*weixin.State) error { return errors.New("fixture storage unavailable") }
	called := false
	if e := c.Drain(context.Background(), state, func(context.Context, weixin.Message) error { called = true; return nil }, commitFail); e == nil || called {
		t.Fatal("processing preceded durable state", e, called)
	}
	commit := func(*weixin.State) error { return nil }
	if e := c.Drain(context.Background(), state, func(context.Context, weixin.Message) error { return errors.New("fixture retry") }, commit); e == nil {
		t.Fatal("failed handler acknowledged message")
	}
	c.db.Close()
	c = inboxFixture(t, root)
	if e := c.Drain(context.Background(), state, func(_ context.Context, m weixin.Message) error {
		if len(m.Sources) != 2 || m.Sources[0].Text != "周五提交记录" || m.Sources[0].Speaker != "导师" || len(m.Sources[1].Missing) != 1 {
			t.Fatal("forward provenance or missing-original marker lost", m.Sources)
		}
		return nil
	}, commit); e != nil {
		t.Fatal(e)
	}
	var n int
	if e := c.db.QueryRow("SELECT COUNT(*) FROM inbox WHERE done=0").Scan(&n); e != nil || n != 0 {
		t.Fatal("successful message replayed", n, e)
	}
	foreign := weixin.NewState(weixin.Account{OwnerID: "other"})
	if e := c.Drain(context.Background(), foreign, nil, nil); e == nil {
		t.Fatal("foreign owner drained inbox")
	}
}
