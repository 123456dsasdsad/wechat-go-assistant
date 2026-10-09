package maintenance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAccountCatalogUsesReturnedModelsAndKeepsOperatorRules(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(root, "manifest.json")
	manifest := map[string]any{"accounts": []any{map[string]any{"id": "free-allowed", "planType": "free"}, map[string]any{"id": "team-restricted", "planType": "team"}}, "modelIds": []any{"gpt-6.1-sol", "gpt-6-luna"}, "apiKeys": []any{map[string]any{"key": "private-fixture", "accountIds": []any{"free-allowed", "team-restricted"}}}, "accountModelRules": []any{map[string]any{"accountId": "free-allowed", "excludedModels": []any{"operator-model", "gpt-6.1-sol", "gpt-6-luna"}}}}
	AtomicJSON(manifestPath, manifest)
	for _, id := range []string{"free-allowed", "team-restricted"} {
		excluded := []string{"gpt-6.1-sol", "gpt-6-luna"}
		if id == "free-allowed" {
			excluded = append(excluded, "operator-model")
		}
		AtomicJSON(filepath.Join(root, "auths", id+".json"), map[string]any{"account_id": id, "access_token": "fixture-token", "plan_type": strings.Split(id, "-")[0], "excluded_models": excluded, "catalog_excluded_models": []string{"gpt-6.1-sol", "gpt-6-luna"}})
	}
	client := fakeDoer{func(req *http.Request) (*http.Response, error) {
		if req.Method != "GET" || !strings.HasSuffix(req.URL.Path, "/codex/models") || req.Body != nil && req.ContentLength > 0 {
			t.Fatal("catalog check called inference")
		}
		if req.Header.Get("ChatGPT-Account-Id") == "free-allowed" {
			return response(200, `{"models":[{"slug":"gpt-6.1-sol","supported_in_api":true},{"slug":"gpt-6-luna","supported_in_api":true}]}`), nil
		}
		return response(200, `{"models":[{"slug":"gpt-6-luna","supported_in_api":true}]}`), nil
	}}
	sum, e := SyncModelCatalog(context.Background(), client, root, false)
	if e != nil || !sum.Reload || sum.Pending != 0 {
		t.Fatal(sum, e)
	}
	free, _ := readMap(filepath.Join(root, "auths", "free-allowed.json"))
	team, _ := readMap(filepath.Join(root, "auths", "team-restricted.json"))
	freeRules, _ := modelStrings(free["excluded_models"])
	teamRules, _ := modelStrings(team["excluded_models"])
	if len(freeRules) != 1 || freeRules[0] != "operator-model" || len(teamRules) != 1 || teamRules[0] != "gpt-6.1-sol" {
		t.Fatalf("plan label overrode returned catalog: %v %v", freeRules, teamRules)
	}
	updated, _ := readMap(manifestPath)
	originalKeys, _ := json.Marshal(manifest["apiKeys"])
	updatedKeys, _ := json.Marshal(updated["apiKeys"])
	if string(originalKeys) != string(updatedKeys) {
		t.Fatal("API key scopes changed")
	}
	// A later entitlement change removes only the automatic exclusion.
	team["catalog_checked_at"] = ""
	AtomicJSON(filepath.Join(root, "auths", "team-restricted.json"), team)
	client.fn = func(*http.Request) (*http.Response, error) {
		return response(200, `{"models":[{"slug":"gpt-6.1-sol","supported_in_api":true},{"slug":"gpt-6-luna","supported_in_api":true}]}`), nil
	}
	if _, e = SyncModelCatalog(context.Background(), client, root, false); e != nil {
		t.Fatal(e)
	}
	team, _ = readMap(filepath.Join(root, "auths", "team-restricted.json"))
	teamRules, _ = modelStrings(team["excluded_models"])
	if len(teamRules) != 0 {
		t.Fatal("restored entitlement remains excluded")
	}
}

