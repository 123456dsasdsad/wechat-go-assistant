package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/accountupload"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/maintenance"
	"net/http"
	"os/exec"
	"strings"
	"time"
	"unicode"
)

func accountUploadCommand(input string) string {
	compact := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, input)
	compact = strings.Trim(compact, "/。？?!！")
	compact = strings.NewReplacer("帐号", "账号", "账户", "账号").Replace(compact)
	switch compact {
	case "上传账号", "账号上传":
		return "upload"
	case "上传账号状态", "账号上传状态":
		return "status"
	default:
		return ""
	}
}

func accountMaintenanceLease(ctx context.Context, cfg config, key string, action, lease string) (string, error) {
	body, _ := json.Marshal(map[string]string{"Action": action, "Lease": lease})
	req, e := http.NewRequestWithContext(ctx, "POST", "http://"+cfg.Listen+"/maintenance/lease", bytes.NewReader(body))
	if e != nil {
		return "", errors.New("account_lease_unavailable")
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, e := client.Do(req)
	if e != nil {
		return "", errors.New("account_lease_unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode == 409 && action == "acquire" {
		return "", nil
	}
	if response.StatusCode != 200 {
		return "", errors.New("account_lease_unavailable")
	}
	var result struct {
		Lease    string `json:"lease"`
		Released bool   `json:"released"`
	}
	if json.NewDecoder(response.Body).Decode(&result) != nil {
		return "", errors.New("account_lease_invalid")
	}
	if action == "release" && !result.Released {
		return "", errors.New("account_lease_release_failed")
	}
	return result.Lease, nil
}
func accountReload(ctx context.Context, cfg config) error {
	runCtx, cancel := context.WithTimeout(ctx, 8*time.Minute)
	defer cancel()
	command := exec.CommandContext(runCtx, "powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", cfg.AccountReloadRunner, "-Root", cfg.CockpitRoot)
	if command.Run() != nil {
		return errors.New("account_pool_reload_failed")
	}
	return nil
}
func activateAccounts(ctx context.Context, cfg config, key string, store *accountupload.Store, reports *maintenance.Store) {
	for pause(ctx, 15*time.Second) {
		ids := store.Pending()
		if len(ids) == 0 {
			continue
		}
		for _, id := range ids {
			receipt, e := store.Receipt(id)
			if e != nil {
				continue
			}
			if receipt.Status != "active" {
				unlock, ok, e := accountupload.MutationLock(cfg.CockpitRoot)
				if e != nil || !ok {
					break
				}
				lease, e := accountMaintenanceLease(ctx, cfg, key, "acquire", "")
				if e != nil || lease == "" {
					unlock()
					break
				}
				e = store.Process(id, cfg.CockpitRoot, func() error { return accountReload(ctx, cfg) })
				releaseCtx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
				_, releaseErr := accountMaintenanceLease(releaseCtx, cfg, key, "release", lease)
				cancel()
				unlock()
				if releaseErr != nil {
					fmt.Println(`{"type":"account_import_retry","category":"lease_release_failed"}`)
				}
				if e != nil {
					fmt.Printf("{\"type\":\"account_import_retry\",\"id\":%q,\"category\":\"import_or_reload_pending\"}\n", id)
					continue
				}
				receipt, e = store.Receipt(id)
				if e != nil {
					continue
				}
			}
			if receipt.Status == "active" {
				report := maintenance.NewReport("cloud", "status", time.Now().In(maintenance.Beijing).Format("2006-01-02"), receipt.Text())
				if reports.Put(report) == nil {
					store.MarkNotified(id)
				}
				fmt.Printf("{\"type\":\"account_import_active\",\"id\":%q,\"added\":%d,\"updated\":%d,\"total\":%d}\n", id, receipt.Added, receipt.Updated, receipt.Total)
			}
		}
	}
}
