package accountupload

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func twoAccountPool(t *testing.T) (string, []string) {
	t.Helper()
	root := fixturePool(t)
	a := fixtureAccount("free@example.com", "free-workspace", "free-refresh")
	a.PlanType = "free"
	b := fixtureAccount("team@example.com", "team-workspace", "team-refresh")
	b.PlanType = "team"
	if _, err := Merge(root, "111111111111111111111111", []Account{a, b}); err != nil {
		t.Fatal(err)
	}
	views, err := ListAccounts(root)
	if err != nil || len(views) != 2 {
		t.Fatal(views, err)
	}
	return root, []string{views[0].ID, views[1].ID}
}

func TestDeleteRemovesCredentialsIndicesAndEveryScope(t *testing.T) {
	root, ids := twoAccountPool(t)
	index := getMap(t, filepath.Join(root, "cockpit-data", "codex_accounts.json"))
	index["current_account_id"] = ids[0]
	putMap(t, filepath.Join(root, "cockpit-data", "codex_accounts.json"), index)
	manifest := getMap(t, filepath.Join(root, "manifest.json"))
	restricted := object(manifest["apiKeys"].([]any)[1])
	restricted["accountIds"] = []any{ids[0]}
	restricted["enabled"] = true
	putMap(t, filepath.Join(root, "manifest.json"), manifest)
	cfg := getMap(t, filepath.Join(root, "config.json"))
	object(cfg["api-key-account-ids"])["restricted-key"] = []any{ids[0]}
	cfg["api-keys"] = []any{"test-private-key", "restricted-key"}
	putMap(t, filepath.Join(root, "config.json"), cfg)
	remainingNative, _ := os.ReadFile(filepath.Join(root, "cockpit-data", "codex_accounts", ids[1]+".json"))
	sum, err := Delete(root, "222222222222222222222222", ids[:1])
	if err != nil || sum.Deleted != 1 || sum.Total != 1 {
		t.Fatal(sum, err)
	}
	for _, p := range []string{filepath.Join("auths", ids[0]+".json"), filepath.Join("cockpit-data", "codex_accounts", ids[0]+".json")} {
		if _, err := os.Stat(filepath.Join(root, p)); !os.IsNotExist(err) {
			t.Fatal("deleted credentials remain", p, err)
		}
	}
	for _, p := range []string{"manifest.json", "config.json", filepath.Join("cockpit-data", "codex_accounts.json")} {
		data, _ := os.ReadFile(filepath.Join(root, p))
		if strings.Contains(string(data), ids[0]) {
			t.Fatal("deleted ID remains", p)
		}
	}
	index = getMap(t, filepath.Join(root, "cockpit-data", "codex_accounts.json"))
	if index["current_account_id"] != nil {
		t.Fatal("deleted current account retained")
	}
	manifest = getMap(t, filepath.Join(root, "manifest.json"))
	if object(manifest["apiKeys"].([]any)[1])["enabled"] != false {
		t.Fatal("emptied scope broadened to other accounts")
	}
	cfg = getMap(t, filepath.Join(root, "config.json"))
	if len(cfg["api-keys"].([]any)) != 1 || cfg["request-retry"].(float64) != 1 {
		t.Fatal("configuration changed")
	}
	actual, _ := os.ReadFile(filepath.Join(root, "cockpit-data", "codex_accounts", ids[1]+".json"))
	if string(actual) != string(remainingNative) {
		t.Fatal("remaining credentials changed")
	}
	views, err := ListAccounts(root)
	if err != nil || len(views) != 1 || views[0].ID != ids[1] {
		t.Fatal(views, err)
	}
}

func TestDeleteCommittedRetryAndInvalidIDs(t *testing.T) {
	root, ids := twoAccountPool(t)
	before, _ := os.ReadFile(filepath.Join(root, "manifest.json"))
	for _, bad := range [][]string{nil, {"../secret"}, {ids[0], ids[0]}, {"missing"}} {
		if _, err := Delete(root, "333333333333333333333333", bad); err == nil {
			t.Fatal("invalid deletion accepted", bad)
		}
		after, _ := os.ReadFile(filepath.Join(root, "manifest.json"))
		if string(before) != string(after) {
			t.Fatal("invalid deletion wrote pool")
		}
	}
	if _, err := Delete(root, "444444444444444444444444", ids[:1]); err != nil {
		t.Fatal(err)
	}
	// Recreate the prior identity to show a committed retry never replays writes.
	data, _ := os.ReadFile(filepath.Join(root, "deletions-backup", "444444444444444444444444", "transaction.json"))
	var tx transaction
	if decode(data, &tx) != nil || restore(root, tx) != nil {
		t.Fatal("cannot restore deletion backup")
	}
	if sum, err := Delete(root, "444444444444444444444444", ids[:1]); err != nil || sum.Deleted != 1 {
		t.Fatal(sum, err)
	}
	if _, err := os.Stat(filepath.Join(root, "auths", ids[0]+".json")); err != nil {
		t.Fatal("committed deletion replayed")
	}
}

func TestDeletionQueueRetriesOnlyActivation(t *testing.T) {
	root, ids := twoAccountPool(t)
	store, _ := Open(t.TempDir())
	receipt, err := store.StageDelete("owner", "source", root, ids[:1])
	if err != nil || receipt.Operation != "delete" || receipt.Status != "pending" {
		t.Fatal(receipt, err)
	}
	if _, err := os.Stat(filepath.Join(root, "auths", ids[0]+".json")); err != nil {
		t.Fatal("staging mutated live pool")
	}
	if err := store.Process(receipt.ID, root, func() error { return errors.New("reload failed") }); err == nil {
		t.Fatal("failed reload claimed success")
	}
	r, _ := store.Status("owner", receipt.ID)
	if r.Status != "applied" || r.Deleted != 1 {
		t.Fatal(r)
	}
	retry, err := store.StageDelete("owner", "source", root, ids[:1])
	if err != nil || retry.ID != receipt.ID {
		t.Fatal("retry not idempotent", err)
	}
	store, _ = Open(store.dir)
	if err := store.Process(receipt.ID, root, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	r, _ = store.Status("owner", receipt.ID)
	if r.Status != "active" || r.Deleted != 1 || r.Total != 1 || !strings.Contains(r.Text(), "未调用 AI") || !strings.Contains(r.Text(), "已删除 1") {
		t.Fatal(r)
	}
}

func TestDeleteLastAccountsThenUploadRestoresSharedPoolKey(t *testing.T) {
	root, ids := twoAccountPool(t)
	if sum, err := Delete(root, "555555555555555555555555", ids); err != nil || sum.Total != 0 {
		t.Fatal(sum, err)
	}
	if _, err := Merge(root, "666666666666666666666666", []Account{fixtureAccount("new@example.com", "new-workspace", "refresh")}); err != nil {
		t.Fatal(err)
	}
	manifest := getMap(t, filepath.Join(root, "manifest.json"))
	key := object(manifest["apiKeys"].([]any)[0])
	if key["enabled"] != true || key["disabledByAccountDeletion"] != nil || len(key["accountIds"].([]any)) != 1 {
		t.Fatal("shared key not restored", key)
	}
	cfg := getMap(t, filepath.Join(root, "config.json"))
	if len(cfg["api-keys"].([]any)) != 1 || cfg["api-keys"].([]any)[0] != "test-private-key" {
		t.Fatal("shared credential not enabled")
	}
}
