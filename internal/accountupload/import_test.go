package accountupload

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func jwt(v map[string]any) string {
	b, _ := json.Marshal(v)
	return "header." + base64.RawURLEncoding.EncodeToString(b) + ".signature"
}
func fixtureAccount(email, workspace, refresh string) Account {
	return Account{Email: email, AccountID: workspace, UserID: "user-" + strings.Split(email, "@")[0], PlanType: "plus", AccessToken: jwt(map[string]any{"email": email, "https://api.openai.com/auth": map[string]any{"chatgpt_account_id": workspace, "chatgpt_user_id": "user-" + strings.Split(email, "@")[0]}}), IDToken: jwt(map[string]any{"email": email}), RefreshToken: refresh}
}
func fixturePool(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	putMap(t, filepath.Join(root, "cockpit-data", "codex_accounts.json"), map[string]any{"version": "1.0", "accounts": []any{}, "current_account_id": nil})
	putMap(t, filepath.Join(root, "manifest.json"), map[string]any{"accounts": []any{}, "modelIds": []any{"chosen-model"}, "apiKeys": []any{map[string]any{"id": "campus-worker", "key": "test-private-key", "accountIds": []any{}, "allowedModels": []any{"chosen-model"}}, map[string]any{"id": "restricted", "key": "restricted-key", "accountIds": []any{"do-not-expand"}}}})
	putMap(t, filepath.Join(root, "config.json"), map[string]any{"api-key-account-ids": map[string]any{"test-private-key": []any{}, "restricted-key": []any{"do-not-expand"}}, "request-retry": json.Number("1")})
	return root
}
func putMap(t *testing.T, p string, v any) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(v)
	if e := os.WriteFile(p, b, 0600); e != nil {
		t.Fatal(e)
	}
}
func getMap(t *testing.T, p string) map[string]any {
	t.Helper()
	b, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	var v map[string]any
	if json.Unmarshal(b, &v) != nil {
		t.Fatal("invalid saved JSON")
	}
	return v
}

func TestParseFormatsRejectsInvalidBatchAndDeduplicates(t *testing.T) {
	a := fixtureAccount("person@example.com", "workspace-1", "refresh-fixture")
	flat := map[string]any{"type": "codex", "email": a.Email, "account_id": a.AccountID, "access_token": a.AccessToken, "id_token": a.IDToken, "refresh_token": a.RefreshToken}
	nested := map[string]any{"tokens": map[string]any{"access_token": a.AccessToken, "id_token": a.IDToken, "account_id": a.AccountID, "refresh_token": a.RefreshToken}}
	for _, v := range []any{flat, []any{flat}, map[string]any{"accounts": []any{nested}}, map[string]any{"tokens": nested["tokens"]}} {
		b, _ := json.Marshal(v)
		rows, _, e := Parse(b)
		if e != nil || len(rows) != 1 || rows[0].Email != a.Email || rows[0].AccountID != a.AccountID {
			t.Fatal("supported export rejected", e)
		}
	}
	b, _ := json.Marshal([]any{flat, flat})
	rows, dups, e := Parse(b)
	if e != nil || len(rows) != 1 || dups != 1 {
		t.Fatal("duplicate account not collapsed")
	}
	for _, v := range []string{`[]`, `{"accounts":[]}`, `[{"type":"claude"}]`, `[null]`, string(b) + ` {}`, `[{"access_token":"fake","email":"person@example.com","account_id":"x"}]`} {
		if _, _, e := Parse([]byte(v)); e == nil {
			t.Fatal("bad batch accepted")
		}
	}
}

