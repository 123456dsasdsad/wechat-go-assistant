package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPrivateAttachmentIntegrity(t *testing.T) {
	content := "actual campus input"
	sum := sha256.Sum256([]byte(content))
	ref := files.Ref{ID: strings.Repeat("a", 24), Name: "note.txt", Size: int64(len(content)), SHA256: hex.EncodeToString(sum[:])}
	var corrupt atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer protected" || r.Header.Get("X-Job-Lease") != "lease" {
			t.Error("private download lost auth")
		}
		w.Header().Set("Content-Length", fmt.Sprint(ref.Size))
		w.Header().Set("X-File-SHA256", ref.SHA256)
		if corrupt.Load() {
			fmt.Fprint(w, strings.Repeat("x", len(content)))
			return
		}
		fmt.Fprint(w, content)
	}))
	defer server.Close()
	dir := t.TempDir()
	result, err := downloadAttachment(context.Background(), server.URL, "protected", "job", "lease", dir, ref)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(result), "original", "note.txt"))
	if err != nil || string(actual) != content {
		t.Fatal("attachment was not staged intact", err)
	}
	corrupt.Store(true)
	if _, err = downloadAttachment(context.Background(), server.URL, "protected", "job", "lease", t.TempDir(), ref); err == nil || err.Error() != "attachment_integrity_failed" {
		t.Fatal("corrupt body accepted", err)
	}
}
