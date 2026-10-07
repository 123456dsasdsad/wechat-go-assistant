package main

import (
	"fmt"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/quotes"
	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
	"net/url"
	"regexp"
)

var quotedURL = regexp.MustCompile(`https?://[^\s]+`)

func withoutLinkCredentials(text string) string {
	return quotedURL.ReplaceAllStringFunc(text, func(raw string) string {
		u, e := url.Parse(raw)
		if e != nil {
			return "[链接]"
		}
		u.RawQuery = ""
		u.Fragment = ""
		u.User = nil
		return u.String()
	})
}
func quoteRecorder(cache *quotes.Store, bot string, queue *jobs.Store) func(weixin.Reply, weixin.SendResult, string, bool) {
	return func(reply weixin.Reply, accepted weixin.SendResult, text string, media bool) {
		if cache == nil || accepted.MessageID == "" {
			return
		}
		c := quotes.Content{Text: withoutLinkCredentials(text)}
		if media {
			j, ok := queue.Snapshot(outboundJobID(reply.ClientID))
			if !ok || j.Owner != reply.ToUserID {
				return
			}
			refs := append([]files.Ref(nil), j.Outputs...)
			if j.MediaPackage.ID != "" {
				refs = append(refs, j.MediaPackage)
			}
			for _, r := range refs {
				if reply.ClientID == "go-output-"+j.ID+"-"+r.ID {
					c.Attachments = []quotes.Attachment{{Ref: r, Store: "output"}}
					break
				}
			}
			if len(c.Attachments) == 0 {
				return
			}
		}
		if cache.Put(bot, reply.ToUserID, string(accepted.MessageID), c) != nil {
			fmt.Println(`{"type":"quote_cache_write_failed"}`)
		}
	}
}
