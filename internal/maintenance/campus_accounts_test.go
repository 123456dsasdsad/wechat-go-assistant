package maintenance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestReportHostSnapshotIsAuthenticatedAndIndependent(t *testing.T) {
	s, _ := Open(t.TempDir())
	cloud := NewReport("cloud", "accounts", "2026-10-08", "pool details")
	cloud.Created = time.Date(2026, 10, 7, 23, 5, 0, 0, time.UTC)
	s.Put(cloud)
	campus := NewReport("campus", "accounts", "2026-10-08", "campus summary")
	campus.Created = cloud.Created.Add(time.Hour)
	s.Put(campus)
	h := Handler(s, "key")
	for _, tc := range []struct {
		host, key string
		code      int
	}{{"cloud", "", 401}, {"cloud", "key", 200}, {"invalid", "key", 400}} {
		r := httptest.NewRequest("GET", "/maintenance/reports?kind=accounts&host="+tc.host, nil)
		r.Header.Set("Authorization", "Bearer "+tc.key)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Fatalf("host=%s status=%d", tc.host, w.Code)
		}
		if w.Code == 200 {
			var result struct {
				Report Report `json:"report"`
			}
			if json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Report.ID != cloud.ID {
				t.Fatal("campus replaced cloud source")
			}
		}
	}
	empty, _ := Open(t.TempDir())
	r := httptest.NewRequest("GET", "/maintenance/reports?host=cloud&kind=accounts", nil)
	r.Header.Set("Authorization", "Bearer key")
	w := httptest.NewRecorder()
	Handler(empty, "key").ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatal("missing snapshot presented as checked")
	}
}

func TestCampusAccountsDetailsFreshnessAndConnectivity(t *testing.T) {
	now := time.Date(2026, 10, 8, 8, 0, 0, 0, Beijing)
	for _, scenario := range []string{"fresh", "stale", "missing", "unauthorized", "malformed", "gateway-down"} {
		t.Run(scenario, func(t *testing.T) {
			day := "2026-10-08"
			cloud := NewReport("cloud", "accounts", day, "总数 1；可用 1\nab***@example.com：有效\n5 小时额度：剩余 99%；重置 10-08 12:00\n168 小时额度：剩余 80%\n本次清理：隔离 0 个失效账号。")
			cloud.Created = now.Add(-time.Hour)
			if scenario == "stale" {
				cloud.Day = "2026-10-07"
				cloud.Created = now.Add(-25 * time.Hour)
			}
			calls := 0
			client := fakeDoer{func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != "GET" || strings.Contains(r.URL.Path, "responses") {
					t.Fatal("inference or credential mutation")
				}
				if strings.HasPrefix(r.URL.Path, "/maintenance") {
					if r.Header.Get("Authorization") != "Bearer relay-key" {
						t.Fatal("wrong relay key")
					}
					if scenario == "missing" {
						return response(404, "{}"), nil
					}
					b, _ := json.Marshal(map[string]any{"report": cloud})
					return response(200, string(b)), nil
				}
				if r.URL.Path == "/health" {
					if scenario == "unauthorized" {
						return response(401, "{}"), nil
					}
					return response(200, `{"ok":true}`), nil
				}
				if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer gateway-key" {
					t.Fatal("wrong model probe")
				}
				if scenario == "malformed" {
					return response(200, `{"data":[{}]}`), nil
				}
				if scenario == "gateway-down" {
					return response(502, "{}"), nil
				}
				return response(200, `{"data":[{"id":"model-a"},{"id":"model-b"}]}`), nil
			}}
			got := CheckCampusAccounts(context.Background(), client, "http://relay", "relay-key", "http://gateway/v1", "gateway-key", day, now)
			text := got.Text()
			if calls != 3 || !strings.Contains(text, "2026-10-08 08:00:00") {
				t.Fatalf("missing actual check time: %s", text)
			}
			if scenario == "missing" {
				if !strings.Contains(text, "未取得共享账号检查明细") || got.Source != nil {
					t.Fatal(text)
				}
			} else if !strings.Contains(text, "ab***@example.com") || !strings.Contains(text, "剩余 80%") || !strings.Contains(text, "来源检查时间：2026-10-08 07:00:00") && scenario != "stale" {
				t.Fatal(text)
			}
			if (scenario == "stale") != got.Stale {
				t.Fatalf("bad freshness: %+v", got)
			}
			if scenario == "stale" && !strings.Contains(text, "旧快照") {
				t.Fatal(text)
			}
			if scenario == "unauthorized" && got.Relay.OK {
				t.Fatal("rejected authentication counted healthy")
			}
			if (scenario == "malformed" || scenario == "gateway-down") && got.Gateway.OK {
				t.Fatal("unverified gateway counted healthy")
			}
			if scenario == "fresh" && (!got.Gateway.OK || got.Gateway.Models != 2) {
				t.Fatal(text)
			}
			later := got
			later.Checked = now.Add(time.Minute)
			if !got.SameResult(later) {
				t.Fatal("unchanged retry will spam")
			}
			later.Stale = !got.Stale
			if got.SameResult(later) {
				t.Fatal("freshness change suppressed")
			}
		})
	}
}
