package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
)

func transcriptionTestHelper() {
	if os.Getenv("WECHAT_TEST_TRANSCRIPTION_HELPER") == "1" {
		if len(os.Args) != 3 {
			os.Exit(2)
		}
		raw, e := os.ReadFile(os.Args[1])
		if e != nil || string(raw) != "actual recording bytes" {
			os.Exit(3)
		}
		data := []byte(`{"engine":"test-cpu","duration":2,"segments":[{"start":0,"end":1.5,"text":"actual test transcript"}]}`)
		if os.WriteFile(os.Args[2], data, 0600) != nil {
			os.Exit(4)
		}
		os.Exit(0)
	}
}

func TestStagedRecordingUsesOriginalFileForTranscription(t *testing.T) {
	t.Setenv("WECHAT_TEST_TRANSCRIPTION_HELPER", "1")
	raw := "actual recording bytes"
	sum := sha256.Sum256([]byte(raw))
	ref := files.Ref{ID: strings.Repeat("a", 24), Name: "voice.flac", Size: int64(len(raw)), SHA256: hex.EncodeToString(sum[:])}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(ref.Size))
		w.Header().Set("X-File-SHA256", ref.SHA256)
		fmt.Fprint(w, raw)
	}))
	defer server.Close()
	conversation := t.TempDir()
	dir := filepath.Join(conversation, "turns", "job")
	os.MkdirAll(dir, 0700)
	staged, e := downloadAttachment(context.Background(), server.URL, "key", "job", "lease", dir, ref)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.ToSlash(filepath.Join("turns", "job", staged, "original", ref.Name))
	exe, _ := os.Executable()
	rows, e := transcribeInput(context.Background(), config{TranscriptionCommand: exe}, conversation, path)
	if e != nil || len(rows) != 2 {
		t.Fatal(rows, e)
	}
	var transcript transcript
	b, _ := os.ReadFile(filepath.Join(conversation, filepath.FromSlash(rows[1]["path"])))
	if json.Unmarshal(b, &transcript) != nil || transcript.Engine != "test-cpu" {
		t.Fatal("transcript not registered")
	}
	if attachmentError(fmt.Errorf("audio_transcription_failed")) != "audio_transcription_failed" {
		t.Fatal("transcription failure hidden")
	}
}
