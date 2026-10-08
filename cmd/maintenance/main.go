package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/maintenance"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type config struct {
	Host           string                `json:"host"`
	Root           string                `json:"root"`
	RelayURL       string                `json:"relay_url"`
	KeyFile        string                `json:"key_file"`
	GatewayRoot    string                `json:"gateway_root"`
	GatewayURL     string                `json:"gateway_url"`
	GatewayKeyFile string                `json:"gateway_key_file"`
	UsageLog       string                `json:"usage_log"`
	Programs       []maintenance.Program `json:"programs"`
}

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run() error {
	path := flag.String("config", "", "private config")
	action := flag.String("action", "", "usage, accounts, models, updates, publish, retry-publish, status")
	dayFlag := flag.String("day", "", "Beijing date; usage defaults to yesterday")
	dry := flag.Bool("dry-run", false, "no account mutations or report publication")
	ifChanged := flag.Bool("if-changed", false, "skip unchanged campus account follow-ups")
	input := flag.String("input", "", "JSON update results for publish")
	flag.Parse()
	raw, e := os.ReadFile(*path)
	var cfg config
	if e != nil || json.Unmarshal(raw, &cfg) != nil || (cfg.Host != "cloud" && cfg.Host != "campus") {
		return errors.New("maintenance_config_invalid")
	}
	keyRaw, e := os.ReadFile(cfg.KeyFile)
	if e != nil {
		return errors.New("private_key_unavailable")
	}
	key := strings.TrimSpace(string(keyRaw))
	if len(key) < 32 {
		return errors.New("private_key_invalid")
	}
	if e = os.MkdirAll(cfg.Root, 0700); e != nil {
		return e
	}
	// OS tasks use IgnoreNew/flock as well; each action has a separate mutex.
	lock := filepath.Join(cfg.Root, *action+".lock")
	if info, err := os.Stat(lock); err == nil && time.Since(info.ModTime()) > 30*time.Minute {
		os.Remove(lock)
	}
	f, e := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return errors.New("maintenance_already_running")
	}
	f.Close()
	defer os.Remove(lock)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	client := &http.Client{Timeout: 10 * time.Minute}
	day := *dayFlag
	if day == "" {
		now := time.Now().In(maintenance.Beijing)
		if *action == "usage" {
			now = now.AddDate(0, 0, -1)
		}
		day = now.Format("2006-01-02")
	}
	if _, e = time.Parse("2006-01-02", day); e != nil {
		return errors.New("invalid_day")
	}
	var text, kind string
	var detail any
	switch *action {
	case "models":
		if cfg.Host != "cloud" {
			return errors.New("model_catalog_requires_gateway")
		}
		sum, e := maintenance.SyncModelCatalog(ctx, &http.Client{Timeout: 12 * time.Second}, cfg.GatewayRoot, *dry)
		if e != nil {
			return e
		}
		if e = maintenance.AtomicJSON(filepath.Join(cfg.Root, "model-catalog-latest.json"), sum); e != nil {
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(sum)
	case "usage":
		kind = "usage"
		if cfg.Host == "cloud" {
			f, e := os.Open(cfg.UsageLog)
			if e != nil {
				return errors.New("usage_log_unavailable")
			}
			sum, e := maintenance.Summarize(f, day)
			f.Close()
			if e != nil {
				return errors.New("usage_parse_failed")
			}
			text = sum.Text()
			detail = sum
		} else {
			text = "【校园用量核对｜" + day + "】\n校园任务通过阿里云共享账号入口调用模型；token 和美元价值统一计入阿里云用量日报，不重复相加。\n校园本机的定时检查不调用 AI，不消耗推理 token。"
		}
	case "accounts":
		kind = "accounts"
		if cfg.Host == "cloud" {
			sum, e := maintenance.CheckAccounts(ctx, &http.Client{Timeout: 30 * time.Second}, cfg.GatewayRoot, *dry)
			if e != nil {
				return e
			}
			detail = sum
			text = sum.Text(day)
		} else {
			gatewayKey, _ := os.ReadFile(cfg.GatewayKeyFile)
			sum := maintenance.CheckCampusAccounts(ctx, &http.Client{Timeout: 15 * time.Second}, cfg.RelayURL, key, cfg.GatewayURL, strings.TrimSpace(string(gatewayKey)), day, time.Now())
			if *ifChanged {
				previousRaw, _ := os.ReadFile(filepath.Join(cfg.Root, "accounts-"+day+".json"))
				var previous maintenance.CampusAccounts
				if json.Unmarshal(previousRaw, &previous) == nil && sum.SameResult(previous) {
					return json.NewEncoder(os.Stdout).Encode(map[string]bool{"skipped_unchanged": true})
				}
			}
			detail = sum
			text = sum.Text()
		}
	case "updates":
		fetcher := maintenance.Fetcher{Client: client}
		if cfg.Host == "campus" {
			fetcher.BridgeURL = cfg.RelayURL
			fetcher.Key = key
		}
		updates, e := fetcher.Stage(ctx, cfg.Programs, filepath.Join(cfg.Root, "staging"))
		if e != nil {
			return e
		}
		if e = maintenance.AtomicJSON(filepath.Join(cfg.Root, "updates-staged.json"), updates); e != nil {
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(updates)
	case "publish":
		kind = "updates"
		b, e := os.ReadFile(*input)
		var updates []maintenance.Update
		if e != nil || json.Unmarshal(b, &updates) != nil {
			return errors.New("update_result_invalid")
		}
		var out strings.Builder
		hostName := "阿里云"
		if cfg.Host == "campus" {
			hostName = "校园"
		}
		fmt.Fprintf(&out, "【软件更新｜%s｜%s】\n检查时间：%s（北京时间）", day, hostName, time.Now().In(maintenance.Beijing).Format("2006-01-02 15:04:05"))
		for _, u := range updates {
			fmt.Fprintf(&out, "\n\n%s\n版本：%s → %s\n结果：%s", u.Name, u.Version, u.Latest, u.State)
		}
		out.WriteString("\n仅更新已纳管软件的稳定版；先校验与备份，任务忙时延后，不自动重启服务器。")
		text = out.String()
		detail = updates
	case "retry-publish":
		entries, _ := os.ReadDir(filepath.Join(cfg.Root, "outbox"))
		count := 0
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			p := filepath.Join(cfg.Root, "outbox", entry.Name())
			b, e := os.ReadFile(p)
			var r maintenance.Report
			if e == nil && json.Unmarshal(b, &r) == nil && post(ctx, client, cfg.RelayURL, key, r) == nil {
				os.Remove(p)
				count++
			}
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]int{"submitted": count})
	case "status":
		req, _ := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(cfg.RelayURL, "/")+"/maintenance/reports", nil)
		req.Header.Set("Authorization", "Bearer "+key)
		resp, e := client.Do(req)
		if e != nil {
			return errors.New("report_service_unavailable")
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return errors.New("report_service_rejected")
		}
		_, e = io.Copy(os.Stdout, io.LimitReader(resp.Body, 65536))
		return e
	default:
		return errors.New("unknown_action")
	}
	if detail != nil {
		if e = maintenance.AtomicJSON(filepath.Join(cfg.Root, kind+"-"+day+".json"), detail); e != nil {
			return e
		}
	}
	report := maintenance.NewReport(cfg.Host, kind, day, text)
	submitted := false
	if !*dry {
		pending := filepath.Join(cfg.Root, "outbox", report.ID+".json")
		if e = maintenance.AtomicJSON(pending, report); e != nil {
			return e
		}
		if post(ctx, client, cfg.RelayURL, key, report) == nil {
			submitted = true
			os.Remove(pending)
		}
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"report": report, "submitted_to_relay": submitted, "dry_run": *dry})
}
func post(ctx context.Context, c *http.Client, base, key string, r maintenance.Report) error {
	b, _ := json.Marshal(r)
	req, e := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(base, "/")+"/maintenance/report", bytes.NewReader(b))
	if e != nil {
		return e
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, e := c.Do(req)
	if e != nil {
		return errors.New("report_submit_failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return errors.New("report_submit_rejected")
	}
	return nil
}
