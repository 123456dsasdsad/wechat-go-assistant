package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
)

func TestCLILockAndReplyRequiresInboundContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	release, err := lockState(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockState(path); err == nil {
		t.Fatal("concurrent state writer admitted")
	}
	release()
	release, err = lockState(path)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := os.Stat(path + ".lock"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("lock not released")
	}
	if err := weixin.SaveState(path, weixin.NewState(weixin.Account{BotToken: "test-token", BotID: "bot", OwnerID: "owner", BaseURL: weixin.DefaultAPI})); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"reply", "-state", path, "-text", "hello"}); err == nil {
		t.Fatal("reply without inbound context was allowed")
	}
	if err := run(context.Background(), []string{"unexpected"}); err == nil {
		t.Fatal("unknown command accepted")
	}
}

func TestQRPathCannotOverwriteCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	original := []byte("existing private credentials")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := login(context.Background(), path, path); err == nil {
		t.Fatal("QR allowed to overwrite credentials")
	}
	if err := login(context.Background(), path, path+".lock"); err == nil {
		t.Fatal("QR allowed to overwrite state lock")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(original) {
		t.Fatal("original credentials changed")
	}
}
