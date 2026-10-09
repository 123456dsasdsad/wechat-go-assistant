package userinput

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (c Connection) permit(ctx context.Context, action, question string) error {
	u, e := url.Parse(c.PermitURL)
	if e != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() || len(c.PermitKey) != 64 {
		return errors.New("invalid_question_permit")
	}
	h := sha256.Sum256([]byte(question))
	b, _ := json.Marshal(map[string]string{"job": c.JobID, "lease": c.Lease, "question": hex.EncodeToString(h[:]), "action": action})
	for {
		req, e := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(c.PermitURL, "/")+"/permit", bytes.NewReader(b))
		if e != nil {
			return e
		}
		req.Header.Set("Authorization", "Bearer "+c.PermitKey)
		req.Header.Set("Content-Type", "application/json")
		res, e := (&http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
		if e == nil {
			io.Copy(io.Discard, io.LimitReader(res.Body, 2048))
			res.Body.Close()
			if res.StatusCode == 200 {
				return nil
			}
			if res.StatusCode >= 400 && res.StatusCode < 500 {
				return errors.New("question_execution_permit_rejected")
			}
		}
		if e = delay(ctx); e != nil {
			return e
		}
	}
}
