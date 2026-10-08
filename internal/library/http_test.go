package library

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStreamingTransfersUseContextWithoutShortAPIDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" {
			time.Sleep(40 * time.Millisecond)
			io.Copy(io.Discard, r.Body)
			w.Write([]byte(`{"ok":true}`))
			return
		}
		w.Write([]byte("first"))
		w.(http.Flusher).Flush()
		time.Sleep(40 * time.Millisecond)
		w.Write([]byte("second"))
	}))
	defer server.Close()
	c, e := NewClient(server.URL, strings.Repeat("k", 32))
	if e != nil {
		t.Fatal(e)
	}
	c.HTTP.Timeout = 10 * time.Millisecond
	if e = c.Upload(context.Background(), "owner", "id", Asset{Name: "a", Size: 3, SHA256: strings.Repeat("a", 64)}, strings.NewReader("abc")); e != nil {
		t.Fatal("large uploads inherit API deadline", e)
	}
	f, e := c.Download(context.Background(), "owner", strings.Repeat("a", 64))
	if e != nil {
		t.Fatal(e)
	}
	body, e := io.ReadAll(f)
	f.Close()
	if e != nil || string(body) != "firstsecond" {
		t.Fatal("stream truncated", e, string(body))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e = c.Upload(ctx, "owner", "id", Asset{Name: "a", Size: 3}, strings.NewReader("abc")); e == nil {
		t.Fatal("canceled upload continued")
	}
}
