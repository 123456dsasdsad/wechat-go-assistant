package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestQueueUsesRealJobCountsAndMetricsWithoutChangingTraining(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "status"), 0700)
	p := filepath.Join(root, "status", "experiment_queue.json")
	b := []byte(`{"counts":{"running":99},"jobs":[{"id":"run1","method":"paper","dataset":"set1","seed":1,"gpu":2,"status":"running"},{"id":"run2","status":"pending"}]}`)
	os.WriteFile(p, b, 0600)
	m := filepath.Join(root, "results", "run1", "fold_0")
	os.MkdirAll(m, 0700)
	os.WriteFile(filepath.Join(m, "live_progress.json"), []byte(`{"epoch":4,"microstep":200,"optimizer_update":25,"loss":0.42,"private":"never expose"}`), 0600)
	u, e := snapshot(config{Root: root}, time.Now())
	if e != nil || !strings.Contains(u.Text, "总计 2 · 运行 1 · 排队 1") || !strings.Contains(u.Text, "轮次 4") || !strings.Contains(u.Text, "loss 0.42") || strings.Contains(u.Text, "never expose") {
		t.Fatal(u, e)
	}
	after, _ := os.ReadFile(p)
	if string(after) != string(b) {
		t.Fatal("collector changed training")
	}
	os.WriteFile(p, []byte(`{"jobs":[{"id":"run1","status":"complete"}]}`), 0600)
	u, e = snapshot(config{Root: root}, time.Now())
	if e != nil || u.State != "done" {
		t.Fatal(u, e)
	}
	os.WriteFile(p, []byte(`{"jobs":[{"id":"../../escape","status":"running"}]}`), 0600)
	if _, e = snapshot(config{Root: root}, time.Now()); e == nil {
		t.Fatal("escaped experiment directory")
	}
}

func TestBaselineHistoryShowsCompletedEpochs(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "status"), 0700)
	os.WriteFile(filepath.Join(root, "status", "experiment_queue.json"), []byte(`{"jobs":[{"id":"baseline1","family":"baseline","method":"mitunet","status":"running"}]}`), 0600)
	result := filepath.Join(root, "results", "baseline1")
	os.MkdirAll(result, 0700)
	os.WriteFile(filepath.Join(result, "history.json"), []byte(`[{"epoch":2,"train_loss":0.4},{"epoch":3,"train_loss":0.3,"val_macro_F1":0.81,"seconds":1200}]`), 0600)
	u, err := snapshot(config{Root: root}, time.Now())
	if err != nil || !strings.Contains(u.Text, "已完成 3 / 120 轮") || !strings.Contains(u.Text, "loss 0.3") || !strings.Contains(u.Text, "验证 F1 81.00%") || strings.Contains(u.Text, "等待首份") {
		t.Fatal(u.Text, err)
	}
}

func TestTimestampedBaselineLogsShowWithinEpochProgress(t *testing.T) {
	logs := "2026-10-10T03:00:01.123456789Z {\"epoch\":11,\"update\":150,\"loss\":0.7}\n" +
		"2026-10-10T03:02:01.987654321Z {\"epoch\":11,\"update\":175,\"loss\":0.6}\n"
	m, updated, ok := parseDockerProgress(logs)
	if !ok || m.Epoch != 11 || m.Updates != 175 || m.Loss == nil || *m.Loss != 0.6 || updated.UTC().Format(time.RFC3339) != "2026-10-10T03:02:01Z" {
		t.Fatal(m, updated, ok)
	}
	if _, _, ok := parseDockerProgress("2026-10-10T03:00:01Z invalid\n"); ok {
		t.Fatal("invalid log accepted")
	}
}

func TestMissingProgressDoesNotClaimTrainingDependency(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "status"), 0700)
	os.WriteFile(filepath.Join(root, "status", "experiment_queue.json"), []byte(`{"jobs":[{"id":"run1","status":"running"}]}`), 0600)
	u, err := snapshot(config{Root: root}, time.Now())
	if err != nil || strings.Contains(u.Text, "等待") || !strings.Contains(u.Text, "暂未读取到训练进度") {
		t.Fatal(u.Text, err)
	}
}
