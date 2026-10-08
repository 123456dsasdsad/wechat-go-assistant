package maintenance

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}
type AccountCheck struct {
	Alias     string          `json:"alias"`
	State     string          `json:"state"`
	Reason    string          `json:"reason,omitempty"`
	Refreshed bool            `json:"refreshed,omitempty"`
	Windows   []AccountWindow `json:"quota_windows,omitempty"`
}
type AccountWindow struct {
	Seconds int64   `json:"seconds"`
	Used    float64 `json:"used_percent"`
	Reset   int64   `json:"resets_at,omitempty"`
}
type accountQuotaWindow struct {
	Used    *float64 `json:"used_percent"`
	Seconds int64    `json:"limit_window_seconds"`
	Reset   int64    `json:"reset_at"`
}

// A successful HTTP response verifies authorization, not necessarily quota.
// Keep missing quota distinguishable from confirmed availability.
func applyAccountQuota(check *AccountCheck, body []byte) {
	var usage struct {
		Rate *struct {
			Allowed   *bool               `json:"allowed"`
			Reached   *bool               `json:"limit_reached"`
			Primary   *accountQuotaWindow `json:"primary_window"`
			Secondary *accountQuotaWindow `json:"secondary_window"`
		} `json:"rate_limit"`
	}
	check.State = "授权有效，额度待核实（保留）"
	if json.Unmarshal(body, &usage) != nil || usage.Rate == nil {
		return
	}
	r := usage.Rate
	exhausted := (r.Reached != nil && *r.Reached) || (r.Allowed != nil && !*r.Allowed)
	for _, w := range []*accountQuotaWindow{r.Primary, r.Secondary} {
		if w == nil || w.Used == nil || *w.Used < 0 || *w.Used > 100 {
			continue
		}
		check.Windows = append(check.Windows, AccountWindow{w.Seconds, *w.Used, w.Reset})
		if *w.Used >= 100 {
			exhausted = true
		}
	}
	if exhausted {
		check.State = "额度用尽（保留）"
	} else if r.Allowed != nil && *r.Allowed {
		check.State = "有效"
	}
}

type AccountSummary struct {
	Accounts    []AccountCheck `json:"accounts"`
	Reload      bool           `json:"reload_required"`
	Quarantined int            `json:"quarantined"`
}

