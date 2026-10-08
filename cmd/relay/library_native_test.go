package main

import (
	"bytes"
	"context"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/library"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
	"image"
	"image/png"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLibraryBatchAndNativeQueriesDoNotLaunchAI(t *testing.T) {
	defer metadb.CloseAll()
	in := inboundFixture(t)
	campus, e := library.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer campus.Close()
	server := httptest.NewServer(library.Handler(campus, strings.Repeat("k", 32)))
	defer server.Close()
	in.library, _ = library.NewClient(server.URL, strings.Repeat("k", 32))
	in.libraryDrafts, _ = library.Open(t.TempDir())
	defer in.libraryDrafts.Close()
	ctx := context.Background()
	for i, text := range []string{"资料库", "开始收录 A,B", "收录备注 研究截图里的方法"} {
		if e = in.handle(ctx, textMessage(string(rune('a'+i)), text)); e != nil {
			t.Fatal(e)
		}
	}
	var picture bytes.Buffer
	png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	in.client.(*fakeMessages).download = picture.Bytes()
	imageMsg := textMessage("picture", "")
	imageMsg.Items = append(imageMsg.Items, weixin.Item{Type: weixin.ImageType, Image: &weixin.ImageItem{}})
	if e = in.handle(ctx, imageMsg); e != nil {
		t.Fatal(e)
	}
	if len(in.queue.History()) != 0 {
		t.Fatal("native library or draft upload invoked AI")
	}
	if e = in.handle(ctx, textMessage("done", "完成收录")); e != nil {
		t.Fatal(e)
	}
	task, e := in.queue.Claim(time.Now())
	if e != nil || task == nil || task.Kind != "library_intake" {
		t.Fatal(task, e)
	}
	draft, e := campus.Intake("owner", task.LibraryID)
	if e != nil || len(draft.Assets) != 1 || !strings.Contains(draft.Text, "研究截图") {
		t.Fatal(draft, e)
	}
	f, e := campus.Blob("owner", draft.Assets[0].SHA256)
	if e != nil {
		t.Fatal(e)
	}
	f.Close()
	if len(in.queue.History()) != 1 {
		t.Fatal("batch was duplicated")
	}
}
