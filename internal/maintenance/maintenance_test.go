package maintenance

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUsageBeijingDedupAndReasoningIncluded(t *testing.T) {
	day := "2026-10-06"
	start, _ := time.ParseInLocation("2006-01-02", day, Beijing)
	line := func(id, model string, at int64) string {
		return fmt.Sprintf(`{"type":"usage","requestId":%q,"authId":"a","requestedAtMs":%d,"model":%q,"serviceTier":"standard","usage":{"inputTokens":1000,"cachedTokens":400,"outputTokens":100,"reasoningTokens":50,"tokenBreakdown":{"input":{"cache_write_tokens":100}}}}`, id, at, model)
	}
	one := line("1", "gpt-6.1-sol", start.UnixMilli())
	s, e := Summarize(strings.NewReader(strings.Join([]string{line("before", "gpt-6.1-sol", start.Add(-time.Millisecond).UnixMilli()), one, one, line("2", "unknown", start.Add(time.Hour).UnixMilli()), line("after", "gpt-6.1-sol", start.AddDate(0, 0, 1).UnixMilli())}, "\n")), day)
	if e != nil || s.Requests != 2 || s.Total != 2200 || s.Reasoning != 100 || s.UnpricedTokens != 1100 {
		t.Fatalf("bad aggregation %+v %v", s, e)
	}
	if diff := s.USD - .00229; diff > 1e-10 || diff < -1e-10 {
		t.Fatalf("reasoning charged twice or cache arithmetic wrong: %f", s.USD)
	}
	if _, ok := Estimate("gpt-6.1-sol", "ultrafast", Tokens{}); ok {
		t.Fatal("invented unsupported rate")
	}
}
func TestReportReceiptAndIdempotency(t *testing.T) {
	s, _ := Open(t.TempDir())
	r := NewReport("cloud", "usage", "2026-10-06", "report")
	s.Put(r)
	s.Receipt(r.ID, false, "wechat_not_accepted", time.Now())
	s.Put(r)
	if len(s.Pending(time.Now())) != 0 {
		t.Fatal("retry erased backoff")
	}
	s.Receipt(r.ID, true, "", time.Now())
	s.Put(r)
	reopen, _ := Open(s.Dir)
	if len(reopen.Pending(time.Now().Add(time.Hour))) != 0 {
		t.Fatal("accepted report replayed")
	}
	if s.Receipt("../../bad", true, "", time.Now()) == nil {
		t.Fatal("unsafe receipt")
	}
}
func TestReleaseAllowlistAndVersions(t *testing.T) {
	for _, bad := range []string{"https://github.com.evil/openai/codex/releases/download/v1/a.zip", "http://github.com/openai/codex/releases/download/v1/a.zip", "https://github.com/openai/codex/releases/download/../a.zip", "https://api.github.com/repos/evil/x/releases/latest", "https://github.com/openai/codex/releases/download/v1/a.zip?token=secret"} {
		if AllowedURL(bad) {
			t.Fatal(bad)
		}
	}
	if !AllowedURL("https://github.com/openai/codex/releases/download/rust-v0.160.1/codex.zip") || !Newer("rust-v0.161.0", "0.160.1") || Newer("v0.160.0", "0.160.1") || Newer("v0.161.0-alpha", "0.160.1") {
		t.Fatal("bad release policy")
	}
}

type fakeDoer struct {
	fn func(*http.Request) (*http.Response, error)
}

func (f fakeDoer) Do(r *http.Request) (*http.Response, error) { return f.fn(r) }
func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}
}
func TestAccountsQuotaRetainedPermanentQuarantinedAndRefreshPersisted(t *testing.T) {
	for _, scenario := range []string{"quota", "network", "revoked", "refresh"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			id := "account-1"
			claims, _ := json.Marshal(map[string]int64{"exp": time.Now().Add(7 * 24 * time.Hour).Unix()})
			if scenario == "refresh" || scenario == "revoked" {
				claims, _ = json.Marshal(map[string]int64{"exp": time.Now().Add(-time.Hour).Unix()})
			}
			token := "x." + base64.RawURLEncoding.EncodeToString(claims) + ".x"
			native := filepath.Join(root, "cockpit-data", "codex_accounts", id+".json")
			authPath := filepath.Join(root, "auths", id+".json")
			AtomicJSON(native, map[string]any{"id": id, "email": "hello@example.com", "tokens": map[string]any{"access_token": token, "refresh_token": "private-refresh", "id_token": "private-id"}})
			AtomicJSON(authPath, map[string]any{"access_token": token, "disabled": false})
			c := fakeDoer{func(req *http.Request) (*http.Response, error) {
				if strings.Contains(req.URL.Path, "responses") {
					t.Fatal("inference called")
				}
				if req.Method == "POST" {
					if scenario == "revoked" {
						return response(400, `{"error":"invalid_grant"}`), nil
					}
					return response(200, `{"access_token":"fresh","refresh_token":"rotated"}`), nil
				}
				if scenario == "quota" {
					return response(429, `{"error":{"code":"usage_limit_reached"}}`), nil
				}
				if scenario == "network" {
					return nil, fmt.Errorf("network")
				}
				a, _ := readMap(native)
				tokens := a["tokens"].(map[string]any)
				if tokens["refresh_token"] != "rotated" {
					t.Fatal("rotated token not saved before network")
				}
				return response(200, `{}`), nil
			}}
			sum, e := CheckAccounts(context.Background(), c, root, false)
			if e != nil {
				t.Fatal(e)
			}
			auth, _ := readMap(authPath)
			if scenario == "revoked" {
				if sum.Quarantined != 1 || auth["disabled"] != true {
					t.Fatal("not quarantined")
				}
			} else if auth["disabled"] != false || sum.Quarantined != 0 {
				t.Fatal("incorrect quarantine")
			}
			if scenario == "refresh" {
				if !sum.Reload || !sum.Accounts[0].Refreshed {
					t.Fatal("refresh missing")
				}
				a, _ := readMap(native)
				if a["tokens"].(map[string]any)["refresh_token"] != "rotated" || auth["refresh_token"] != "" {
					t.Fatal("refresh ownership violated")
				}
			}
		})
	}
}
func TestPrivateRoutesRequireAuthAndNeverAcceptTraversal(t *testing.T) {
	s, _ := Open(t.TempDir())
	h := Handler(s, "private-key")
	r := httptest.NewRequest("POST", "/maintenance/report", bytes.NewBufferString(`{}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("anonymous management exposed")
	}
	r.Header.Set("Authorization", "Bearer private-key")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal("invalid report accepted")
	}
	entries, _ := os.ReadDir(s.Dir)
	if len(entries) != 0 {
		t.Fatal("invalid report persisted")
	}
}