func errorCode(b []byte) string {
	var v struct {
		Error json.RawMessage `json:"error"`
		Code  string          `json:"code"`
	}
	if json.Unmarshal(b, &v) != nil {
		return ""
	}
	if v.Code != "" {
		return v.Code
	}
	var s string
	if json.Unmarshal(v.Error, &s) == nil {
		return s
	}
	var o struct{ Code, Type string }
	json.Unmarshal(v.Error, &o)
	if o.Code != "" {
		return o.Code
	}
	return o.Type
}
func Permanent(code string) bool {
	switch code {
	case "invalid_grant", "invalid_refresh_token", "refresh_token_revoked", "refresh_token_expired", "refresh_token_reused", "account_deactivated", "account_deleted", "user_deactivated", "account_suspended":
		return true
	}
	return false
}
func Classification(status int, code string) string {
	if status == 200 {
		return "有效"
	}
	if Permanent(code) && (status == 400 || status == 401 || status == 403) {
		return "失效"
	}
	if status == 429 {
		return "额度或频率限制（保留）"
	}
	if status == 401 {
		return "授权待刷新（保留）"
	}
	return "暂时无法核实（保留）"
}
func MaskEmail(s string) string {
	p := strings.SplitN(s, "@", 2)
	if len(p) != 2 {
		return "账号"
	}
	r := []rune(p[0])
	if len(r) > 2 {
		r = r[:2]
	}
	return string(r) + "***@" + p[1]
}
func tokenExpiry(token string) time.Time {
	p := strings.Split(token, ".")
	if len(p) < 2 {
		return time.Time{}
	}
	b, e := base64.RawURLEncoding.DecodeString(p[1])
	if e != nil {
		return time.Time{}
	}
	var claims struct{ Exp int64 }
	if json.Unmarshal(b, &claims) != nil {
		return time.Time{}
	}
	return time.Unix(claims.Exp, 0)
}
func readMap(path string) (map[string]any, error) {
	b, e := os.ReadFile(path)
	var m map[string]any
	if e == nil {
		e = json.Unmarshal(b, &m)
	}
	if m == nil && e == nil {
		e = errors.New("invalid_json")
	}
	return m, e
}
func str(m map[string]any, k string) string { s, _ := m[k].(string); return s }
func request(ctx context.Context, c HTTPDoer, method, url string, body []byte, headers map[string]string) (int, []byte, error) {
	req, e := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if e != nil {
		return 0, nil, errors.New("invalid_request")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, e := c.Do(req)
	if e != nil {
		return 0, nil, errors.New("network_or_timeout")
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	return resp.StatusCode, b, e
}

// CheckAccounts owns refresh-token rotation in this headless deployment. It
// writes the managed native store first, then its access-token-only sidecar copy.
// Quarantine requires an explicit permanent upstream error, never quota/timeout.
func CheckAccounts(ctx context.Context, c HTTPDoer, root string, dry bool) (AccountSummary, error) {
	var sum AccountSummary
	details := filepath.Join(root, "cockpit-data", "codex_accounts")
	entries, e := os.ReadDir(details)
	if e != nil {
		return sum, errors.New("account_store_unavailable")
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		p := filepath.Join(details, entry.Name())
		a, e := readMap(p)
		if e != nil {
			return sum, errors.New("account_record_unreadable")
		}
		id := str(a, "id")
		if filepath.Base(id+".json") != entry.Name() {
			return sum, errors.New("account_identity_mismatch")
		}
		short := id
		if len(short) > 8 {
			short = short[:8]
		}
		check := AccountCheck{Alias: MaskEmail(str(a, "email")) + " [" + short + "]"}
		tokens, ok := a["tokens"].(map[string]any)
		if !ok {
			return sum, errors.New("account_tokens_unreadable")
		}
		authPath := filepath.Join(root, "auths", entry.Name())
		auth, e := readMap(authPath)
		if e != nil {
			return sum, errors.New("sidecar_account_unreadable")
		}
		if disabled, _ := auth["disabled"].(bool); disabled {
			check.State = "已隔离/停用"
			sum.Accounts = append(sum.Accounts, check)
			continue
		}
		token := str(tokens, "access_token")
		expiry := tokenExpiry(token)
		needsRefresh := expiry.IsZero() || expiry.Before(time.Now().Add(26*time.Hour))
		status := 0
		code := ""
		var usageResponse []byte
		if !needsRefresh {
			var response []byte
			status, response, e = request(ctx, c, "GET", "https://chatgpt.com/backend-api/wham/usage", nil, map[string]string{"Authorization": "Bearer " + token, "Chatgpt-Account-Id": str(a, "account_id"), "User-Agent": "codex_cli_rs/0.160.1"})
			code = errorCode(response)
			usageResponse = response
			needsRefresh = status == 401
		}
		if needsRefresh && !dry {
			rt := str(tokens, "refresh_token")
			if rt == "" {
				check.State = "需重新授权（保留）"
				check.Reason = "缺少刷新凭据"
				sum.Accounts = append(sum.Accounts, check)
				continue
			}
			body, _ := json.Marshal(map[string]string{"client_id": "app_EMoamEEZ73f0CkXaXp7hrann", "grant_type": "refresh_token", "refresh_token": rt})
			var response []byte
			status, response, e = request(ctx, c, "POST", "https://auth.openai.com/oauth/token", body, map[string]string{"Content-Type": "application/json"})
			code = errorCode(response)
			if e == nil && status == 200 {
				var next map[string]any
				if json.Unmarshal(response, &next) != nil || str(next, "access_token") == "" {
					return sum, errors.New("refresh_response_invalid")
				}
				// Retain the rotated token before doing any further network request.
				for _, k := range []string{"access_token", "id_token", "refresh_token"} {
					if str(next, k) != "" {
						tokens[k] = next[k]
					}
				}
				a["token_updated_at"] = time.Now().Unix()
				generation, _ := a["token_generation"].(float64)
				a["token_generation"] = generation + 1
				a["requires_reauth"] = false
				if e = AtomicJSON(p, a); e != nil {
					return sum, errors.New("refreshed_authority_save_failed")
				}
				auth["access_token"] = tokens["access_token"]
				auth["id_token"] = tokens["id_token"]
				auth["refresh_token"] = ""
				auth["last_refresh"] = fmt.Sprint(time.Now().Unix())
				if e = AtomicJSON(authPath, auth); e != nil {
					return sum, errors.New("refreshed_sidecar_save_failed")
				}
				check.Refreshed = true
				sum.Reload = true
				status, response, e = request(ctx, c, "GET", "https://chatgpt.com/backend-api/wham/usage", nil, map[string]string{"Authorization": "Bearer " + str(tokens, "access_token"), "Chatgpt-Account-Id": str(a, "account_id"), "User-Agent": "codex_cli_rs/0.160.1"})
				code = errorCode(response)
				usageResponse = response
			}
		} else if needsRefresh {
			check.State = "待刷新（只检查模式）"
			sum.Accounts = append(sum.Accounts, check)
			continue
		}
		if e != nil {
			check.State = "网络异常（保留）"
		} else {
			check.State = Classification(status, code)
			if status == 200 {
				applyAccountQuota(&check, usageResponse)
			}
		}
		if check.State == "失效" && !dry {
			q := filepath.Join(root, "quarantine", time.Now().In(Beijing).Format("2006-01-02"), id)
			if e = AtomicJSON(filepath.Join(q, "native.json"), a); e != nil {
				return sum, errors.New("quarantine_backup_failed")
			}
			if e = AtomicJSON(filepath.Join(q, "auth.json"), auth); e != nil {
				return sum, errors.New("quarantine_backup_failed")
			}
			// Exclude from runnable pool while retaining identity and recoverable originals.
			auth["disabled"] = true
			a["requires_reauth"] = true
			a["maintenance_quarantine_reason"] = code
			if e = AtomicJSON(authPath, auth); e != nil {
				return sum, e
			}
			if e = AtomicJSON(p, a); e != nil {
				return sum, e
			}
			sum.Reload = true
			sum.Quarantined++
			check.State = "失效，已移出可用池并隔离"
			check.Reason = code
		}
		sum.Accounts = append(sum.Accounts, check)
	}
	return sum, nil
}
func (s AccountSummary) Text(day string) string {
	var b strings.Builder
	available, limited, disabled, unknown := 0, 0, 0, 0
	for _, a := range s.Accounts {
		switch {
		case a.State == "有效":
			available++
		case strings.Contains(a.State, "额度") && !strings.Contains(a.State, "待核实"):
			limited++
		case strings.Contains(a.State, "隔离") || a.State == "失效":
			disabled++
		default:
			unknown++
		}
	}
	fmt.Fprintf(&b, "【账号状态｜%s 07:00｜阿里云共享池】\n总数 %d；可用 %d；额度/频率受限 %d；失效或停用 %d；待核实 %d。\n本次清理：隔离 %d 个失效账号。", day, len(s.Accounts), available, limited, disabled, unknown, s.Quarantined)
	for i, a := range s.Accounts {
		fmt.Fprintf(&b, "\n\n【账号 %d｜%s】\n状态：%s", i+1, a.Alias, a.State)
		if a.Reason != "" {
			b.WriteString("（" + a.Reason + "）")
		}
		if a.Refreshed {
			b.WriteString("，凭据已续期")
		}
		for _, w := range a.Windows {
			label := "额度窗口"
			if w.Seconds > 0 {
				label = fmt.Sprintf("%g 小时额度", float64(w.Seconds)/3600)
				if w.Seconds%86400 == 0 {
					label = fmt.Sprintf("%d 天额度", w.Seconds/86400)
				}
			}
			fmt.Fprintf(&b, "\n%s：剩余 %.1f%%（已用 %.1f%%）", label, 100-w.Used, w.Used)
			if w.Reset > 0 {
				fmt.Fprintf(&b, "\n重置时间：%s", time.Unix(w.Reset, 0).In(Beijing).Format("01-02 15:04"))
			}
		}
	}
	b.WriteString("\n\n清理规则：仅明确撤销/永久失效才隔离；额度用完、网络异常保留。隔离记录可恢复，不删除原始备份。")
	return b.String()
}

// AccountParagraphs also presents persisted reports from older builds clearly,
// without changing the source check time, account data, or delivery receipts.
func AccountParagraphs(text string) string {
	var out []string
	index := 0
	for _, line := range strings.Split(text, "\n") {
		alias, state, ok := strings.Cut(line, "：")
		if ok && strings.Contains(alias, "@") && !strings.HasPrefix(alias, "【") {
			index++
			if len(out) > 0 && out[len(out)-1] != "" {
				out = append(out, "")
			}
			out = append(out, fmt.Sprintf("【账号 %d｜%s】", index, alias), "状态："+state)
		} else {
			out = append(out, strings.Replace(line, "；重置 ", "\n重置时间：", 1))
		}
	}
	return strings.Join(out, "\n")
}
