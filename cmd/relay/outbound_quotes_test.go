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
