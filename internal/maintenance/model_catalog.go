package maintenance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CatalogExclusions changes only exclusions owned by the account catalog.
// Explicit operator rules and all account/key scopes remain intact.
func CatalogExclusions(auth, manifest map[string]any, id string, models []string) error {
	owned, e := modelStrings(auth["catalog_excluded_models"])
	if e != nil {
		return e
	}
	existing, e := modelStrings(auth["excluded_models"])
	if e != nil {
		return e
	}
	merge := func(old []string) []string {
		var out []string
		for _, m := range old {
			if !containsModel(owned, m) && !containsModel(out, m) {
				out = append(out, m)
			}
		}
		for _, m := range models {
			if !containsModel(out, m) {
				out = append(out, m)
			}
		}
		if out == nil {
			out = []string{}
		}
		return out
	}
	rules, ok := manifest["accountModelRules"].([]any)
	if !ok && manifest["accountModelRules"] != nil {
		return errors.New("pool_model_rules_invalid")
	}
	var match map[string]any
	for _, row := range rules {
		rule, ok := row.(map[string]any)
		if !ok {
			return errors.New("pool_model_rules_invalid")
		}
		if str(rule, "accountId") == id {
			if match != nil {
				return errors.New("pool_model_rules_ambiguous")
			}
			match = rule
		}
	}
	if match == nil {
		match = map[string]any{"accountId": id}
		rules = append(rules, match)
	}
	ruleModels, e := modelStrings(match["excludedModels"])
	if e != nil {
		return e
	}
	var nextOwned []string
	for _, model := range models {
		explicit := !containsModel(owned, model) && (containsModel(existing, model) || containsModel(ruleModels, model))
		if !explicit && !containsModel(nextOwned, model) {
			nextOwned = append(nextOwned, model)
		}
	}
	match["excludedModels"] = merge(ruleModels)
	manifest["accountModelRules"] = rules
	auth["excluded_models"] = merge(existing)
	auth["catalog_excluded_models"] = append([]string{}, nextOwned...)
	return nil
}

func modelStrings(v any) ([]string, error) {
	if v == nil {
		return nil, nil
	}
	if rows, ok := v.([]string); ok {
		return rows, nil
	}
	rows, ok := v.([]any)
	if !ok {
		return nil, errors.New("pool_model_list_invalid")
	}
	var out []string
	for _, row := range rows {
		m, ok := row.(string)
		if !ok || strings.TrimSpace(m) == "" {
			return nil, errors.New("pool_model_list_invalid")
		}
		if !containsModel(out, m) {
			out = append(out, m)
		}
	}
	return out, nil
}
func containsModel(rows []string, value string) bool {
	for _, row := range rows {
		if row == value {
			return true
		}
	}
	return false
}

type ModelCatalogAccount struct {
	ID        string   `json:"id"`
	State     string   `json:"state"`
	Available []string `json:"available,omitempty"`
	Excluded  []string `json:"excluded,omitempty"`
}
type ModelCatalogSummary struct {
	Accounts  []ModelCatalogAccount `json:"accounts"`
	Reload    bool                  `json:"reload_required"`
	Pending   int                   `json:"pending"`
	Suspended int                   `json:"authorization_pending,omitempty"`
}

