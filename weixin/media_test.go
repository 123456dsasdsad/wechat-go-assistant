package weixin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEncryptedFilePipeline(t *testing.T) {
	plain := []byte("CSV 数据,结果\n1,2\n")
	var key []byte
	var ciphertext []byte
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ilink/bot/getuploadurl":
			var body struct {
				Kind       int    `json:"media_type"`
				AESKey     string `json:"aeskey"`
				RawSize    int    `json:"rawsize"`
				CipherSize int    `json:"filesize"`
				NoThumb    bool   `json:"no_need_thumb"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			key, _ = hex.DecodeString(body.AESKey)
			if body.Kind != UploadFile || body.RawSize != len(plain) || body.CipherSize%16 != 0 || !body.NoThumb {
				t.Error("bad upload metadata")
			}
			json.NewEncoder(w).Encode(map[string]string{"upload_full_url": server.URL + "/custom-upload?signature=secret", "upload_param": "ignored"})
		case "/custom-upload":
			if r.Header.Get("Authorization") != "" || r.Header.Get("X-WECHAT-UIN") != "" {
				t.Error("bot auth leaked to CDN")
			}
			ciphertext, _ = io.ReadAll(r.Body)
			got, err := decryptECB(ciphertext, key)
			if err != nil || !bytes.Equal(got, plain) {
				t.Error("incorrect CDN encryption")
			}
			w.Header().Set("x-encrypted-param", "download-param")
		case "/ilink/bot/sendmessage":
			var req struct {
				Msg Message `json:"msg"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			item := req.Msg.Items[0]
			if item.Type != FileType || item.File.Name != "report.csv" || item.File.Media.DownloadParam != "download-param" {
				t.Error("incorrect file message")
			}
			got, err := decodeMediaKey(item.File.Media.AESKey)
			if err != nil || !bytes.Equal(got, key) {
				t.Error("incorrect outgoing key encoding")
			}
			w.Write([]byte(`{"ret":0,"message_id":"42"}`))
		case "/custom-download":
			if r.Header.Get("Authorization") != "" {
				t.Error("bot token leaked to download")
			}
			w.Write(ciphertext)
		default:
			t.Errorf("wrong CDN URL selection %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	c, err := New(Options{BaseURL: server.URL, CDNURL: server.URL, Token: "token", AllowLocalHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	file, err := c.Upload(context.Background(), "owner", UploadFile, plain)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.SendFile(context.Background(), Reply{ToUserID: "owner", ContextToken: "ctx"}, "report.csv", file); err != nil {
		t.Fatal(err)
	}
	item := Item{Type: FileType, File: &FileItem{Media: &Media{FullURL: server.URL + "/custom-download", DownloadParam: "ignored", AESKey: base64.StdEncoding.EncodeToString(key)}}}
	got, err := c.Download(context.Background(), item)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("download roundtrip failed: %v", err)
	}
}

func TestUploadRetryAndLimit(t *testing.T) {
	for _, status := range []int{400, 500, 200} {
		count := 0
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/ilink/bot/getuploadurl" {
				w.Write([]byte(`{"upload_param":"signature"}`))
				return
			}
			count++
			w.WriteHeader(status)
		}, Options{MaxMediaBytes: 32})
		if _, err := c.Upload(context.Background(), "owner", UploadFile, []byte("data")); err == nil {
			t.Error("accepted failed upload or missing download header")
		}
		want := 3
		if status == 400 {
			want = 1
		}
		if count != want {
			t.Errorf("status %d retry count %d want %d", status, count, want)
		}
		if _, err := c.Upload(context.Background(), "owner", UploadFile, make([]byte, 33)); err == nil || count != want {
			t.Error("oversized file was uploaded")
		}
	}
}

func TestDownloadSizeAndKeyPriority(t *testing.T) {
	key := bytes.Repeat([]byte{1}, 16)
	cipher, _ := encryptECB([]byte("small"), key)
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("encrypted_query_param") != "a+b/=signed" {
			t.Error("download param was corrupted")
		}
		w.Write(cipher)
	}, Options{MaxMediaBytes: 5})
	item := Item{Type: ImageType, Image: &ImageItem{AESKeyHex: hex.EncodeToString(key), Media: &Media{DownloadParam: "a+b/=signed", AESKey: "invalid-ignored"}}}
	got, err := c.Download(context.Background(), item)
	if err != nil || string(got) != "small" {
		t.Fatalf("image key priority failed: %v", err)
	}
	item = Item{Type: FileType, File: &FileItem{Length: "9999", Media: &Media{AESKey: base64.StdEncoding.EncodeToString(key), DownloadParam: "x"}}}
	if _, err := c.Download(context.Background(), item); err == nil {
		t.Error("oversized declared length accepted")
	}
	if _, err := c.Download(context.Background(), Item{Type: FileType, File: &FileItem{Media: &Media{FullURL: "https://example.com"}}}); err == nil {
		t.Error("untrusted media accepted")
	}
}
