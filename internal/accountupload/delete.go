package accountupload

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/maintenance"
)

// AccountView contains only the fields needed by an authenticated operator.
type AccountView struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	Plan          string `json:"plan"`
	Disabled      bool   `json:"disabled"`
	PendingDelete bool   `json:"pending_delete,omitempty"`
}

func ListAccounts(root string) ([]AccountView, error) {
	manifest, err := readMap(filepath.Join(root, "manifest.json"))
	if err != nil {
		return nil, err
	}
	rows, err := array(manifest, "accounts")
	if err != nil {
		return nil, err
	}
	views := make([]AccountView, 0, len(rows))
	seen := map[string]bool{}
	for _, row := range rows {
		record := object(row)
		id := text(record, "id")
		if !validID(id) || seen[id] {
			return nil, errors.New("pool_identity_invalid")
		}
		seen[id] = true
		native, err := readMap(filepath.Join(root, "cockpit-data", "codex_accounts", id+".json"))
		if err != nil {
			return nil, err
		}
		if text(native, "id") != id {
			return nil, errors.New("pool_identity_invalid")
		}
		auth, err := readMap(filepath.Join(root, "auths", id+".json"))
		if err != nil {
			return nil, err
		}
		disabled, _ := auth["disabled"].(bool)
		reauth, _ := native["requires_reauth"].(bool)
		views = append(views, AccountView{ID: id, Email: text(native, "email"), Plan: text(native, "plan_type"), Disabled: disabled || reauth})
	}
	return views, nil
}

func deletionIDs(ids []string) ([]string, error) {
	if len(ids) == 0 || len(ids) > MaxAccounts {
		return nil, errors.New("invalid_deletion")
	}
	result := append([]string(nil), ids...)
	sort.Strings(result)
	for i, id := range result {
		if !validID(id) || i > 0 && id == result[i-1] {
			return nil, errors.New("invalid_deletion")
		}
	}
	return result, nil
}

func (s *Store) StageDelete(owner, source, root string, ids []string) (Receipt, error) {
	if owner == "" || source == "" {
		return Receipt{}, errors.New("invalid_deletion_identity")
	}
	ids, err := deletionIDs(ids)
	if err != nil {
		return Receipt{}, err
	}
	data, _ := json.Marshal(ids)
	hash := sha256.Sum256(append([]byte(owner+"\x00delete\x00"+source+"\x00"), data...))
	id := hex.EncodeToString(hash[:12])
	s.mu.Lock()
	defer s.mu.Unlock()
	if b, err := s.readBatch(id); err == nil {
		return b.Receipt, nil
	}
	views, err := ListAccounts(root)
	if err != nil {
		return Receipt{}, err
	}
	existing := map[string]bool{}
	for _, v := range views {
		existing[v.ID] = true
	}
	for _, id := range ids {
		if !existing[id] {
			return Receipt{}, errors.New("account_not_found")
		}
	}
	now := time.Now().UTC()
	b := batch{Receipt: Receipt{ID: id, Operation: "delete", AccountIDs: ids, Status: "pending", Count: len(ids), Created: now, UpdatedAt: now}, Owner: owner}
	if err := s.save(b); err != nil {
		return Receipt{}, errors.New("account_delete_save_failed")
	}
	return b.Receipt, nil
}

func (s *Store) MarkPendingDeletes(views []AccountView) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pending := map[string]bool{}
	for _, b := range s.list() {
		if b.Operation == "delete" && (b.Status == "pending" || b.Status == "applied") {
			for _, id := range b.AccountIDs {
				pending[id] = true
			}
		}
	}
	for i := range views {
		views[i].PendingDelete = pending[views[i].ID]
	}
}

func withoutIDs(rows []any, ids map[string]bool) ([]any, error) {
	result := make([]any, 0, len(rows))
	for _, row := range rows {
		id, ok := row.(string)
		if !ok {
			return nil, errors.New("pool_scope_invalid")
		}
		if !ids[id] {
			result = append(result, row)
		}
	}
	return result, nil
}

