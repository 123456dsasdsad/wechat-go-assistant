package maintenance

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
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
