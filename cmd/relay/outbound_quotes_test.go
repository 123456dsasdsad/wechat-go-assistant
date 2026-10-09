package main

import (
	"context"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/quotes"
	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
	"path/filepath"
	"strings"
	"testing"
)

type acceptedQuoteSender struct{ fakeResultSender }

func (s *acceptedQuoteSender) SendText(context.Context, weixin.Reply, string) (weixin.SendResult, error) {
	return weixin.SendResult{MessageID: "server-id"}, nil
}
func TestAcceptedOutboundServerIDIsCachedWithoutLinkCredentials(t *testing.T) {
	defer metadb.CloseAll()
	in := quoteFixture(t)
	sender := &liveResultSender{client: &acceptedQuoteSender{}, contextFor: func(string) (string, error) { return "fresh", nil }, remember: quoteRecorder(in.quotes, "bot", in.queue)}
	if _, e := sender.SendText(context.Background(), weixin.Reply{ToUserID: "owner"}, "回答 https://example.com/#private-grant https://example.com/result?token=private-key"); e != nil {
		t.Fatal(e)
	}
	c, ok := in.quotes.Get("bot", "owner", "server-id")
	if !ok || !strings.Contains(c.Text, "回答") || strings.Contains(c.Text, "private-") {
		t.Fatal("outbound not cached or credential retained")
	}
	path := filepath.Join(t.TempDir(), "empty.json")
	other, _ := quotes.Open(path)
	quoteRecorder(other, "bot", in.queue)(weixin.Reply{ToUserID: "owner"}, weixin.SendResult{ClientID: "local-only"}, "text", false)
	if _, ok := other.Get("bot", "owner", "local-only"); ok {
		t.Fatal("client ID guessed to be server ID")
	}
}

func TestOutboundWithoutServerIDMatchesOnlyCompleteAcceptedText(t *testing.T) {
	defer metadb.CloseAll()
	in := quoteFixture(t)
	in.handle(context.Background(), textMessage("job", "原问题"))
	j := in.queue.History()[0]
	text := "任务 " + j.ID[:8] + "\n原问题：原问题\n结果链接 https://example.com/?token=private"
	quoteRecorder(in.quotes, in.botID, in.queue)(weixin.Reply{ToUserID: in.owner, ClientID: "go-result-" + j.ID}, weixin.SendResult{}, text, false)
	m := textMessage("quote", "补发这个")
	m.Items[0].Ref = &weixin.RefMessage{Item: &weixin.Item{Type: weixin.TextType, Text: &weixin.TextItem{Text: text}}}
	if c, ok := in.quotedContent(m); !ok || c.JobID != j.ID {
		t.Fatal("complete accepted quote not bound", c, ok)
	}
	m.Items[0].Ref.Item.Text.Text = "原问题"
	if _, ok := in.quotedContent(m); ok {
		t.Fatal("partial summary guessed a task")
	}
}