// Delete journals all originals before removing live credentials. The caller
// holds MutationLock and an idle maintenance lease until the gateway reloads.
func Delete(root, batch string, ids []string) (Summary, error) {
	var sum Summary
	ids, err := deletionIDs(ids)
	if err != nil || !batchID(batch) {
		return sum, errors.New("invalid_deletion")
	}
	journal := filepath.Join(root, "deletions-backup", batch, "transaction.json")
	if data, err := os.ReadFile(journal); err == nil {
		var old transaction
		if decode(data, &old) != nil {
			return sum, errors.New("pool_backup_invalid")
		}
		if old.Committed {
			return old.Summary, nil
		}
		if err := restore(root, old); err != nil {
			return sum, err
		}
	} else if !os.IsNotExist(err) {
		return sum, errors.New("pool_backup_unavailable")
	}
	manifest, err := readMap(filepath.Join(root, "manifest.json"))
	if err != nil {
		return sum, err
	}
	pool, err := array(manifest, "accounts")
	if err != nil {
		return sum, err
	}
	index, err := readMap(filepath.Join(root, "cockpit-data", "codex_accounts.json"))
	if err != nil {
		return sum, err
	}
	indexRows, err := array(index, "accounts")
	if err != nil {
		return sum, err
	}
	cfg, err := readMap(filepath.Join(root, "config.json"))
	if err != nil {
		return sum, err
	}
	keys, err := array(manifest, "apiKeys")
	if err != nil {
		return sum, err
	}
	scopes := object(cfg["api-key-account-ids"])
	if scopes == nil {
		return sum, errors.New("pool_scope_invalid")
	}
	selected := map[string]bool{}
	for _, id := range ids {
		selected[id] = true
	}
	found := map[string]bool{}
	remaining := make([]any, 0, len(pool))
	for _, row := range pool {
		id := text(object(row), "id")
		if !validID(id) {
			return sum, errors.New("pool_identity_invalid")
		}
		if selected[id] {
			found[id] = true
		} else {
			remaining = append(remaining, row)
		}
	}
	if len(found) != len(ids) {
		return sum, errors.New("account_not_found")
	}
	keptIndex := make([]any, 0, len(indexRows))
	for _, row := range indexRows {
		if !selected[text(object(row), "id")] {
			keptIndex = append(keptIndex, row)
		}
	}
	index["accounts"] = keptIndex
	if selected[text(index, "current_account_id")] {
		index["current_account_id"] = nil
	}
	manifest["accounts"] = remaining
	if raw, ok := manifest["accountModelRules"]; ok {
		rules, ok := raw.([]any)
		if !ok {
			return sum, errors.New("pool_model_rules_invalid")
		}
		next := make([]any, 0, len(rules))
		for _, row := range rules {
			rule := object(row)
			if rule == nil {
				return sum, errors.New("pool_model_rules_invalid")
			}
			if !selected[text(rule, "accountId")] {
				next = append(next, row)
			}
		}
		manifest["accountModelRules"] = next
	}
	disabledKeys := map[string]bool{}
	for _, row := range keys {
		key := object(row)
		scoped, ok := key["accountIds"].([]any)
		if !ok && key["accountIds"] != nil {
			return sum, errors.New("pool_scope_invalid")
		}
		next, err := withoutIDs(scoped, selected)
		if err != nil {
			return sum, err
		}
		if scoped != nil {
			key["accountIds"] = next
		}
		// Empty scopes may mean unrestricted. Disable a formerly restricted key.
		if len(scoped) > 0 && len(next) == 0 {
			key["enabled"] = false
			key["disabledByAccountDeletion"] = true
			disabledKeys[text(key, "key")] = true
		}
	}
	for key, value := range scopes {
		scoped, ok := value.([]any)
		if !ok {
			return sum, errors.New("pool_scope_invalid")
		}
		next, err := withoutIDs(scoped, selected)
		if err != nil {
			return sum, err
		}
		scopes[key] = next
		if len(scoped) > 0 && len(next) == 0 {
			disabledKeys[key] = true
		}
	}
	if raw, ok := cfg["api-keys"]; ok {
		apiKeys, ok := raw.([]any)
		if !ok {
			return sum, errors.New("pool_key_invalid")
		}
		next, err := withoutIDs(apiKeys, disabledKeys)
		if err != nil {
			return sum, err
		}
		cfg["api-keys"] = next
	}
	writes := map[string]any{"manifest.json": manifest, "config.json": cfg, filepath.Join("cockpit-data", "codex_accounts.json"): index}
	paths := []string{}
	for path := range writes {
		paths = append(paths, path)
	}
	for _, id := range ids {
		paths = append(paths, filepath.Join("auths", id+".json"), filepath.Join("cockpit-data", "codex_accounts", id+".json"))
	}
	sort.Strings(paths)
	sum.Deleted = len(ids)
	sum.Total = len(remaining)
	tx := transaction{Summary: sum}
	for _, path := range paths {
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil && !os.IsNotExist(err) {
			return Summary{}, errors.New("pool_backup_failed")
		}
		tx.Entries = append(tx.Entries, backupEntry{Path: path, Present: err == nil, Data: data})
	}
	if err := maintenance.AtomicJSON(journal, tx); err != nil {
		return Summary{}, errors.New("pool_backup_failed")
	}
	rollback := func() (Summary, error) {
		if restore(root, tx) != nil {
			return Summary{}, errors.New("pool_rollback_failed")
		}
		return Summary{}, errors.New("pool_delete_failed")
	}
	for _, path := range paths {
		if value, ok := writes[path]; ok {
			if maintenance.AtomicJSON(filepath.Join(root, path), value) != nil {
				return rollback()
			}
		} else if err := os.Remove(filepath.Join(root, path)); err != nil && !os.IsNotExist(err) {
			return rollback()
		}
	}
	tx.Committed = true
	if maintenance.AtomicJSON(journal, tx) != nil {
		return rollback()
	}
	return sum, nil
}
