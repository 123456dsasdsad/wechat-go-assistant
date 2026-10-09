// queuewatch reads one experiment scheduler's state; it never changes a job.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/watches"
)

type config struct {
	WorkerConfig string `json:"worker_config"`
	Root         string `json:"root"`
	Conversation string `json:"conversation"`
	Thread       string `json:"thread"`
	Title        string `json:"title"`
	Context      string `json:"context"`
}
type queue struct {
	Jobs []struct {
		ID      string `json:"id"`
		Method  string `json:"method"`
		Dataset string `json:"dataset"`
		Seed    int    `json:"seed"`
		Status  string `json:"status"`
		GPU     int    `json:"gpu"`
	} `json:"jobs"`
}
type metrics struct {
	Epoch     int      `json:"epoch"`
	Microstep int      `json:"microstep"`
	Updates   int      `json:"optimizer_update"`
	Loss      *float64 `json:"loss"`
}

func snapshot(c config, now time.Time) (watches.Update, error) {
	p := filepath.Join(c.Root, "status", "experiment_queue.json")
	b, e := os.ReadFile(p)
	if e != nil {
		return watches.Update{}, e
	}
	var q queue
	if len(b) > 2<<20 || json.Unmarshal(b, &q) != nil || len(q.Jobs) == 0 || len(q.Jobs) > 1000 {
		return watches.Update{}, errors.New("invalid_queue")
	}
	counts := map[string]int{}
	var lines []string
	state := "running"
	for _, j := range q.Jobs {
		if j.Status != "pending" && j.Status != "running" && j.Status != "complete" && j.Status != "failed" {
			return watches.Update{}, errors.New("unknown_job_status")
		}
		counts[j.Status]++
		if j.Status != "running" {
			continue
		}
		// IDs come from a separate scheduler, so require a single safe component.
		if j.ID == "" || filepath.Base(j.ID) != j.ID || strings.ContainsAny(j.ID, "\\/\x00") || j.ID == ".." {
			return watches.Update{}, errors.New("invalid_job_id")
		}
		root := filepath.Join(c.Root, "results", j.ID)
		var latest metrics
		found := false
		var modified time.Time
		visited := 0
		_ = filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
			if e != nil {
				return filepath.SkipDir
			}
			visited++
			if visited > 5000 {
				return filepath.SkipAll
			}
			if d.IsDir() || d.Name() != "live_progress.json" {
				return nil
			}
			stat, e := d.Info()
			if e != nil || stat.Size() > 16384 || !stat.ModTime().After(modified) {
				return nil
			}
			data, e := os.ReadFile(p)
			var m metrics
			if e == nil && json.Unmarshal(data, &m) == nil {
				latest = m
				found = true
				modified = stat.ModTime()
			}
			return nil
		})
		line := fmt.Sprintf("%s / %s / seed %d / GPU %d", j.Dataset, j.Method, j.Seed, j.GPU)
		if found {
			line += fmt.Sprintf("\n轮次 %d · microstep %d · 优化步 %d", latest.Epoch, latest.Microstep, latest.Updates)
			if latest.Loss != nil {
				line += fmt.Sprintf(" · loss %.5g", *latest.Loss)
			}
			line += "\n训练记录更新：" + modified.In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("01-02 15:04:05")
		} else {
			line += "\n等待首份训练记录。"
		}
		lines = append(lines, line)
	}
	if counts["running"] == 0 && counts["pending"] == 0 {
		state = "done"
		if counts["failed"] > 0 {
			state = "failed"
		}
	}
	summary := fmt.Sprintf("原 Codex 已启动的实验队列\n总计 %d · 运行 %d · 排队 %d · 完成 %d · 失败 %d", len(q.Jobs), counts["running"], counts["pending"], counts["complete"], counts["failed"])
	if info, e := os.Stat(p); e == nil {
		summary += "\n调度记录更新：" + info.ModTime().In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("01-02 15:04:05")
	}
	summary += "\n\n" + strings.Join(lines, "\n\n") + "\n\nCodex 任务说明：\n" + c.Context
	return watches.Update{Source: "training", Conversation: c.Conversation, Thread: c.Thread, Title: c.Title, State: state, Text: summary, Sequence: uint64(now.UnixNano()), Observed: now.UTC()}, nil
}
func run(c config) error {
	var worker struct {
		Relay   string `json:"relay_url"`
		KeyFile string `json:"relay_key_file"`
	}
	b, e := os.ReadFile(c.WorkerConfig)
	if e != nil || json.Unmarshal(b, &worker) != nil {
		return errors.New("worker_config_unavailable")
	}
	key, e := os.ReadFile(worker.KeyFile)
	if e != nil {
		return e
	}
	u, e := snapshot(c, time.Now())
	if e != nil {
		return e
	}
	body, e := json.Marshal(u)
	if e != nil {
		return e
	}
	req, e := http.NewRequest("POST", strings.TrimRight(worker.Relay, "/")+"/watch/update", bytes.NewReader(body))
	if e != nil {
		return e
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(key)))
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{Timeout: 15 * time.Second}
	resp, e := client.Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != 200 {
		return errors.New("watch_update_not_accepted")
	}
	return nil
}
func main() {
	path := flag.String("config", "", "private configuration")
	once := flag.Bool("once", false, "read and publish once")
	flag.Parse()
	b, e := os.ReadFile(*path)
	var c config
	if e != nil || json.Unmarshal(b, &c) != nil || !filepath.IsAbs(c.Root) {
		fmt.Println(`{"ok":false,"error":"invalid_config"}`)
		os.Exit(1)
	}
	for {
		e = run(c)
		if e != nil {
			fmt.Println(`{"ok":false,"error":"queue_sync_failed"}`)
		} else {
			fmt.Println(`{"ok":true,"source":"training"}`)
		}
		if *once {
			if e != nil {
				os.Exit(1)
			}
			return
		}
		time.Sleep(30 * time.Second)
	}
}
