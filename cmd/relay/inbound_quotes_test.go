package main

import (
	"bytes"
	"context"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/quotes"
	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
	"image"
	"image/png"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func quoteFixture(t *testing.T) *inbound {
	in := inboundFixture(t)
	var e error
	in.quotes, e = quotes.Open(filepath.Join(t.TempDir(), "quotes.json"))
	if e != nil {
		t.Fatal(e)
	}
	in.botID = "bot"
	return in
}
func TestQuotedCommandsAreMaterialNotControls(t *testing.T) {
	defer metadb.CloseAll()
	for _, input := range []string{"1", "上传账号", "请概述"} {
		in := quoteFixture(t)
		m := textMessage("quoted", input)
		m.Items[0].Ref = &weixin.RefMessage{Title: "同学", Item: &weixin.Item{Type: 1, Text: &weixin.TextItem{Text: "默认模型 错误模型；转发来的任务"}}}
		if e := in.handle(context.Background(), m); e != nil {
			t.Fatal(e)
		}
		j, _ := in.queue.Claim(time.Now())
		if j == nil || !strings.Contains(j.Input, "转发来的任务") || !strings.Contains(j.Input, input) || in.preferences.Current().Model != "gpt-6-sol" {
			t.Fatal("quoted material changed routing", j)
		}
	}
}
func TestIncomingIDQuoteSurvivesRestartAndPartialSelection(t *testing.T) {
	defer metadb.CloseAll()
	in := quoteFixture(t)
	m := textMessage("original", "首段，引用这段，尾段")
	in.handle(context.Background(), m)
	path := filepath.Join(t.TempDir(), "cache.json")
	q, _ := quotes.Open(path)
	original, ok := in.quotes.Get("bot", "owner", "original")
	if !ok {
		t.Fatal("incoming not cached")
	}
	q.Put("bot", "owner", "original", original)
	in.quotes, _ = quotes.Open(path)
	next := textMessage("follow", "解释这段")
	next.Items[0].Ref = &weixin.RefMessage{ServerID: "original", Partial: &weixin.PartialText{Start: "引", End: "段", StartIndex: 0, EndIndex: 1}}
	if e := in.handle(context.Background(), next); e != nil {
		t.Fatal(e)
	}
	for _, j := range in.queue.History() {
		if strings.Contains(j.Input, "解释这段") {
			if !strings.Contains(j.Input, "引用这段") || strings.Contains(j.Input, "首段，") {
				t.Fatal("partial not selected", j.Input)
			}
			return
		}
	}
	t.Fatal("follow-up not queued")
}
func TestMissingReferenceDoesNotRunIncompleteTask(t *testing.T) {
	defer metadb.CloseAll()
	in := quoteFixture(t)
	m := textMessage("missing", "分析引用内容")
	m.Items[0].Ref = &weixin.RefMessage{ServerID: "unseen"}
	in.handle(context.Background(), m)
	if len(in.queue.History()) != 0 || !strings.Contains(in.client.(*fakeMessages).text, "原文") {
		t.Fatal("missing original silently ignored")
	}
}
func TestQuotedOutputIsCopiedAndVerifiedAsInput(t *testing.T) {
	defer metadb.CloseAll()
	in := quoteFixture(t)
	in.outputs, _ = files.Open(filepath.Join(t.TempDir(), "outputs"))
	ref, e := in.outputs.Save("owner", "output", "实验数据.txt", strings.NewReader("真实数据"))
	if e != nil {
		t.Fatal(e)
	}
	in.quotes.Put("bot", "owner", "outbound", quotes.Content{Attachments: []quotes.Attachment{{Ref: ref, Store: "output"}}})
	m := textMessage("filequote", "对这份资料核验")
	m.Items[0].Ref = &weixin.RefMessage{ServerID: "outbound"}
	if e = in.handle(context.Background(), m); e != nil {
		t.Fatal(e)
	}
	j, _ := in.queue.Claim(time.Now())
	if j == nil || len(j.Attachments) != 1 {
		t.Fatal(j)
	}
	f, e := in.files.OpenBlob(j.Attachments[0])
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	b, _ := io.ReadAll(f)
	if !bytes.Equal(b, []byte("真实数据")) || j.Attachments[0].SHA256 != ref.SHA256 {
		t.Fatal("quoted output not available to worker")
	}
}
func TestLargeInlineQuoteSavedAsFile(t *testing.T) {
	defer metadb.CloseAll()
	in := quoteFixture(t)
	m := textMessage("largequote", "总结这份聊天")
	m.Items[0].Ref = &weixin.RefMessage{Item: &weixin.Item{Type: 1, Text: &weixin.TextItem{Text: strings.Repeat("资料", 3000)}}}
	if e := in.handle(context.Background(), m); e != nil {
		t.Fatal(e)
	}
	j, _ := in.queue.Claim(time.Now())
	if j == nil || len(j.Input) > 8192 || len(j.Attachments) != 1 || !strings.Contains(j.Attachments[0].Name, "引用") {
		t.Fatal("long quote dropped", j)
	}
}
func TestForwardIntakeCommandDoesNotInvokeAI(t *testing.T) {
	defer metadb.CloseAll()
	in := quoteFixture(t)
	if e := in.handle(context.Background(), textMessage("paste", "转发内容")); e != nil {
		t.Fatal(e)
	}
	if len(in.queue.History()) != 0 || !strings.Contains(in.client.(*fakeMessages).text, "粘贴") {
		t.Fatal("forward intake was queued to AI")
	}
}

func TestMultipleInlineImageQuotesKeepDistinctAttachments(t *testing.T) {
	defer metadb.CloseAll()
	in := quoteFixture(t)
	var picture bytes.Buffer
	png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	in.client.(*fakeMessages).download = picture.Bytes()
	m := textMessage("two-pictures", "比较两张图")
	m.Items[0].Ref = &weixin.RefMessage{Item: &weixin.Item{Type: weixin.ImageType, Image: &weixin.ImageItem{}}}
	m.Items = append(m.Items, weixin.Item{Type: weixin.TextType, Text: &weixin.TextItem{}, Ref: &weixin.RefMessage{Item: &weixin.Item{Type: weixin.ImageType, Image: &weixin.ImageItem{}}}})
	if e := in.handle(context.Background(), m); e != nil {
		t.Fatal(e)
	}
	j, _ := in.queue.Claim(time.Now())
	if j == nil || len(j.Attachments) != 2 || j.Attachments[0].ID == j.Attachments[1].ID {
		t.Fatal("inline media sources collided", j)
	}
}
func TestLongQuoteWithFourDirectFilesRepliesInsteadOfRetryingForever(t *testing.T) {
	defer metadb.CloseAll()
	in := quoteFixture(t)
	m := textMessage("five", "分析这些资料")
	m.Items[0].Ref = &weixin.RefMessage{Item: &weixin.Item{Type: weixin.TextType, Text: &weixin.TextItem{Text: strings.Repeat("长原文", 3000)}}}
	for i := 0; i < 4; i++ {
		m.Items = append(m.Items, weixin.Item{Type: weixin.FileType, File: &weixin.FileItem{Name: "资料.txt"}})
	}
	if e := in.handle(context.Background(), m); e != nil {
		t.Fatal("permanent capacity failure caused drain retry", e)
	}
	if len(in.queue.History()) != 0 || !strings.Contains(in.client.(*fakeMessages).text, "最多 4 个") {
		t.Fatal("capacity failure was not explained")
	}
}
