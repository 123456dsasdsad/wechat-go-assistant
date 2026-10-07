package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/codex"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/steering"
	"io"
	"net/http"
	"strings"
	"time"
)

type relaySteering struct {
	url, key string
	task     jobs.Task
}

func (s *relaySteering) call(ctx context.Context, path string, body any, out any) (int, error) {
	b, _ := json.Marshal(body)
	req, e := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(s.url, "/")+path, bytes.NewReader(b))
	if e != nil {
		return 0, e
	}
	req.Header.Set("Authorization", "Bearer "+s.key)
	req.Header.Set("Content-Type", "application/json")
	res, e := (&http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if e != nil {
		return 0, errors.New("steering_network_error")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		io.Copy(io.Discard, io.LimitReader(res.Body, 1024))
		return res.StatusCode, errors.New("steering_rejected")
	}
	if out != nil {
		e = json.NewDecoder(io.LimitReader(res.Body, 300<<10)).Decode(out)
	}
	return res.StatusCode, e
}
func (s *relaySteering) Poll(ctx context.Context) ([]codex.SteerInput, error) {
	var pending []jobs.Supplement
	_, e := s.call(ctx, "/jobs/steering/poll", jobs.SteerRequest{ID: s.task.ID, Lease: s.task.Lease}, &pending)
	if e != nil {
		return nil, e
	}
	out := []codex.SteerInput{}
	for _, v := range pending {
		if len(v.ID) != 24 || len(v.Input) > 8192 {
			return nil, errors.New("invalid_supplement")
		}
		out = append(out, codex.SteerInput{ID: v.ID, Input: v.Input})
	}
	return out, nil
}
func (s *relaySteering) Reserve(ctx context.Context, id, turn string) (bool, error) {
	status, e := s.call(ctx, "/jobs/steering/ack", jobs.SteerRequest{ID: s.task.ID, Lease: s.task.Lease, SupplementID: id, State: "dispatching", TurnID: turn}, nil)
	return status == 200 && e == nil, e
}
func (s *relaySteering) Ack(ctx context.Context, r steering.Receipt) error {
	var e error
	for i := 0; i < 3; i++ {
		status, err := s.call(ctx, "/jobs/steering/ack", jobs.SteerRequest{ID: s.task.ID, Lease: s.task.Lease, SupplementID: r.ID, State: r.State, TurnID: r.TurnID}, nil)
		e = err
		if e == nil || status == 409 {
			return e
		}
		if !pause(ctx, 250*time.Millisecond) {
			return ctx.Err()
		}
	}
	return e
}
