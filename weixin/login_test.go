package weixin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLoginVerificationThenConfirmation(t *testing.T) {
	polls, shown := 0, 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("bot token sent during QR login")
		}
		if strings.Contains(r.URL.Path, "get_bot_qrcode") {
			if r.Method != "POST" || r.URL.Query().Get("bot_type") != "3" {
				t.Error("incorrect QR request")
			}
			var req struct {
				Tokens []string `json:"local_token_list"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			if req.Tokens == nil {
				t.Error("tokens encoded as null")
			}
			w.Write([]byte(`{"qrcode":"qr-code","qrcode_img_content":"https://weixin.qq.com/qr"}`))
			return
		}
		if r.Method != "GET" || r.URL.Query().Get("qrcode") != "qr-code" || r.Header.Get("X-WECHAT-UIN") != "" {
			t.Error("incorrect QR status request")
		}
		polls++
		if polls == 1 {
			w.Write([]byte(`{"status":"need_verifycode"}`))
			return
		}
		if r.URL.Query().Get("verify_code") != "1234" {
			t.Error("verification missing")
		}
		w.Write([]byte(`{"status":"confirmed","bot_token":"credential","ilink_bot_id":"bot","ilink_user_id":"owner","baseurl":"https://ilinkai.weixin.qq.com"}`))
	}, Options{})
	account, err := c.Login(context.Background(), LoginCallbacks{QRCode: func(q QRCode) error { shown++; return nil }, VerifyCode: func(context.Context) (string, error) { return "1234", nil }}, nil)
	if err != nil || account.OwnerID != "owner" || account.BotToken != "credential" || shown != 1 {
		t.Fatalf("login failed %+v %v", account, err)
	}
}

func TestLoginRegionRedirect(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "get_bot_qrcode"):
			w.Write([]byte(`{"qrcode":"q","qrcode_img_content":"https://weixin.qq.com/qr"}`))
		case r.URL.Path == "/first/ilink/bot/get_qrcode_status":
			json.NewEncoder(w).Encode(QRStatus{Status: "scaned_but_redirect", RedirectHost: strings.TrimPrefix(server.URL, "https://")})
		default:
			json.NewEncoder(w).Encode(QRStatus{Status: "confirmed", BotToken: "token", BotID: "bot", OwnerID: "owner", BaseURL: server.URL})
		}
	}))
	defer server.Close()
	c, err := New(Options{LoginURL: server.URL + "/first", AllowLocalHTTP: true, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	account, err := c.Login(context.Background(), LoginCallbacks{}, nil)
	if err != nil || account.BaseURL != server.URL {
		t.Fatalf("region redirect failed: %v", err)
	}
}

func TestLoginRefreshAndIncompleteCredentials(t *testing.T) {
	refreshes := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "get_bot_qrcode") {
			refreshes++
			w.Write([]byte(`{"qrcode":"q","qrcode_img_content":"qr"}`))
			return
		}
		w.Write([]byte(`{"status":"expired"}`))
	}, Options{})
	if _, err := c.Login(context.Background(), LoginCallbacks{}, nil); err == nil || refreshes != 4 {
		t.Fatalf("refresh bound: count=%d err=%v", refreshes, err)
	}
	for _, response := range []string{`{"status":"confirmed","ilink_bot_id":"bot"}`, `{"status":"scaned_but_redirect","redirect_host":"example.com"}`, `{"status":"binded_redirect"}`, `{"status":"verify_code_blocked"}`} {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "get_bot_qrcode") {
				w.Write([]byte(`{"qrcode":"q","qrcode_img_content":"qr"}`))
				return
			}
			w.Write([]byte(response))
		}, Options{})
		_, err := c.Login(context.Background(), LoginCallbacks{}, nil)
		if err == nil {
			t.Errorf("invalid login accepted %s", response)
		}
		if strings.Contains(response, "binded_redirect") && !errors.Is(err, ErrAlreadyBound) {
			t.Errorf("already bound incorrectly handled %v", err)
		}
	}
}
