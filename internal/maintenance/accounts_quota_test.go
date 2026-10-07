package maintenance

import (
	"strings"
	"testing"
)

func TestAccountQuotaExhaustionDoesNotMeanInvalidCredential(t *testing.T) {
	for _, tc := range []struct {
		body, state string
		windows     int
	}{
		{`{"rate_limit":{"allowed":true,"limit_reached":false,"primary_window":{"used_percent":14,"limit_window_seconds":18000,"reset_at":1791324000}}}`, "有效", 1},
		{`{"rate_limit":{"allowed":false,"limit_reached":true,"primary_window":{"used_percent":100,"limit_window_seconds":18000}}}`, "额度用尽（保留）", 1},
		{`{"rate_limit":{"allowed":true,"secondary_window":{"used_percent":100}}}`, "额度用尽（保留）", 1},
		{`{}`, "授权有效，额度待核实（保留）", 0},
		{`{"rate_limit":{"primary_window":{"used_percent":null}}}`, "授权有效，额度待核实（保留）", 0},
		{`{"rate_limit":{"allowed":true,"primary_window":{"used_percent":120}}}`, "有效", 0},
	} {
		var a AccountCheck
		applyAccountQuota(&a, []byte(tc.body))
		if a.State != tc.state || len(a.Windows) != tc.windows {
			t.Fatalf("quota misclassified: %#v", a)
		}
		if a.State == "失效" {
			t.Fatal("quota must not trigger quarantine")
		}
	}
	var good, limited, unknown AccountCheck
	good.Alias = "账号1"
	applyAccountQuota(&good, []byte(`{"rate_limit":{"allowed":true,"primary_window":{"used_percent":25,"limit_window_seconds":18000}}}`))
	limited.Alias = "账号2"
	applyAccountQuota(&limited, []byte(`{"rate_limit":{"allowed":false}}`))
	unknown.Alias = "账号3"
	applyAccountQuota(&unknown, []byte(`{}`))
	text := (AccountSummary{Accounts: []AccountCheck{good, limited, unknown, {Alias: "账号4", State: "已隔离/停用"}}}).Text("2026-10-07")
	for _, want := range []string{"总数 4；可用 1；额度/频率受限 1；失效或停用 1；待核实 1", "剩余 75.0%", "隔离 0 个"} {
		if !strings.Contains(text, want) {
			t.Fatal("missing summary", want)
		}
	}
}