func TestCatalogFailuresPreserveKnownRulesAndDryRunDoesNotWrite(t *testing.T) {
	for _, body := range []string{`{"models":[]}`, `{"unexpected":true}`, `{"models":[{"slug":"gpt-6.1-sol"}]}`} {
		root := t.TempDir()
		path := filepath.Join(root, "auths", "one.json")
		AtomicJSON(filepath.Join(root, "manifest.json"), map[string]any{"accounts": []any{map[string]any{"id": "one"}}, "modelIds": []string{"gpt-6.1-sol"}})
		AtomicJSON(path, map[string]any{"account_id": "one", "access_token": "private-fixture", "excluded_models": []string{"gpt-6.1-sol"}, "catalog_excluded_models": []string{"gpt-6.1-sol"}})
		client := fakeDoer{func(*http.Request) (*http.Response, error) { return response(200, body), nil }}
		sum, e := SyncModelCatalog(context.Background(), client, root, false)
		if e != nil || sum.Pending != 1 || sum.Reload {
			t.Fatal(sum, e)
		}
		auth, _ := readMap(path)
		rules, _ := modelStrings(auth["excluded_models"])
		if len(rules) != 1 || rules[0] != "gpt-6.1-sol" {
			t.Fatal("unverified catalog removed routing protection")
		}
		client.fn = func(*http.Request) (*http.Response, error) {
			return response(200, `{"models":[{"slug":"gpt-6.1-sol","supported_in_api":true}]}`), nil
		}
		if _, e = SyncModelCatalog(context.Background(), client, root, true); e != nil {
			t.Fatal(e)
		}
		auth, _ = readMap(path)
		rules, _ = modelStrings(auth["excluded_models"])
		if len(rules) != 1 {
			t.Fatal("dry run wrote live rule")
		}
	}
}

func TestUnauthorizedCatalogSuspendsRoutingAndRestoredAuthorizationRecovers(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(root, "manifest.json")
	authPath := filepath.Join(root, "auths", "one.json")
	manifest := map[string]any{
		"accounts":          []any{map[string]any{"id": "one"}},
		"modelIds":          []string{"gpt-6.1-sol", "gpt-5.6-luna"},
		"apiKeys":           []any{map[string]any{"key": "private-fixture", "accountIds": []string{"one"}}},
		"accountModelRules": []any{map[string]any{"accountId": "one", "excludedModels": []string{"operator-model"}}},
	}
	auth := map[string]any{"account_id": "one", "access_token": "private-fixture", "refresh_token": "", "excluded_models": []string{"operator-model"}, "catalog_excluded_models": []string{}}
	if e := AtomicJSON(manifestPath, manifest); e != nil {
		t.Fatal(e)
	}
	if e := AtomicJSON(authPath, auth); e != nil {
		t.Fatal(e)
	}
	client := fakeDoer{func(req *http.Request) (*http.Response, error) {
		if req.Method != "GET" || !strings.HasSuffix(req.URL.Path, "/codex/models") {
			t.Fatal("authorization check called inference")
		}
		return response(401, `{"error":{"code":"invalid_token"}}`), nil
	}}
	sum, e := SyncModelCatalog(context.Background(), client, root, false)
	if e != nil || !sum.Reload || sum.Pending != 0 || sum.Suspended != 1 || sum.Accounts[0].State != "authorization_pending" {
		t.Fatal(sum, e)
	}
	current, e := readMap(authPath)
	if e != nil {
		t.Fatal(e)
	}
	rules, _ := modelStrings(current["excluded_models"])
	if len(rules) != 3 || !containsModel(rules, "gpt-6.1-sol") || !containsModel(rules, "gpt-5.6-luna") || !containsModel(rules, "operator-model") {
		t.Fatal("unauthorized credentials remained runnable", rules)
	}
	if str(current, "access_token") != "private-fixture" || current["disabled"] == true || current["catalog_authorization_pending"] != true {
		t.Fatal("temporary routing suspension destroyed or disabled credentials")
	}
	updated, _ := readMap(manifestPath)
	beforeKeys, _ := json.Marshal(manifest["apiKeys"])
	afterKeys, _ := json.Marshal(updated["apiKeys"])
	if string(beforeKeys) != string(afterKeys) {
		t.Fatal("API key scope changed")
	}
	// A recently cached catalog must not hide an authorization recovery check.
	current["catalog_checked_at"] = time.Now().UTC().Format(time.RFC3339)
	current["catalog_checked_models"] = []string{"gpt-6.1-sol", "gpt-5.6-luna"}
	if e = AtomicJSON(authPath, current); e != nil {
		t.Fatal(e)
	}
	client.fn = func(*http.Request) (*http.Response, error) {
		return response(200, `{"models":[{"slug":"gpt-6.1-sol","supported_in_api":true},{"slug":"gpt-5.6-luna","supported_in_api":true}]}`), nil
	}
	sum, e = SyncModelCatalog(context.Background(), client, root, false)
	if e != nil || !sum.Reload || sum.Pending != 0 || sum.Suspended != 0 || sum.Accounts[0].State != "verified" {
		t.Fatal(sum, e)
	}
	current, _ = readMap(authPath)
	rules, _ = modelStrings(current["excluded_models"])
	if len(rules) != 1 || rules[0] != "operator-model" || current["catalog_authorization_pending"] != nil {
		t.Fatal("restored account kept an automatic suspension or lost operator policy", rules)
	}
}

