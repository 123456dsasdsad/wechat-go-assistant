package userinput

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Connection struct {
	URL, Key, JobID, Lease, Executable string
}
type envelope struct {
	ID         string  `json:"id"`
	Lease      string  `json:"lease"`
	QuestionID string  `json:"question_id,omitempty"`
	Request    Request `json:"request,omitempty"`
}
type remoteQuestion struct {
	ID      string            `json:"id"`
	State   string            `json:"state"`
	Answers map[string]Answer `json:"answers"`
}

func (c Connection) Valid() bool {
	u, e := url.Parse(c.URL)
	return e == nil && u.Scheme == "http" && net.ParseIP(u.Hostname()) != nil && net.ParseIP(u.Hostname()).IsLoopback() && u.User == nil && u.RawQuery == "" && u.Fragment == "" && c.JobID != "" && len(c.Key) >= 32 && c.Lease != ""
}
func (c Connection) call(ctx context.Context, path string, v envelope) (remoteQuestion, int, error) {
	b, _ := json.Marshal(v)
	var q remoteQuestion
	r, e := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(c.URL, "/")+path, bytes.NewReader(b))
	if e != nil {
		return q, 0, e
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+c.Key)
	res, e := (&http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(r)
	if e != nil {
		return q, 0, errors.New("user_question_network_error")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		io.Copy(io.Discard, io.LimitReader(res.Body, 1024))
		return q, res.StatusCode, errors.New("user_question_rejected")
	}
	e = json.NewDecoder(io.LimitReader(res.Body, 256<<10)).Decode(&q)
	return q, res.StatusCode, e
}
func delay(ctx context.Context) error {
	t := time.NewTimer(time.Second)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Wait retries transient failures and never generates an automatic/default answer.
func (c Connection) Wait(ctx context.Context, r Request) (Response, error) {
	if !c.Valid() {
		return Response{}, errors.New("invalid_question_connection")
	}
	if e := Validate(r); e != nil {
		return Response{}, e
	}
	v := envelope{ID: c.JobID, Lease: c.Lease, Request: r}
	path := "/jobs/questions/publish"
	for {
		q, status, e := c.call(ctx, path, v)
		if e != nil {
			if status >= 400 && status < 500 {
				return Response{}, errors.New("user_question_lease_or_payload_rejected")
			}
			if e = delay(ctx); e != nil {
				return Response{}, e
			}
			continue
		}
		if q.ID == "" {
			return Response{}, errors.New("invalid_question_receipt")
		}
		v.QuestionID = q.ID
		v.Request = Request{}
		path = "/jobs/questions/poll"
		if q.State == "answered" || q.State == "resolved" {
			if len(q.Answers) != len(r.Questions) {
				return Response{}, errors.New("incomplete_user_answers")
			}
			for _, item := range r.Questions {
				a := q.Answers[item.ID]
				if len(a.Answers) != 1 || strings.TrimSpace(a.Answers[0]) == "" {
					return Response{}, errors.New("incomplete_user_answers")
				}
			}
			for {
				_, status, e = c.call(ctx, "/jobs/questions/resolve", v)
				if e == nil {
					return Response{Answers: q.Answers}, nil
				}
				if status >= 400 && status < 500 {
					return Response{}, errors.New("user_question_resolution_rejected")
				}
				if e = delay(ctx); e != nil {
					return Response{}, e
				}
			}
		}
		if q.State != "pending" {
			return Response{}, errors.New("user_question_canceled")
		}
		if e = delay(ctx); e != nil {
			return Response{}, e
		}
	}
}