// SyncModelCatalog uses the account-specific upstream catalog, never a plan
// allowlist or an inference request. Network/quota errors retain verified rules.
// Callers hold the shared mutation lock and maintenance lease.
func SyncModelCatalog(ctx context.Context, client HTTPDoer, root string, dry bool) (ModelCatalogSummary, error) {
	ctx, cancelRun := context.WithTimeout(ctx, 5*time.Minute)
	defer cancelRun()
	var sum ModelCatalogSummary
	manifestPath := filepath.Join(root, "manifest.json")
	manifest, e := readMap(manifestPath)
	if e != nil {
		return sum, errors.New("pool_manifest_unavailable")
	}
	manifestBefore, _ := json.Marshal(manifest)
	models, e := modelStrings(manifest["modelIds"])
	if e != nil || len(models) == 0 {
		return sum, errors.New("pool_models_invalid")
	}
	rows, ok := manifest["accounts"].([]any)
	if !ok {
		return sum, errors.New("pool_accounts_invalid")
	}
	writes := map[string]any{}
	for _, row := range rows {
		a, ok := row.(map[string]any)
		id := str(a, "id")
		if !ok || id == "" || !filepath.IsLocal(id) || filepath.Base(id) != id || strings.ContainsAny(id, "/\\:") {
			return sum, errors.New("pool_identity_invalid")
		}
		path := filepath.Join(root, "auths", id+".json")
		auth, e := readMap(path)
		if e != nil {
			return sum, errors.New("pool_auth_unavailable")
		}
		check := ModelCatalogAccount{ID: id}
		if disabled, _ := auth["disabled"].(bool); disabled {
			check.State = "disabled"
			sum.Accounts = append(sum.Accounts, check)
			continue
		}
		checked, _ := time.Parse(time.RFC3339, str(auth, "catalog_checked_at"))
		cachedModels, _ := modelStrings(auth["catalog_checked_models"])
		authorizationPending, _ := auth["catalog_authorization_pending"].(bool)
		if !authorizationPending && !checked.IsZero() && time.Since(checked) >= 0 && time.Since(checked) < 30*time.Minute && len(cachedModels) == len(models) {
			same := true
			for _, model := range models {
				if !containsModel(cachedModels, model) {
					same = false
				}
			}
			if same {
				check.State = "verified_cached"
				check.Available, _ = modelStrings(auth["catalog_models"])
				check.Excluded, _ = modelStrings(auth["catalog_excluded_models"])
				sum.Accounts = append(sum.Accounts, check)
				continue
			}
		}
		if ctx.Err() != nil {
			check.State = "unverified_preserved"
			sum.Pending++
			sum.Accounts = append(sum.Accounts, check)
			continue
		}
		probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		status, body, err := request(probeCtx, client, "GET", "https://chatgpt.com/backend-api/codex/models?client_version=0.160.1", nil, map[string]string{"Authorization": "Bearer " + str(auth, "access_token"), "ChatGPT-Account-Id": str(auth, "account_id"), "originator": "codex_cli_rs", "User-Agent": "codex_cli_rs/0.160.1"})
		cancel()
		// Unauthorized credentials cannot serve any inference model now. Suspend
		// only catalog-owned routing rules, retaining credentials for the account
		// refresh runner or a later upload. This known exclusion must not block
		// activating healthy accounts as an unresolved catalog check would.
		if err == nil && status == http.StatusUnauthorized {
			before, _ := json.Marshal(auth["excluded_models"])
			if e = CatalogExclusions(auth, manifest, id, models); e != nil {
				return sum, e
			}
			after, _ := json.Marshal(auth["excluded_models"])
			if string(before) != string(after) {
				sum.Reload = true
			}
			auth["catalog_authorization_pending"] = true
			delete(auth, "catalog_checked_at")
			writes[path] = auth
			check.State = "authorization_pending"
			check.Excluded = append([]string{}, models...)
			sum.Suspended++
			sum.Accounts = append(sum.Accounts, check)
			continue
		}
		var catalog struct {
			Models []struct {
				Slug      string `json:"slug"`
				Supported *bool  `json:"supported_in_api"`
			} `json:"models"`
		}
		if err != nil || status != 200 || json.Unmarshal(body, &catalog) != nil || len(catalog.Models) == 0 {
			check.State = "unverified_preserved"
			sum.Pending++
			sum.Accounts = append(sum.Accounts, check)
			continue
		}
		var available []string
		for _, model := range catalog.Models {
			if model.Slug != "" && model.Supported != nil && *model.Supported {
				available = append(available, model.Slug)
			}
		}
		if len(available) == 0 {
			check.State = "unverified_preserved"
			sum.Pending++
			sum.Accounts = append(sum.Accounts, check)
			continue
		}
		check.State = "verified"
		for _, model := range models {
			if containsModel(available, model) {
				check.Available = append(check.Available, model)
			} else {
				check.Excluded = append(check.Excluded, model)
			}
		}
		before, _ := json.Marshal(auth["excluded_models"])
		if e = CatalogExclusions(auth, manifest, id, check.Excluded); e != nil {
			return sum, e
		}
		after, _ := json.Marshal(auth["excluded_models"])
		if string(before) != string(after) {
			sum.Reload = true
		}
		auth["catalog_checked_at"] = time.Now().UTC().Format(time.RFC3339)
		delete(auth, "catalog_authorization_pending")
		auth["catalog_models"] = check.Available
		auth["catalog_checked_models"] = append([]string{}, models...)
		writes[path] = auth
		sum.Accounts = append(sum.Accounts, check)
	}
	manifestAfter, _ := json.Marshal(manifest)
	if string(manifestBefore) != string(manifestAfter) {
		sum.Reload = true
	}
	if dry {
		return sum, nil
	}
	writes[manifestPath] = manifest
	// Save recoverable originals before publishing either representation.
	backup := filepath.Join(root, "catalog-backups", fmt.Sprint(time.Now().UnixNano()))
	originals := map[string][]byte{}
	for path := range writes {
		b, e := os.ReadFile(path)
		if e != nil {
			return sum, errors.New("catalog_backup_failed")
		}
		originals[path] = b
		relative, _ := filepath.Rel(root, path)
		if e = os.MkdirAll(filepath.Dir(filepath.Join(backup, relative)), 0700); e != nil {
			return sum, errors.New("catalog_backup_failed")
		}
		if e = os.WriteFile(filepath.Join(backup, relative), b, 0600); e != nil {
			return sum, errors.New("catalog_backup_failed")
		}
	}
	for path, value := range writes {
		if e = AtomicJSON(path, value); e != nil {
			for originalPath, b := range originals {
				if os.WriteFile(originalPath, b, 0600) != nil {
					return sum, errors.New("catalog_rollback_failed")
				}
			}
			return sum, errors.New("catalog_write_failed")
		}
	}
	return sum, nil
}
