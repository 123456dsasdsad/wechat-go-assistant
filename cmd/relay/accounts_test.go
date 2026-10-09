package main

import (
	"context"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/accountupload"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAccountUploadCommandNeverCreatesAIJobOrFileGrant(t *testing.T) {
	defer metadb.CloseAll()
	for _, command := range []string{"管理账号", "账号管理", "管理帐号", "上传账号", "账号上传", "上传帐号", "帐号上传", "上传账户", "账户上传", " 上传 帐号。 ", "/上传帐号", "上传帐号！", "上传\n帐号", "上传　账户？"} {
		t.Run(command, func(t *testing.T) { defer metadb.CloseAll(); checkAccountUploadCommand(t, command) })
	}
}

func checkAccountUploadCommand(t *testing.T, command string) {
	t.Helper()
	in := inboundFixture(t)
	store, e := accountupload.Open(filepath.Join(t.TempDir(), "accounts"))
	if e != nil {
		t.Fatal(e)
	}
	in.accounts = store
	original := in.sessions.Current().ID
	if e = in.handle(context.Background(), textMessage("account-upload", command)); e != nil {
		t.Fatal(e)
	}
	reply := in.client.(*fakeMessages).text
	marker := in.publicURL + "accounts/#"
	if !strings.Contains(reply, marker) {
		t.Fatal("dedicated link not sent")
	}
	token := strings.Split(strings.Split(reply, marker)[1], "\n")[0]
	if owner, ok := store.Authorize(token); !ok || owner != "owner" {
		t.Fatal("account grant unavailable")
	}
	if _, ok := in.files.Authorize(token); ok {
		t.Fatal("account grant can access ordinary files")
	}
	if len(in.queue.History()) != 0 || in.sessions.Current().ID != original {
		t.Fatal("upload command created AI job or changed session")
	}
	for _, status := range []string{"上传账号状态", "账号上传状态", "上传帐号状态", "帐号上传状态", "上传账户状态", "账户上传状态", " 上传 帐号 状态。 ", "/帐号上传状态"} {
		if e = in.handle(context.Background(), textMessage("account-upload-status-"+status, status)); e != nil {
			t.Fatal(e)
		}
		if !strings.Contains(in.client.(*fakeMessages).text, "还没有账号上传记录") || len(in.queue.History()) != 0 || in.sessions.Current().ID != original {
			t.Fatalf("account status sent to AI: %q", status)
		}
	}
	if task, _ := in.queue.Claim(time.Now()); task != nil {
		t.Fatal("account status called AI")
	}
}

func TestAccountUploadUnavailableNeverFallsBackToAI(t *testing.T) {
	defer metadb.CloseAll()
	for _, command := range []string{"上传帐号", "帐号上传状态"} {
		in := inboundFixture(t)
		if e := in.handle(context.Background(), textMessage("unavailable", command)); e != nil {
			t.Fatal(e)
		}
		if !strings.Contains(in.client.(*fakeMessages).text, "尚未配置") || len(in.queue.History()) != 0 {
			t.Fatalf("unconfigured account upload sent to AI: %q", command)
		}
	}
}

func TestAccountUploadMatchingPreservesOrdinaryPrompt(t *testing.T) {
	defer metadb.CloseAll()
	in := inboundFixture(t)
	prompt := "解释一下上传 帐号的流程？"
	if e := in.handle(context.Background(), textMessage("ordinary", prompt)); e != nil {
		t.Fatal(e)
	}
	j, _ := in.queue.Claim(time.Now())
	if j == nil || j.Input != prompt {
		t.Fatal("ordinary AI question was altered or intercepted")
	}
}
