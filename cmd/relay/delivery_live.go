package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
	"strings"
	"sync"
	"time"
)

type cachedUpload struct {
	value weixin.Uploaded
	at    time.Time
}
type liveResultSender struct {
	client       resultSender
	contextFor   func(string) (string, error)
	mu           sync.Mutex
	cache        map[string]cachedUpload
	sendMu       sync.Mutex
	lastJob      string
	mediaAllowed func(weixin.Reply) bool
	pauseMedia   func(string) error
	remember     func(weixin.Reply, weixin.SendResult, string, bool)
}

func latestReplyContext(path string) func(string) (string, error) {
	return func(owner string) (string, error) {
		state, e := weixin.LoadState(path)
		if e != nil || state.Account.OwnerID != owner || state.Contexts[owner] == "" {
			return "", errors.New("reply_context_unavailable")
		}
		return state.Contexts[owner], nil
	}
}
func (s *liveResultSender) reply(r weixin.Reply) (weixin.Reply, error) {
	if strings.HasPrefix(r.ClientID, "go-output-") {
		if r.ContextToken == "" || (s.mediaAllowed != nil && !s.mediaAllowed(r)) {
			return r, errors.New("media_batch_paused")
		}
		return r, nil
	}
	value, e := s.contextFor(r.ToUserID)
	r.ContextToken = value
	return r, e
}
func (s *liveResultSender) PauseMedia(owner string) error {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	if s.pauseMedia != nil {
		return s.pauseMedia(owner)
	}
	return nil
}
func (s *liveResultSender) SendText(ctx context.Context, r weixin.Reply, text string) (weixin.SendResult, error) {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	r, e := s.reply(r)
	if e != nil {
		return weixin.SendResult{}, e
	}
	value, e := s.client.SendText(ctx, r, text)
	if e == nil {
		s.lastJob = outboundJobID(r.ClientID)
		if s.remember != nil {
			s.remember(r, value, text, false)
		}
	}
	return value, e
}
func (s *liveResultSender) SendImage(ctx context.Context, r weixin.Reply, file weixin.Uploaded) (weixin.SendResult, error) {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	r, e := s.reply(r)
	if e != nil {
		return weixin.SendResult{}, e
	}
	value, e := s.client.SendImage(ctx, r, file)
	if e == nil {
		s.lastJob = outboundJobID(r.ClientID)
		if s.remember != nil {
			s.remember(r, value, "", true)
		}
	}
	return value, e
}
func (s *liveResultSender) SendFile(ctx context.Context, r weixin.Reply, name string, file weixin.Uploaded) (weixin.SendResult, error) {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	r, e := s.reply(r)
	if e != nil {
		return weixin.SendResult{}, e
	}
	value, e := s.client.SendFile(ctx, r, name, file)
	if e == nil {
		s.lastJob = outboundJobID(r.ClientID)
		if s.remember != nil {
			s.remember(r, value, name, true)
		}
	}
	return value, e
}
func (s *liveResultSender) Download(ctx context.Context, item weixin.Item) ([]byte, error) {
	return s.client.(messageClient).Download(ctx, item)
}
func (s *liveResultSender) SendCaptionedImage(ctx context.Context, r weixin.Reply, file weixin.Uploaded, caption string) (weixin.SendResult, error) {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	jobID := outboundJobID(r.ClientID)
	bound, e := s.reply(r)
	if e != nil {
		return weixin.SendResult{}, e
	}
	if s.lastJob != jobID {
		label := bound
		label.ClientID = "go-caption-" + r.ClientID
		accepted, sendErr := s.client.SendText(ctx, label, caption)
		if sendErr != nil {
			e = sendErr
			return weixin.SendResult{}, e
		}
		if s.remember != nil {
			s.remember(label, accepted, caption, false)
		}
		s.lastJob = jobID
	}
	value, e := s.client.SendImage(ctx, bound, file)
	if e == nil {
		s.lastJob = jobID
		if s.remember != nil {
			s.remember(bound, value, "", true)
		}
	}
	return value, e
}
func (s *liveResultSender) Upload(ctx context.Context, owner string, kind int, data []byte) (weixin.Uploaded, error) {
	key := fmt.Sprintf("%x/%d/%x", sha256.Sum256([]byte(owner)), kind, sha256.Sum256(data))
	s.mu.Lock()
	entry, ok := s.cache[key]
	s.mu.Unlock()
	if ok && time.Since(entry.at) < 10*time.Minute {
		return entry.value, nil
	}
	value, e := s.client.Upload(ctx, owner, kind, data)
	if e == nil {
		s.mu.Lock()
		if s.cache == nil {
			s.cache = map[string]cachedUpload{}
		}
		for k, v := range s.cache {
			if time.Since(v.at) >= 10*time.Minute {
				delete(s.cache, k)
			}
		}
		s.cache[key] = cachedUpload{value, time.Now()}
		s.mu.Unlock()
	}
	return value, e
}

type deliveryBackoff struct {
	next     time.Time
	context  string
	failures int
}

func shouldAttempt(current string, b deliveryBackoff) bool {
	return current != b.context || !time.Now().Before(b.next)
}
func failedDelivery(current string, previous deliveryBackoff, err error) deliveryBackoff {
	n := previous.failures + 1
	seconds := 2 * n
	var api *weixin.APIError
	if errors.As(err, &api) && api.Ret == -2 {
		seconds = 15 * n
	}
	if seconds > 60 {
		seconds = 60
	}
	return deliveryBackoff{time.Now().Add(time.Duration(seconds) * time.Second), current, n}
}