func TestMergeUpdatesStableIdentityPreservesRefreshAndPolicies(t *testing.T) {
	root := fixturePool(t)
	a := fixtureAccount("person@example.com", "workspace-1", "old-refresh")
	sum, e := Merge(root, "111111111111111111111111", []Account{a})
	if e != nil || sum.Added != 1 {
		t.Fatal(sum, e)
	}
	m := getMap(t, filepath.Join(root, "manifest.json"))
	rows := m["accounts"].([]any)
	id := rows[0].(map[string]any)["id"].(string)
	nativePath := filepath.Join(root, "cockpit-data", "codex_accounts", id+".json")
	native := getMap(t, nativePath)
	native["tags"] = []any{"preserve-me"}
	putMap(t, nativePath, native)
	a.Email = "PERSON@example.com"
	a.RefreshToken = ""
	a.AccessToken += "-updated"
	sum, e = Merge(root, "222222222222222222222222", []Account{a})
	if e != nil || sum.Added != 0 || sum.Updated != 1 {
		t.Fatal(sum, e)
	}
	native = getMap(t, nativePath)
	if native["tokens"].(map[string]any)["refresh_token"] != "old-refresh" || native["tags"].([]any)[0] != "preserve-me" {
		t.Fatal("existing account settings lost")
	}
	auth := getMap(t, filepath.Join(root, "auths", id+".json"))
	if auth["refresh_token"] != "" {
		t.Fatal("sidecar received authoritative refresh credential")
	}
	m = getMap(t, filepath.Join(root, "manifest.json"))
	if len(m["accounts"].([]any)) != 1 || m["modelIds"].([]any)[0] != "chosen-model" {
		t.Fatal("duplicate or model policy changed")
	}
	keys := m["apiKeys"].([]any)
	if len(keys[0].(map[string]any)["accountIds"].([]any)) != 1 || len(keys[1].(map[string]any)["accountIds"].([]any)) != 1 {
		t.Fatal("API key scopes changed incorrectly")
	}
	a.AccountID = "different-workspace"
	a.UserID = "same-person-other-workspace"
	sum, e = Merge(root, "333333333333333333333333", []Account{a})
	if e != nil || sum.Added != 1 {
		t.Fatal("different workspace overwritten", e)
	}
}

func TestMergeDoesNotTouchPoolWhenBatchContainsInvalidAccount(t *testing.T) {
	root := fixturePool(t)
	before, _ := os.ReadFile(filepath.Join(root, "manifest.json"))
	_, e := Merge(root, "444444444444444444444444", []Account{fixtureAccount("person@example.com", "one", "rt"), {Email: "bad", AccountID: "two"}})
	after, _ := os.ReadFile(filepath.Join(root, "manifest.json"))
	if e == nil || string(before) != string(after) {
		t.Fatal("invalid batch partially applied")
	}
}

func TestMergeWaitsForCatalogWithoutPlanAllowlist(t *testing.T) {
	for _, plan := range []string{"free", "go", "team", "plus", ""} {
		t.Run(plan, func(t *testing.T) {
			root := fixturePool(t)
			a := fixtureAccount("person@example.com", "workspace-1", "private-refresh")
			a.PlanType = plan
			if _, e := Merge(root, "111111111111111111111111", []Account{a}); e != nil {
				t.Fatal(e)
			}
			m := getMap(t, filepath.Join(root, "manifest.json"))
			id := m["accounts"].([]any)[0].(map[string]any)["id"].(string)
			p := filepath.Join(root, "auths", id+".json")
			auth := getMap(t, p)
			encoded, _ := json.Marshal(auth["excluded_models"])
			if !strings.Contains(string(encoded), "chosen-model") || strings.Contains(string(encoded), "gpt-6.1-sol") {
				t.Fatalf("pending catalog rules ignored configured models for %q: %s", plan, encoded)
			}
			auth["excluded_models"] = append(auth["excluded_models"].([]any), "explicit-model-restriction")
			putMap(t, p, auth)
			if _, e := Merge(root, "222222222222222222222222", []Account{a}); e != nil {
				t.Fatal(e)
			}
			encoded, _ = json.Marshal(getMap(t, p)["excluded_models"])
			if !strings.Contains(string(encoded), "explicit-model-restriction") || strings.Count(string(encoded), "chosen-model") != 1 {
				t.Fatal("lost or duplicated model exclusion")
			}
			m = getMap(t, filepath.Join(root, "manifest.json"))
			rules, _ := json.Marshal(m["accountModelRules"])
			if !strings.Contains(string(rules), id) || !strings.Contains(string(rules), "chosen-model") {
				t.Fatal("native manifest did not retain pending catalog rule")
			}
		})
	}
}
