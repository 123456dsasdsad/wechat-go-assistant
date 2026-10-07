package weixin

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var ErrAlreadyBound = errors.New("机器人已绑定；请使用原凭据，不能从此响应恢复丢失的 token")

type LoginCallbacks struct {
	QRCode     func(QRCode) error
	Status     func(string)
	VerifyCode func(context.Context) (string, error)
}

func (c *Client) GetQRCode(ctx context.Context, localTokens []string) (QRCode, error) {
	var out QRCode
	if localTokens == nil {
		localTokens = []string{}
	}
	if len(localTokens) > 10 {
		localTokens = localTokens[:10]
	}
	body := struct {
		Tokens []string `json:"local_token_list"`
	}{localTokens}
	err := c.jsonRequest(ctx, c.opts.LoginURL, "ilink/bot/get_bot_qrcode?bot_type=3", http.MethodPost, body, false, c.opts.APITimeout, &out)
	if err != nil {
		return out, err
	}
	if err := out.apiStatus.check("getQRCode"); err != nil {
		return out, err
	}
	if out.Code == "" || out.Content == "" {
		return out, errors.New("QR response is missing code or display content")
	}
	return out, nil
}

func (c *Client) PollQRCode(ctx context.Context, base, code, verification string) (QRStatus, error) {
	var out QRStatus
	query := url.Values{"qrcode": {code}}
	if verification != "" {
		query.Set("verify_code", verification)
	}
	err := c.jsonRequest(ctx, base, "ilink/bot/get_qrcode_status?"+query.Encode(), http.MethodGet, nil, false, c.opts.PollTimeout, &out)
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return QRStatus{Status: "wait"}, nil
	}
	if err != nil {
		return out, err
	}
	return out, out.apiStatus.check("pollQRCode")
}

func wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Login only performs account authorization; it does not send any message.
func (c *Client) Login(ctx context.Context, callbacks LoginCallbacks, localTokens []string) (Account, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var qr QRCode
	refresh := func() error {
		var err error
		qr, err = c.GetQRCode(ctx, localTokens)
		if err != nil {
			return err
		}
		if callbacks.QRCode != nil {
			return callbacks.QRCode(qr)
		}
		return nil
	}
	if err := refresh(); err != nil {
		return Account{}, err
	}
	base, verification := c.opts.LoginURL, ""
	refreshes, failures := 0, 0
	for {
		status, err := c.PollQRCode(ctx, base, qr.Code, verification)
		if err != nil {
			if ctx.Err() != nil {
				return Account{}, ctx.Err()
			}
			failures++
			if failures >= 3 {
				return Account{}, err
			}
			if err := wait(ctx, time.Second); err != nil {
				return Account{}, err
			}
			continue
		}
		failures = 0
		if callbacks.Status != nil {
			callbacks.Status(status.Status)
		}
		switch status.Status {
		case "confirmed":
			if status.BotToken == "" || status.BotID == "" || status.OwnerID == "" {
				return Account{}, errors.New("login confirmation is missing token, bot ID or owner ID")
			}
			if status.BaseURL == "" {
				status.BaseURL = base
			}
			if err := c.validateURL(status.BaseURL, true); err != nil {
				return Account{}, err
			}
			return Account{BotToken: status.BotToken, BotID: status.BotID, OwnerID: status.OwnerID, BaseURL: status.BaseURL}, nil
		case "need_verifycode":
			if callbacks.VerifyCode == nil {
				return Account{}, errors.New("Weixin requires verification code input")
			}
			verification, err = callbacks.VerifyCode(ctx)
			if err != nil {
				return Account{}, err
			}
			verification = strings.TrimSpace(verification)
			if verification == "" {
				return Account{}, errors.New("verification code cannot be empty")
			}
			continue
		case "scaned":
			verification = ""
		case "scaned_but_redirect":
			if status.RedirectHost == "" || strings.ContainsAny(status.RedirectHost, "/@?#") {
				return Account{}, errors.New("invalid QR redirect host")
			}
			base = "https://" + status.RedirectHost
			if err := c.validateURL(base, true); err != nil {
				return Account{}, err
			}
		case "expired":
			refreshes++
			if refreshes > 3 {
				return Account{}, errors.New("QR refresh limit exceeded")
			}
			base, verification = c.opts.LoginURL, ""
			if err := refresh(); err != nil {
				return Account{}, err
			}
			continue
		case "verify_code_blocked":
			return Account{}, errors.New("verification attempts blocked; login stopped")
		case "binded_redirect":
			return Account{}, ErrAlreadyBound
		case "wait":
		default:
			return Account{}, errors.New("unrecognized QR login status")
		}
		if err := wait(ctx, 500*time.Millisecond); err != nil {
			return Account{}, err
		}
	}
}
