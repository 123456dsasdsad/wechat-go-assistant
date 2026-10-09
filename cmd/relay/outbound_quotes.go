package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/quotes"
	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var quotedURL = regexp.MustCompile(`https?://[^\s]+`)

func quoteTextKey(text string) string {
	text = withoutLinkCredentials(text)
	if strings.TrimSpace(text) == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(text))
	return "content:" + hex.EncodeToString(sum[:])
}

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
		if strings.HasPrefix(reply.ClientID, "go-question-") {
			parts := strings.Split(strings.TrimPrefix(reply.ClientID, "go-question-"), "-")
			if len(parts) == 2 {
				index, e := strconv.Atoi(parts[1])
				if e == nil {
					for _, j := range queue.Active() {
						if j.Owner != reply.ToUserID {
							continue
						}
						for _, b := range j.Questions {
							if b.ID == parts[0] && index > 0 && index <= len(b.Request.Questions) {
								for _, id := range []string{string(accepted.MessageID), quoteTextKey(text)} {
									if id != "" && queue.RecordQuestionMessage(j.ID, b.ID, b.Request.Questions[index-1].ID, id) != nil {
										fmt.Println(`{"type":"question_message_binding_failed"}`)
									}
								}
							}
						}
					}
				}
			}
		}
		if cache == nil {
			return
		}
		c := quotes.Content{Text: withoutLinkCredentials(text)}
		jobID := outboundJobID(reply.ClientID)
		if jobID == "" {
			if m := regexp.MustCompile(`(?m)^任务 ([a-f0-9]{8})`).FindStringSubmatch(text); len(m) == 2 {
				if j, e := queue.Find(reply.ToUserID, m[1]); e == nil {
					jobID = j.ID
				}
			}
		}
		if j, ok := queue.Snapshot(jobID); ok && j.Owner == reply.ToUserID {
			c.JobID = j.ID
			c.ConversationID = j.ConversationID
		}
		if m := regexp.MustCompile(`材料ID：([a-f0-9]{24})`).FindStringSubmatch(text); len(m) == 2 {
			c.MaterialID = m[1]
		}
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
		for _, id := range []string{string(accepted.MessageID), quoteTextKey(text)} {
			if id != "" && cache.Put(bot, reply.ToUserID, id, c) != nil {
				fmt.Println(`{"type":"quote_cache_write_failed"}`)
			}
		}
	}
}
