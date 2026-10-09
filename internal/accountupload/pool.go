package accountupload

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/maintenance"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Summary struct {
	Added   int `json:"added"`
	Updated int `json:"updated"`
	Deleted int `json:"deleted,omitempty"`
	Total   int `json:"total"`
}

func readMap(path string) (map[string]any, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, errors.New("pool_record_unavailable")
	}
	var m map[string]any
	if decode(b, &m) != nil || m == nil {
		return nil, errors.New("pool_record_invalid")
	}
	return m, nil
}
func validID(id string) bool {
	if len(id) < 1 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
func batchID(id string) bool {
	if len(id) != 24 {
		return false
	}
	_, e := hex.DecodeString(id)
	return e == nil
}
func array(m map[string]any, key string) ([]any, error) {
	v, ok := m[key].([]any)
	if !ok {
		return nil, errors.New("pool_index_invalid")
	}
	return v, nil
}
func uuid() (string, error) {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	b[6] = b[6]&0xf | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}
func appendUnique(v any, id string) ([]any, error) {
	list, ok := v.([]any)
	if !ok && v != nil {
		return nil, errors.New("pool_scope_invalid")
	}
	for _, x := range list {
		if x == id {
			return list, nil
		}
	}
	return append(list, id), nil
}

// Updated credentials enter the pool after their account-specific model catalog
// has been checked by the reload runner. Plan labels never grant or deny models.
func pendingCatalogRules(id string, auth, manifest map[string]any) error {
	rows, ok := manifest["modelIds"].([]any)
	if !ok {
		return errors.New("pool_model_list_invalid")
	}
	var models []string
	for _, row := range rows {
		m, ok := row.(string)
		if !ok || m == "" {
			return errors.New("pool_model_list_invalid")
		}
		models = append(models, m)
	}
	delete(auth, "catalog_checked_at")
	return maintenance.CatalogExclusions(auth, manifest, id, models)
}
func upsert(rows []any, id string, values map[string]any) []any {
	for _, row := range rows {
		m := object(row)
		if text(m, "id") == id {
			for k, v := range values {
				m[k] = v
			}
			return rows
		}
	}
	return append(rows, values)
}

type backupEntry struct {
	Path    string          `json:"path"`
	Present bool            `json:"present"`
	Data    json.RawMessage `json:"data,omitempty"`
}
type transaction struct {
	Committed bool          `json:"committed"`
	Summary   Summary       `json:"summary"`
	Entries   []backupEntry `json:"entries"`
}

func restore(root string, t transaction) error {
	for _, item := range t.Entries {
		if !filepath.IsLocal(item.Path) {
			return errors.New("invalid_backup_path")
		}
		p := filepath.Join(root, item.Path)
		if item.Present {
			if e := maintenance.AtomicJSON(p, item.Data); e != nil {
				return errors.New("pool_rollback_failed")
			}
		} else {
			if e := os.Remove(p); e != nil && !os.IsNotExist(e) {
				return errors.New("pool_rollback_failed")
			}
		}
	}
	return nil
}

// Merge preserves account IDs and policies and journals the multi-file write.
// A committed batch is never replayed, including after a relay crash.
func Merge(root, id string, accounts []Account) (Summary, error) {
	var sum Summary
	if !batchID(id) || len(accounts) == 0 || len(accounts) > MaxAccounts {
		return sum, errors.New("invalid_batch")
	}
	for _, a := range accounts {
		if !validAccount(a) {
			return sum, errors.New("invalid_account")
		}
	}
	backupPath := filepath.Join(root, "imports-backup", id, "transaction.json")
	if b, e := os.ReadFile(backupPath); e == nil {
		var old transaction
		if decode(b, &old) != nil {
			return sum, errors.New("pool_backup_invalid")
		}
		if old.Committed {
			return old.Summary, nil
		}
		if e = restore(root, old); e != nil {
			return sum, e
		}
	} else if !os.IsNotExist(e) {
		return sum, errors.New("pool_backup_unavailable")
	}
	indexPath := filepath.Join(root, "cockpit-data", "codex_accounts.json")
	index, e := readMap(indexPath)
	if e != nil {
		return sum, e
	}
	indexRows, e := array(index, "accounts")
	if e != nil {
		return sum, e
	}
	manifest, e := readMap(filepath.Join(root, "manifest.json"))
	if e != nil {
		return sum, e
	}
	pool, e := array(manifest, "accounts")
	if e != nil {
		return sum, e
	}
	keys, e := array(manifest, "apiKeys")
	if e != nil {
		return sum, e
	}
	config, e := readMap(filepath.Join(root, "config.json"))
	if e != nil {
		return sum, e
	}
	scopes := object(config["api-key-account-ids"])
	if scopes == nil {
		return sum, errors.New("pool_scope_invalid")
	}
	existing := map[string]map[string]any{}
	entries, e := os.ReadDir(filepath.Join(root, "cockpit-data", "codex_accounts"))
	if os.IsNotExist(e) {
		e = nil
	}
	if e != nil {
		return sum, errors.New("pool_details_unavailable")
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		record, e := readMap(filepath.Join(root, "cockpit-data", "codex_accounts", entry.Name()))
		if e != nil {
			return sum, e
		}
		rid := text(record, "id")
		if !validID(rid) || rid+".json" != entry.Name() {
			return sum, errors.New("pool_identity_invalid")
		}
		existing[rid] = record
	}
	writes := map[string]any{}
	now := time.Now().Unix()
	seen := map[string]bool{}
	for _, a := range accounts {
		if seen[identity(a)] {
			return sum, errors.New("duplicate_import_identity")
		}
		seen[identity(a)] = true
		rid := ""
		var native map[string]any
		for candidate, v := range existing {
			if text(v, "account_id") != a.AccountID {
				continue
			}
			user := text(v, "user_id")
			matches := a.UserID != "" && user != "" && user == a.UserID || strings.EqualFold(text(v, "email"), a.Email)
			if !matches {
				continue
			}
			if a.UserID != "" && user != "" && user != a.UserID {
				return sum, errors.New("pool_identity_conflict")
			}
			if rid != "" && rid != candidate {
				return sum, errors.New("pool_identity_ambiguous")
			}
			rid, native = candidate, v
		}
		added := rid == ""
		if added {
			rid, e = uuid()
			if e != nil {
				return sum, errors.New("identity_generation_failed")
			}
			native = map[string]any{"id": rid, "auth_mode": "oauth", "api_provider_mode": "openai_builtin", "tokens": map[string]any{}, "token_generation": json.Number("0"), "token_source_mode": "managed", "quota": nil, "quota_error": nil, "tags": []any{"cloud-wechat"}, "created_at": now, "last_used": 0}
			sum.Added++
		} else {
			sum.Updated++
		}
		tokens := object(native["tokens"])
		if tokens == nil {
			return sum, errors.New("pool_tokens_invalid")
		}
		tokens["access_token"], tokens["id_token"] = a.AccessToken, a.IDToken
		if a.RefreshToken != "" {
			tokens["refresh_token"] = a.RefreshToken
		}
		native["email"], native["account_id"], native["token_updated_at"], native["token_source_mode"], native["requires_reauth"] = strings.ToLower(a.Email), a.AccountID, now, "managed", false
		if a.UserID != "" {
			native["user_id"] = a.UserID
		}
		if a.PlanType != "" {
			native["plan_type"] = a.PlanType
		}
		generation, _ := native["token_generation"].(json.Number)
		g, _ := generation.Int64()
		native["token_generation"] = g + 1
		delete(native, "maintenance_quarantine_reason")
		existing[rid] = native
		authPath := filepath.Join("auths", rid+".json")
		auth := map[string]any{}
		if !added {
			auth, e = readMap(filepath.Join(root, authPath))
			if e != nil {
				return sum, e
			}
		}
		auth["type"], auth["auth_mode"], auth["openai_auth_mode"] = "codex", "oauth", "oauth"
		auth["email"], auth["account_id"], auth["plan_type"] = native["email"], a.AccountID, native["plan_type"]
		auth["access_token"], auth["id_token"], auth["refresh_token"], auth["refresh_owner"], auth["last_refresh"], auth["disabled"] = a.AccessToken, a.IDToken, "", "cockpit_token_authority", fmt.Sprint(now), false
		if added {
			auth["websockets"] = false
			auth["excluded_models"] = []any{}
		}
		if e = pendingCatalogRules(rid, auth, manifest); e != nil {
			return sum, e
		}
		writes[filepath.Join("cockpit-data", "codex_accounts", rid+".json")] = native
		writes[authPath] = auth
		indexRows = upsert(indexRows, rid, map[string]any{"id": rid, "email": native["email"], "plan_type": native["plan_type"], "created_at": native["created_at"], "last_used": native["last_used"]})
		pool = upsert(pool, rid, map[string]any{"id": rid, "email": native["email"], "authId": rid + ".json", "authKind": "oauth", "planType": native["plan_type"], "chatgptAccountId": a.AccountID, "accessTokenOnly": text(tokens, "refresh_token") == ""})
		if added {
			found := false
			for _, row := range keys {
				key := object(row)
				if text(key, "id") != "campus-worker" {
					continue
				}
				found = true
				key["accountIds"], e = appendUnique(key["accountIds"], rid)
				if e != nil {
					return sum, e
				}
				value := text(key, "key")
				if value == "" {
					return sum, errors.New("pool_key_invalid")
				}
				if owned, _ := key["disabledByAccountDeletion"].(bool); owned {
					key["enabled"] = true
					delete(key, "disabledByAccountDeletion")
					config["api-keys"], e = appendUnique(config["api-keys"], value)
					if e != nil {
						return sum, e
					}
				}
				scopes[value], e = appendUnique(scopes[value], rid)
				if e != nil {
					return sum, e
				}
			}
			if !found {
				return sum, errors.New("campus_pool_key_missing")
			}
		}
	}
	index["accounts"] = indexRows
	manifest["accounts"] = pool
	sum.Total = len(existing)
	writes[filepath.Join("cockpit-data", "codex_accounts.json")] = index
	writes["manifest.json"] = manifest
	writes["config.json"] = config
	t := transaction{Summary: sum}
	// Save originals before changing any live file; backups inherit the private pool ACL.
	for relative := range writes {
		p := filepath.Join(root, relative)
		data, e := os.ReadFile(p)
		if e != nil && !os.IsNotExist(e) {
			return Summary{}, errors.New("pool_backup_failed")
		}
		t.Entries = append(t.Entries, backupEntry{Path: relative, Present: e == nil, Data: data})
	}
	if e = maintenance.AtomicJSON(backupPath, t); e != nil {
		return Summary{}, errors.New("pool_backup_failed")
	}
	for relative, value := range writes {
		if e = maintenance.AtomicJSON(filepath.Join(root, relative), value); e != nil {
			if restore(root, t) != nil {
				return Summary{}, errors.New("pool_rollback_failed")
			}
			return Summary{}, errors.New("pool_write_failed")
		}
	}
	t.Committed = true
	if e = maintenance.AtomicJSON(backupPath, t); e != nil {
		if restore(root, t) != nil {
			return Summary{}, errors.New("pool_rollback_failed")
		}
		return Summary{}, errors.New("pool_commit_failed")
	}
	return sum, nil
}