func TestCatalogTransientFailureAndUnauthorizedDryRunPreserveFiles(t *testing.T) {
	for _, tc := range []struct {
		status int
		dry    bool
	}{{503, false}, {401, true}} {
		t.Run(fmt.Sprintf("status_%d_dry_%t", tc.status, tc.dry), func(t *testing.T) {
			root := t.TempDir()
			manifestPath := filepath.Join(root, "manifest.json")
			authPath := filepath.Join(root, "auths", "one.json")
			if e := AtomicJSON(manifestPath, map[string]any{"accounts": []any{map[string]any{"id": "one"}}, "modelIds": []string{"gpt-6.1-sol"}}); e != nil {
				t.Fatal(e)
			}
			if e := AtomicJSON(authPath, map[string]any{"account_id": "one", "access_token": "fixture", "excluded_models": []string{}, "catalog_excluded_models": []string{}}); e != nil {
				t.Fatal(e)
			}
			beforeAuth, _ := os.ReadFile(authPath)
			beforeManifest, _ := os.ReadFile(manifestPath)
			client := fakeDoer{func(*http.Request) (*http.Response, error) {
				return response(tc.status, `{"error":{"code":"test_error"}}`), nil
			}}
			sum, e := SyncModelCatalog(context.Background(), client, root, tc.dry)
			if e != nil {
				t.Fatal(e)
			}
			if tc.status == 503 && (sum.Pending != 1 || sum.Suspended != 0 || sum.Reload) {
				t.Fatal(sum)
			}
			afterAuth, _ := os.ReadFile(authPath)
			afterManifest, _ := os.ReadFile(manifestPath)
			if string(beforeAuth) != string(afterAuth) || string(beforeManifest) != string(afterManifest) {
				t.Fatal("dry run or transient failure changed routing")
			}
		})
	}
}

func TestCatalogSuspensionPreservesOperatorExclusionForConfiguredModel(t *testing.T) {
	auth := map[string]any{"excluded_models": []string{"gpt-6.1-sol"}, "catalog_excluded_models": []string{}}
	manifest := map[string]any{"accountModelRules": []any{map[string]any{"accountId": "one", "excludedModels": []string{"gpt-6.1-sol"}}}}
	if e := CatalogExclusions(auth, manifest, "one", []string{"gpt-6.1-sol", "gpt-5.6-luna"}); e != nil {
		t.Fatal(e)
	}
	if e := CatalogExclusions(auth, manifest, "one", nil); e != nil {
		t.Fatal(e)
	}
	rules, _ := modelStrings(auth["excluded_models"])
	rows := manifest["accountModelRules"].([]any)
	operator, _ := modelStrings(rows[0].(map[string]any)["excludedModels"])
	if len(rules) != 1 || rules[0] != "gpt-6.1-sol" || len(operator) != 1 || operator[0] != "gpt-6.1-sol" {
		t.Fatal("automatic suspension took ownership of an existing operator exclusion", rules, operator)
	}
}
